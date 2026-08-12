// Package pipeline orchestrates Source.Fetch → Resolve → Quote upsert.
package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"buff-go/internal/buffgo/catalog"
	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/nameid"
	"buff-go/internal/buffgo/pool"
	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/steam"
	"buff-go/internal/buffgo/store"
	"buff-go/internal/telemetry"

	"github.com/go-redis/redis/v8"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// SteamSellRunResult is the outcome of one steam.sell pipeline pass.
type SteamSellRunResult struct {
	JobKey        string
	AppID         int64
	Fetched       int
	Resolved      int
	Unresolved    int
	QuotesWritten int
	Sample        []store.Quote
	Skipped       []string // unresolved or error messages (capped)
	NextStart     int      // resume offset after an incomplete crawl
	Completeness  Completeness
}

// SteamSellRunner runs Fetch → Resolve → Upsert for steam.sell jobs.
// Resolver / Quotes / Source are interfaces so offline fixture tests can
// inject MemoryResolver + MemoryQuoteStore without PostgreSQL.
type SteamSellRunner struct {
	DB       *sql.DB // optional; required only when wiring default PG components
	Source   OfferFetcher
	Resolver ItemResolver
	Quotes   QuoteWriter
	// MaxPages / Count override per-job fetch size (0 = full crawl).
	MaxPages int
	Count    int
	// ProxyID keys search rate limits for Pull (empty → source default / direct).
	// Set per job when StaticProvider selects a proxy for HTTP egress.
	ProxyID string
	// WorkerID and LeaseID link job events to the selected resource combination.
	WorkerID string
	LeaseID  string
	// Obs records fetch/job success/failure (P5.3). nil uses telemetry.Default().
	Obs telemetry.Recorder
}

func (r *SteamSellRunner) jobResources() telemetry.JobResources {
	if r == nil {
		return telemetry.JobResources{}
	}
	return telemetry.JobResources{WorkerID: r.WorkerID, Proxy: r.ProxyID, LeaseID: r.LeaseID}
}

func (r *SteamSellRunner) recorder() telemetry.Recorder {
	if r != nil && r.Obs != nil {
		return r.Obs
	}
	return telemetry.Default()
}

// NewSteamSellRunner wires PG-backed components that share the same *sql.DB.
func NewSteamSellRunner(db *sql.DB, src *source.SteamSellSource) *SteamSellRunner {
	if src == nil {
		src = source.NewSteamSellSource(source.SteamSellOptions{})
	}
	return &SteamSellRunner{
		DB:       db,
		Source:   src,
		Resolver: resolve.New(db),
		Quotes:   store.NewQuoteStore(db),
		MaxPages: 0,
	}
}

// NewSteamSellRunnerParts builds a runner from explicit dependencies (tests / custom wiring).
func NewSteamSellRunnerParts(src OfferFetcher, resolver ItemResolver, quotes QuoteWriter) *SteamSellRunner {
	return &SteamSellRunner{
		Source:   src,
		Resolver: resolver,
		Quotes:   quotes,
		MaxPages: 0,
	}
}

// RunJob executes one job: fetch offers, resolve items, upsert quotes.
// Emits observ fetch/job success or failure events (P5.3).
func (r *SteamSellRunner) RunJob(ctx context.Context, job source.JobSpec) (SteamSellRunResult, error) {
	res := SteamSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}
	srcName := source.NameSteamAsk
	platform := source.PlatformSteam
	resources := r.jobResources()
	if r == nil || r.Source == nil || r.Resolver == nil || r.Quotes == nil {
		err := fmt.Errorf("steam.sell pipeline: incomplete runner")
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonError, err.Error(), resources)
		return res, err
	}
	if job.AppID <= 0 {
		err := fmt.Errorf("steam.sell pipeline: appid required")
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonInvalid, err.Error(), resources)
		return res, err
	}
	if job.Side != "" && job.Side != source.SideAsk {
		err := fmt.Errorf("steam.sell pipeline: side must be ask")
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonInvalid, err.Error(), resources)
		return res, err
	}
	if err := ensureQuoteWriterReady(r.Quotes); err != nil {
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonError, err.Error(), resources)
		return res, fmt.Errorf("market storage unavailable: %w", err)
	}
	job.Platform = source.PlatformSteam
	job.Side = source.SideAsk

	maxPages := r.MaxPages
	if job.MaxPages > 0 {
		maxPages = job.MaxPages
	}
	count := r.Count
	if job.Count > 0 {
		count = job.Count
	}
	opts := source.SteamSellOptions{
		AppID:     job.AppID,
		Start:     job.Start,
		Count:     count,
		MaxPages:  maxPages,
		PageDelay: job.PageDelay,
		ProxyID:   strings.TrimSpace(r.ProxyID),
	}
	offers, err := r.Source.Pull(ctx, opts)
	// A later-page failure may return completed offers + a resume cursor.
	var partialErr error
	if err != nil {
		if pe, ok := source.AsPartialPull(err); ok && len(pe.Offers) > 0 {
			offers = pe.Offers
			res.NextStart = pe.Start
			partialErr = pe.Unwrap()
			if partialErr == nil {
				partialErr = err
			}
			log.Printf("buffgo worker: steam.sell partial fetch appid=%d job_ref=%s offers=%d page=%d error_ref=%s",
				job.AppID, telemetry.SafeJobRef(job.Key), len(offers), pe.Page, telemetry.SafeErrorRef(partialErr))
		} else {
			telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonError, err.Error(), resources)
			return res, fmt.Errorf("fetch: %w", err)
		}
	}
	res.Fetched = len(offers)
	if partialErr == nil {
		res.Completeness = CompletenessComplete
	}
	fetchFailed := partialErr != nil && !errors.Is(partialErr, source.ErrPageLimit)
	if fetchFailed {
		telemetry.RecordFetchFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, telemetry.ReasonError, partialErr.Error(), resources)
	}
	recordFetchOK := func() {
		if !fetchFailed {
			telemetry.RecordFetchOKWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, res.Fetched, resources)
		}
	}
	if len(offers) == 0 {
		telemetry.RecordJobOKWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, 0, 0, resources)
		return res, nil
	}

	quotes := make([]store.Quote, 0, len(offers))
	for i, o := range offers {
		if err := validateOfferSide(o, source.SideAsk); err != nil {
			recordFetchOK()
			telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, false, telemetry.ReasonInvalid, err.Error(), resources)
			return res, fmt.Errorf("market quote item_index=%d: %w", i, err)
		}
		if err := validateOfferScope(o, source.PlatformSteam, job.AppID); err != nil {
			recordFetchOK()
			telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, false, telemetry.ReasonInvalid, err.Error(), resources)
			return res, fmt.Errorf("market quote item_index=%d: %w", i, err)
		}
		resolved, err := r.Resolver.Resolve(ctx, o)
		if err == nil {
			err = validateIdentityMatch(resolved)
		}
		if err != nil {
			res.Unresolved++
			if len(res.Skipped) < 10 {
				res.Skipped = append(res.Skipped, fmt.Sprintf("item_index=%d error_ref=%s", i, telemetry.SafeErrorRef(err)))
			}
			continue
		}
		res.Resolved++
		quote, err := store.QuoteFromOffer(int64(resolved.ProductID), o)
		if err != nil {
			recordFetchOK()
			telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, false, telemetry.ReasonInvalid, err.Error(), resources)
			return res, fmt.Errorf("market quote item_index=%d: %w", i, err)
		}
		quotes = append(quotes, quote)
	}
	if len(quotes) == 0 {
		err := fmt.Errorf("steam.sell: resolved 0/%d offers for appid=%d", res.Fetched, job.AppID)
		// fetch itself succeeded; job fails on resolve
		recordFetchOK()
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, false, telemetry.ReasonUnresolved, err.Error(), resources)
		return res, err
	}

	up, err := r.Quotes.UpsertQuotes(ctx, quotes)
	if err != nil {
		recordFetchOK()
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, false, telemetry.ReasonError, err.Error(), resources)
		return res, fmt.Errorf("upsert quotes: %w", err)
	}
	res.QuotesWritten = up.Written

	sample, err := r.Quotes.ListByAppIDPlatformSide(ctx, job.AppID, source.PlatformSteam, string(source.SideAsk), 5)
	if err == nil {
		res.Sample = sample
	}
	if partialErr != nil {
		if errors.Is(partialErr, source.ErrPageLimit) {
			telemetry.RecordJobOKWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, res.Fetched, res.QuotesWritten, resources)
			return res, nil
		}
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, false, telemetry.ReasonError, partialErr.Error(), resources)
		return res, fmt.Errorf("steam.sell partial after %d quotes: %w", res.QuotesWritten, partialErr)
	}
	telemetry.RecordJobOKWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, res.Fetched, res.QuotesWritten, resources)
	return res, nil
}

// SteamSellRunOptions optional CLI/runtime overrides for steam.sell sizing.
type SteamSellRunOptions struct {
	MaxPages  int
	Count     int
	PageDelay time.Duration
}

// SteamSellRuntime holds optional process-level deps for config-driven steam.sell runs.
// Redis non-nil prefers steam.RedisSearchLimiter and enables pool.Manager
// Acquire/Release so concurrent jobs do not double-lease the same proxy/platform.
// Nil Redis uses MemorySearchLimiter and static first-candidate
// proxy selection without leasing (unit tests without Redis still work).
//
// Injected *source.SteamSellSource still wins (tests/httptest): proxy selection,
// HTTP client, limiter, and leasing are only wired when src == nil.
//
// Manager is built inside the pipeline from Redis+cfg when Redis != nil (keeps run.go thin).
// WorkerID is optional; empty → hostname-pid (or worker-<hex> fallback), stable for the Run call.
type SteamSellRuntime struct {
	Redis    *redis.Client
	WorkerID string // optional override for tests / ops
}

// RunSteamSellFromConfig opens PG and runs all matching steam.sell jobs once.
// onlyAppID 0 = all enabled appids; >0 runs jobs for that game only (worker --appid).
func RunSteamSellFromConfig(ctx context.Context, cfg *config.Config, sourcesAllowlist string, src *source.SteamSellSource) ([]SteamSellRunResult, error) {
	return RunSteamSellFromConfigAppID(ctx, cfg, sourcesAllowlist, 0, src, SteamSellRunOptions{}, SteamSellRuntime{})
}

// searchLimitConfigFrom builds SearchLimitConfig from [steam.search] (zeros → package defaults via Normalize).
func searchLimitConfigFrom(cfg *config.Config) steam.SearchLimitConfig {
	limCfg := steam.SearchLimitConfig{}
	if cfg == nil {
		return limCfg
	}
	limCfg.SoftMax = cfg.SteamSearchMaxPerWindow()
	limCfg.HardMax = cfg.SteamSearchHardMaxPerWindow()
	limCfg.Window = cfg.SteamSearchWindow()
	limCfg.CooldownOn429 = cfg.SteamSearchCooldownOn429()
	limCfg.MinInterval = cfg.SteamSearchMinInterval()
	return limCfg
}

// searchLimiterFromConfig prefers RedisSearchLimiter when rdb is non-nil; otherwise MemorySearchLimiter.
func searchLimiterFromConfig(cfg *config.Config, rdb *redis.Client) steam.SearchLimiter {
	limCfg := searchLimitConfigFrom(cfg)
	soft := 80
	cooldown := 3 * time.Minute
	if cfg != nil {
		soft = cfg.SteamSearchMaxPerWindowOrDefault()
		cooldown = cfg.SteamSearchCooldownOn429OrDefault()
	}
	if rdb != nil {
		lim, err := steam.NewRedisSearchLimiter(rdb, limCfg, steam.RedisSearchLimitOptions{})
		if err != nil {
			log.Printf("buffgo worker: redis search limiter failed error_ref=%s; falling back to memory search limiter soft=%d cooldown=%s",
				telemetry.SafeErrorRef(err), soft, cooldown)
			return steam.NewMemorySearchLimiter(limCfg)
		}
		log.Printf("buffgo worker: redis search limiter soft=%d cooldown=%s", soft, cooldown)
		return lim
	}
	log.Printf("buffgo worker: memory search limiter soft=%d cooldown=%s", soft, cooldown)
	return steam.NewMemorySearchLimiter(limCfg)
}

// staticProviderFromConfig builds pool.StaticProvider from config [[proxies]].
// Empty / all-disabled → direct mode (proxy_id=direct).
func staticProviderFromConfig(cfg *config.Config) *pool.StaticProvider {
	if cfg == nil {
		return pool.NewStaticProvider(nil)
	}
	in := cfg.StaticProxyInputs()
	rows := make([]pool.StaticProxyInput, len(in))
	for i, r := range in {
		rows[i] = pool.StaticProxyInput{
			Endpoint:      r.Endpoint,
			Auth:          r.Auth,
			LineType:      r.LineType,
			OnlyAppIDs:    r.OnlyAppIDs,
			PreferAppIDs:  r.PreferAppIDs,
			Enabled:       r.Enabled,
			MaxConcurrent: r.MaxConcurrent,
		}
	}
	return pool.NewStaticProviderFromInput(rows)
}

// resolveProxyForAppID picks the first ordered candidate for appid and returns
// the full ProxyEndpoint (endpoint URL + auth) from the provider List.
// Empty provider / no candidates → DirectEndpoint (proxy_id=direct).
func resolveProxyForAppID(ctx context.Context, prov pool.ProxyProvider, appid int64) (pool.ProxyEndpoint, error) {
	if prov == nil {
		return pool.DirectEndpoint(), nil
	}
	cands, err := pool.CandidatesFromProvider(ctx, prov, appid)
	if err != nil {
		return pool.ProxyEndpoint{}, err
	}
	if len(cands) == 0 {
		return pool.DirectEndpoint(), nil
	}
	want := cands[0].ID
	list, err := prov.List(ctx)
	if err != nil {
		return pool.ProxyEndpoint{}, err
	}
	for _, e := range list {
		if e.ProxyID() == want {
			return e, nil
		}
	}
	if want == pool.ProxyIDDirect || want == steam.ProxyDirect {
		return pool.DirectEndpoint(), nil
	}
	// Candidate ID not found in list — stay safe with direct.
	return pool.DirectEndpoint(), nil
}

// proxyURLFromEndpoint builds a proxy *url.URL for http.Transport.Proxy.
// Supports:
//   - full URL: http://user:pass@host:port or http://host:port
//   - host:port with Auth "user:pass" → http://user:pass@host:port
//   - host:port without auth → http://host:port
//
// Empty / "direct" returns (nil, nil) meaning no HTTP proxy.
func proxyURLFromEndpoint(endpoint, auth string) (*url.URL, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" || strings.EqualFold(ep, pool.ProxyIDDirect) || strings.EqualFold(ep, steam.ProxyDirect) {
		return nil, nil
	}
	auth = strings.TrimSpace(auth)
	// Already a URL with scheme?
	if strings.Contains(ep, "://") {
		u, err := url.Parse(ep)
		if err != nil {
			return nil, telemetry.WrapError("proxy URL parse", err)
		}
		if u.User == nil && auth != "" {
			if user, pass, ok := strings.Cut(auth, ":"); ok {
				u.User = url.UserPassword(user, pass)
			} else {
				u.User = url.User(auth)
			}
		}
		return u, nil
	}
	// host:port form
	raw := "http://" + ep
	if auth != "" {
		if user, pass, ok := strings.Cut(auth, ":"); ok {
			raw = "http://" + url.UserPassword(user, pass).String() + "@" + ep
		} else {
			raw = "http://" + url.User(auth).String() + "@" + ep
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, telemetry.WrapError("proxy host parse", err)
	}
	return u, nil
}

// HTTPClientForProxy builds an *http.Client for a proxy endpoint.
// Direct (empty / "direct") → default client, no Transport.Proxy.
// Timeout is always 30s.
func HTTPClientForProxy(endpoint, auth string) (*http.Client, error) {
	const timeout = 30 * time.Second
	u, err := proxyURLFromEndpoint(endpoint, auth)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return &http.Client{Timeout: timeout}, nil
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(u),
		},
	}, nil
}

// HTTPClientForEndpoint is HTTPClientForProxy using a pool.ProxyEndpoint.
func HTTPClientForEndpoint(ep pool.ProxyEndpoint) (*http.Client, error) {
	if ep.IsDirect() {
		return HTTPClientForProxy(pool.ProxyIDDirect, "")
	}
	return HTTPClientForProxy(ep.Endpoint, ep.Auth)
}

// RunSteamSellFromConfigAppID is RunSteamSellFromConfig with an optional appid filter,
// sizing overrides, and optional runtime deps (Redis for shared search limiter + pool leases).
//
// When src is nil (production worker path):
//   - SearchLimiter: Redis when rt.Redis != nil, else memory
//   - Proxy: config [[proxies]] via StaticProvider; empty → proxy_id=direct + default client
//   - When rt.Redis != nil: pool.Manager Acquire per job (try ordered candidates until one
//     lease succeeds); Release on exit; SetCooldown on steam search 429 / rate-limit
//   - When rt.Redis == nil: first ordered candidate without lease (tests without Redis)
//
// When src is non-nil (tests/httptest): used as-is for all jobs; no proxy/limiter/lease rewiring.
func RunSteamSellFromConfigAppID(ctx context.Context, cfg *config.Config, sourcesAllowlist string, onlyAppID int64, src *source.SteamSellSource, size SteamSellRunOptions, rt SteamSellRuntime) ([]SteamSellRunResult, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if onlyAppID > 0 && !cfg.HasAppID(onlyAppID) {
		return nil, fmt.Errorf("appid=%d not in enabled_appids=%v", onlyAppID, cfg.Games.EnabledAppIDs)
	}
	jobs := JobsFromConfigAppID(cfg, sourcesAllowlist, onlyAppID)
	if len(jobs) == 0 {
		return nil, nil
	}
	return RunSteamSellJobs(ctx, cfg, jobs, size, rt, src)
}

// RunSteamSellJobs runs an explicit list of JobSpecs.
// Same lease, rate-limit, and proxy wiring as RunSteamSellFromConfigAppID.
func RunSteamSellJobs(ctx context.Context, cfg *config.Config, jobs []source.JobSpec, size SteamSellRunOptions, rt SteamSellRuntime, src *source.SteamSellSource) ([]SteamSellRunResult, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if len(jobs) == 0 {
		return nil, nil
	}

	injected := src != nil
	var lim steam.SearchLimiter
	var prov *pool.StaticProvider
	var mgr *pool.Manager
	workerID := resolveWorkerID(rt.WorkerID)
	if !injected {
		lim = searchLimiterFromConfig(cfg, rt.Redis)
		prov = staticProviderFromConfig(cfg)
		if prov.IsDirectMode() {
			log.Printf("buffgo worker: steam.sell proxies empty/disabled → direct mode (node_ref=%s)", telemetry.SafeNodeRef(pool.ProxyIDDirect))
		} else {
			log.Printf("buffgo worker: steam.sell static proxies configured=%d (enabled listed via StaticProvider)", prov.LenConfigured())
		}
		if rt.Redis != nil {
			m, err := poolManagerFromConfig(cfg, rt.Redis)
			if err != nil {
				return nil, fmt.Errorf("steam.sell pool manager: %w", err)
			}
			mgr = m
			log.Printf("buffgo worker: pool manager enabled worker_ref=%s lease_ttl=%s default_cooldown=%s",
				telemetry.SafeWorkerRef(workerID), mgr.LeaseTTL(), mgr.DefaultCooldown())
		} else {
			log.Printf("buffgo worker: redis nil → steam.sell static first-candidate (no proxy lease)")
		}
	}

	// Apply process overrides onto jobs (config job fields remain default).
	for i := range jobs {
		if size.MaxPages > 0 {
			jobs[i].MaxPages = size.MaxPages
		}
		if size.Count > 0 {
			jobs[i].Count = size.Count
		}
		if size.PageDelay > 0 {
			jobs[i].PageDelay = size.PageDelay
		}
	}

	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(time.Minute)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(cctx); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}

	// Optional capped nameid backfill after successful sell (env, default off).
	// Does not affect sell quote success; failures only log.
	// Resolver is built per job so listing HTML reuses the job's HTTP client/proxy.
	nameidLimit := nameid.LimitFromEnv()
	var nameidStore *catalog.Store
	if nameidLimit > 0 {
		nameidStore = catalog.NewStore(db)
		log.Printf("buffgo worker: nameid backfill enabled limit=%d delay=%s", nameidLimit, nameid.DelayFromEnv())
	}

	// Cooldown applied on Release when fetch hits steam search 429 / rate-limit.
	searchCooldown := 3 * time.Minute
	if cfg != nil {
		searchCooldown = cfg.SteamSearchCooldownOn429OrDefault()
	}

	results := make([]SteamSellRunResult, 0, len(jobs))
	for _, job := range jobs {
		res, err := runSteamSellJobOnce(ctx, steamSellJobDeps{
			db:             db,
			src:            src,
			injected:       injected,
			lim:            lim,
			prov:           prov,
			mgr:            mgr,
			workerID:       workerID,
			size:           size,
			searchCooldown: searchCooldown,
			nameidLimit:    nameidLimit,
			nameidStore:    nameidStore,
			job:            job,
		})
		if err != nil {
			// Soft-skip: no lease, or steam search budget/429. Pool cooldown already applied
			// on Release in runSteamSellJobOnce. Once-mode returns nil when only soft errors.
			if isSoftSkipJobErr(err) {
				if steam.IsSearchRateLimit(err) {
					log.Printf("buffgo worker: rate limited soft-skip job_ref=%s error_ref=%s",
						telemetry.SafeJobRef(job.Key), telemetry.SafeErrorRef(err))
				} else {
					log.Printf("buffgo worker: steam.sell skip job_ref=%s appid=%d error_ref=%s",
						telemetry.SafeJobRef(job.Key), job.AppID, telemetry.SafeErrorRef(err))
				}
				if steam.IsSearchRateLimit(err) {
					res.Completeness = CompletenessPartial
					if res.NextStart == 0 {
						res.NextStart = job.Start
					}
				}
				res.Skipped = append(res.Skipped, telemetry.SafeErrorRef(err))
				results = append(results, res)
				continue
			}
			results = append(results, res)
			return results, telemetry.WrapError("steam sell job", err)
		}
		results = append(results, res)
	}
	return results, nil
}

// steamSellJobDeps is the per-job wiring for runSteamSellJobOnce (keeps defer Release safe in loops).
type steamSellJobDeps struct {
	db             *sql.DB
	src            *source.SteamSellSource
	injected       bool
	lim            steam.SearchLimiter
	prov           *pool.StaticProvider
	mgr            *pool.Manager
	workerID       string
	size           SteamSellRunOptions
	searchCooldown time.Duration
	nameidLimit    int
	nameidStore    *catalog.Store
	job            source.JobSpec
}

// runSteamSellJobOnce selects/leases a proxy (production), fetches, and always releases a held lease.
func runSteamSellJobOnce(ctx context.Context, d steamSellJobDeps) (SteamSellRunResult, error) {
	job := d.job
	jobSrc := d.src
	proxyID := steam.ProxyDirect
	var lease *pool.Lease
	// jobHTTP is the sell job's egress client (proxy transport when configured).
	// Used for optional nameid backfill so listing HTML shares the same path.
	var jobHTTP *http.Client
	// runCtx is cancelled if lease Renew fails (fail-closed); defaults to parent ctx.
	runCtx := ctx
	var stopRenewer func()

	if !d.injected {
		var ep pool.ProxyEndpoint
		if d.mgr != nil {
			// Acquire ordered candidates until one succeeds.
			l, endpoint, err := acquireSteamProxyLease(ctx, d.mgr, d.prov, d.workerID, job.AppID)
			if err != nil {
				return SteamSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}, err
			}
			lease = l
			ep = endpoint
			// Keep exclusive lease alive across multi-page fetches (LeaseTTL default 30s).
			runCtx, stopRenewer = startLeaseRenewer(ctx, d.mgr, lease.ID, d.workerID, leaseRenewInterval(d.mgr.LeaseTTL()))
		} else {
			// No Redis / no Manager: static first candidate without lease.
			var err error
			ep, err = resolveProxyForAppID(ctx, d.prov, job.AppID)
			if err != nil {
				return SteamSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}, fmt.Errorf("proxy select: %w", err)
			}
		}
		proxyID = ep.ProxyID()
		if lease != nil {
			// Prefer lease identity (normalized) for rate-limit keys.
			proxyID = lease.Proxy
		}
		client, err := HTTPClientForEndpoint(ep)
		if err != nil {
			if stopRenewer != nil {
				stopRenewer()
				stopRenewer = nil
			}
			if lease != nil {
				releaseSteamProxyLease(ctx, d.mgr, lease, d.workerID, pool.ReleaseOptions{})
			}
			return SteamSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}, fmt.Errorf("http client: %w", err)
		}
		jobHTTP = client
		jobSrc = source.NewSteamSellSource(source.SteamSellOptions{
			HTTPClient:    client,
			ProxyID:       proxyID,
			SearchLimiter: d.lim,
		})
	} else if jobSrc != nil {
		// Prefer source-configured proxy key when present (tests).
		if id := strings.TrimSpace(jobSrc.ProxyID()); id != "" {
			proxyID = steam.NormalizeProxyID(id)
		}
	}

	// Always stop renewer then Release a held lease; set cooldown after steam search rate-limit / 429.
	// Release is completed before optional nameid backfill so listing HTML does not
	// extend exclusive (proxy, platform) hold (R5). HTTP client may outlive the lease.
	var fetchErr error
	var leaseReleased bool
	releaseLease := func() {
		if lease == nil || leaseReleased {
			return
		}
		if stopRenewer != nil {
			stopRenewer()
			stopRenewer = nil
		}
		opts := releaseOptsForFetchErr(fetchErr, d.searchCooldown)
		releaseSteamProxyLease(context.Background(), d.mgr, lease, d.workerID, opts)
		leaseReleased = true
	}
	if lease != nil {
		defer releaseLease()
	}

	runner := NewSteamSellRunner(d.db, jobSrc)
	runner.ProxyID = proxyID
	runner.WorkerID = d.workerID
	if lease != nil {
		runner.LeaseID = lease.ID
	}
	if d.size.MaxPages > 0 {
		runner.MaxPages = d.size.MaxPages
	}
	if d.size.Count > 0 {
		runner.Count = d.size.Count
	}

	pages := job.MaxPages
	if pages == 0 {
		pages = runner.MaxPages
	}
	leaseRef := ""
	if lease != nil {
		leaseRef = telemetry.SafeLeaseRef(lease.ID)
	}
	pageLabel := "all"
	if pages > 0 {
		pageLabel = fmt.Sprintf("%d", pages)
	}
	log.Printf("buffgo worker: steam.sell fetch appid=%d job_ref=%s pages=%s count=%d delay=%s node_ref=%s lease_ref=%s worker_ref=%s",
		job.AppID, telemetry.SafeJobRef(job.Key), pageLabel, job.Count, job.PageDelay,
		telemetry.SafeNodeRef(proxyID), leaseRef, telemetry.SafeWorkerRef(d.workerID))
	jctx := runCtx
	jcancel := func() {}
	if pages > 0 {
		timeout := time.Duration(pages)*45*time.Second + 2*time.Minute
		jctx, jcancel = context.WithTimeout(runCtx, timeout)
	}
	res, err := runner.RunJob(jctx, job)
	jcancel()
	fetchErr = err
	// stop renewer + Release before nameid — keep leased section = fetch only.
	releaseLease()
	if err != nil {
		return res, err
	}
	log.Printf("buffgo worker: steam.sell appid=%d node_ref=%s fetched=%d resolved=%d quotes=%d unresolved=%d",
		res.AppID, telemetry.SafeNodeRef(proxyID), res.Fetched, res.Resolved, res.QuotesWritten, res.Unresolved)
	for _, q := range res.Sample {
		hash := ""
		if q.SourceMeta != nil {
			hash = q.SourceMeta["market_hash_name"]
		}
		log.Printf("buffgo worker: sample quote item_id=%d item_ref=%s price_cents=%d currency=CNY",
			q.ItemID, telemetry.SafeDetailRef(hash), q.PriceCents)
	}
	// Light optional fill: never fails the sell job; runs outside lease hold.
	// Reuse job HTTP client when proxy is non-direct; otherwise default client.
	if d.nameidLimit > 0 {
		var nameidHC *http.Client
		pid := steam.NormalizeProxyID(proxyID)
		if jobHTTP != nil && pid != steam.ProxyDirect {
			nameidHC = jobHTTP
		}
		nameidResolver := nameid.NewResolverWithClient(nameidHC, proxyID)
		_ = nameid.MaybeBackfillAfterSell(ctx, d.nameidStore, nameidResolver, job.AppID, d.nameidLimit)
	}
	return res, nil
}
