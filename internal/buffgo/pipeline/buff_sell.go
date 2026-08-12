package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/pool"
	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
	"buff-go/internal/telemetry"

	"github.com/go-redis/redis/v8"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// BuffSellRunResult is the outcome of one buff.sell pipeline pass.
type BuffSellRunResult struct {
	JobKey        string
	AppID         int64
	Fetched       int
	Resolved      int
	Unresolved    int
	QuotesWritten int
	Sample        []store.Quote
	Skipped       []string
	NextStart     int // resume page after an incomplete crawl
	Completeness  Completeness
}

// BuffOfferFetcher pulls Buff sell offers (usually *source.BuffSellSource).
type BuffOfferFetcher interface {
	Pull(ctx context.Context, opts source.BuffSellOptions) ([]source.RawOffer, error)
}

// BuffSellRunner runs Fetch → Resolve → Upsert for buff.sell jobs.
// Resolver / Quotes / Source are interfaces so offline fixture tests can
// inject MemoryResolver + MemoryQuoteStore without PostgreSQL.
type BuffSellRunner struct {
	DB       *sql.DB
	Source   BuffOfferFetcher
	Resolver ItemResolver
	Quotes   QuoteWriter
	// Cookie is optional session for needs_account; Lease.Account overrides.
	Cookie string
	// WorkerID, ProxyID and LeaseID link events to the active resource combination.
	WorkerID string
	ProxyID  string
	LeaseID  string
	// MaxPages / PageSize override per-job fetch size (0 = full crawl).
	MaxPages int
	PageSize int
	// Obs records fetch/job success/failure (P5.3). nil uses telemetry.Default().
	Obs telemetry.Recorder
}

func (r *BuffSellRunner) jobResources() telemetry.JobResources {
	if r == nil {
		return telemetry.JobResources{}
	}
	return telemetry.JobResources{
		WorkerID: r.WorkerID,
		Proxy:    r.ProxyID,
		Account:  r.Cookie,
		LeaseID:  r.LeaseID,
	}
}

func (r *BuffSellRunner) recorder() telemetry.Recorder {
	if r != nil && r.Obs != nil {
		return r.Obs
	}
	return telemetry.Default()
}

// NewBuffSellRunner wires PG-backed components that share the same *sql.DB.
func NewBuffSellRunner(db *sql.DB, src *source.BuffSellSource) *BuffSellRunner {
	if src == nil {
		src = source.NewBuffSellSource(source.BuffSellOptions{})
	}
	return &BuffSellRunner{
		DB:       db,
		Source:   src,
		Resolver: resolve.New(db),
		Quotes:   store.NewQuoteStore(db),
		Cookie:   src.Cookie(),
		MaxPages: 0,
	}
}

// NewBuffSellRunnerParts builds a runner from explicit dependencies (tests / custom wiring).
func NewBuffSellRunnerParts(src BuffOfferFetcher, resolver ItemResolver, quotes QuoteWriter) *BuffSellRunner {
	return &BuffSellRunner{
		Source:   src,
		Resolver: resolver,
		Quotes:   quotes,
		MaxPages: 0,
	}
}

// RunJob executes one job: fetch offers, resolve items, upsert quotes.
// Emits observ fetch/job success or failure events (P5.3).
func (r *BuffSellRunner) RunJob(ctx context.Context, job source.JobSpec) (BuffSellRunResult, error) {
	res := BuffSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}
	srcName := source.NameBuffAsk
	platform := source.PlatformBuff
	resources := r.jobResources()
	if r == nil || r.Source == nil || r.Resolver == nil || r.Quotes == nil {
		err := fmt.Errorf("buff.sell pipeline: incomplete runner")
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonError, err.Error(), resources)
		return res, err
	}
	if job.AppID <= 0 {
		err := fmt.Errorf("buff.sell pipeline: appid required")
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonInvalid, err.Error(), resources)
		return res, err
	}
	if job.Side != "" && job.Side != source.SideAsk {
		err := fmt.Errorf("buff.sell pipeline: side must be ask")
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonInvalid, err.Error(), resources)
		return res, err
	}
	if err := ensureQuoteWriterReady(r.Quotes); err != nil {
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, true, telemetry.ReasonError, err.Error(), resources)
		return res, fmt.Errorf("market storage unavailable: %w", err)
	}
	job.Platform = source.PlatformBuff
	job.Side = source.SideAsk

	// Prefer per-job MaxPages from config JobSpec when set (mirror SteamSellRunner).
	maxPages := r.MaxPages
	if job.MaxPages > 0 {
		maxPages = job.MaxPages
	}
	opts := source.BuffSellOptions{
		AppID:    job.AppID,
		PageNum:  job.Start,
		PageSize: r.PageSize,
		MaxPages: maxPages,
		Cookie:   r.Cookie,
	}
	offers, err := r.Source.Pull(ctx, opts)
	var partialErr error
	if err != nil {
		if pe, ok := source.AsBuffPartialPull(err); ok && len(pe.Offers) > 0 {
			offers = pe.Offers
			res.NextStart = pe.NextPage
			partialErr = err
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
		if err := validateOfferScope(o, source.PlatformBuff, job.AppID); err != nil {
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
		err := fmt.Errorf("buff.sell: resolved 0/%d offers for appid=%d skipped=%v",
			res.Fetched, job.AppID, res.Skipped)
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

	sample, err := r.Quotes.ListByAppIDPlatformSide(ctx, job.AppID, source.PlatformBuff, string(source.SideAsk), 5)
	if err == nil {
		res.Sample = sample
	}
	if partialErr != nil {
		if errors.Is(partialErr, source.ErrPageLimit) {
			telemetry.RecordJobOKWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, res.Fetched, res.QuotesWritten, resources)
			return res, nil
		}
		telemetry.RecordJobFailWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, false, telemetry.ReasonError, partialErr.Error(), resources)
		return res, fmt.Errorf("buff.sell partial after %d quotes: %w", res.QuotesWritten, partialErr)
	}
	telemetry.RecordJobOKWithResources(r.recorder(), srcName, platform, job.Key, job.AppID, res.Fetched, res.QuotesWritten, resources)
	return res, nil
}

// BuffJobsFromConfig collects enabled buff+sell jobs whose appid is enabled
// and source name is allowed by the worker --sources filter.
// onlyAppID 0 = all enabled appids; >0 restricts to that game (worker --appid).
func BuffJobsFromConfig(cfg *config.Config, sourcesAllowlist string) []source.JobSpec {
	return jobsFromConfig(cfg, sourcesAllowlist, source.PlatformBuff, source.SideAsk, 0)
}

// BuffJobsFromConfigAppID is BuffJobsFromConfig with an optional appid filter.
func BuffJobsFromConfigAppID(cfg *config.Config, sourcesAllowlist string, onlyAppID int64) []source.JobSpec {
	return jobsFromConfig(cfg, sourcesAllowlist, source.PlatformBuff, source.SideAsk, onlyAppID)
}

// JobsFromConfig collects enabled steam+sell jobs (kept for P1 callers/tests).
func JobsFromConfig(cfg *config.Config, sourcesAllowlist string) []source.JobSpec {
	return jobsFromConfig(cfg, sourcesAllowlist, source.PlatformSteam, source.SideAsk, 0)
}

// JobsFromConfigAppID is JobsFromConfig with an optional appid filter.
func JobsFromConfigAppID(cfg *config.Config, sourcesAllowlist string, onlyAppID int64) []source.JobSpec {
	return jobsFromConfig(cfg, sourcesAllowlist, source.PlatformSteam, source.SideAsk, onlyAppID)
}

// jobsFromConfig builds JobSpecs for platform+side.
// effective(job) = sources allowlist ∩ job.enabled ∩ game.enabled_appids [∩ onlyAppID].
// Appid is required on every job; disabled games' jobs are dropped (no single-game hardcode).
func jobsFromConfig(cfg *config.Config, sourcesAllowlist, platform string, side source.Side, onlyAppID int64) []source.JobSpec {
	if cfg == nil {
		return nil
	}
	var out []source.JobSpec
	for key, j := range cfg.Jobs {
		if !j.Enabled {
			continue
		}
		if !strings.EqualFold(j.Platform, platform) {
			continue
		}
		if !strings.EqualFold(j.Side, string(side)) {
			continue
		}
		if j.AppID <= 0 {
			continue
		}
		if !cfg.HasAppID(j.AppID) {
			continue
		}
		if onlyAppID > 0 && j.AppID != onlyAppID {
			continue
		}
		spec := source.JobSpec{
			Key:       key,
			Platform:  strings.ToLower(platform),
			Side:      side,
			AppID:     j.AppID,
			Enabled:   true,
			MaxPages:  j.MaxPages,
			Count:     j.Count,
			PageDelay: j.PageDelayDuration(),
		}
		if !source.MatchSources(sourcesAllowlist, spec.SourceName()) {
			continue
		}
		out = append(out, spec)
	}
	// Deterministic order by key for stable logs.
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].Key < out[i].Key {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

// BuffCookieFromEnv returns BUFFGO_BUFF_COOKIE when set (optional live account).
func BuffCookieFromEnv() string {
	return strings.TrimSpace(os.Getenv("BUFFGO_BUFF_COOKIE"))
}

// BuffSellRuntime holds optional process-level deps for config-driven buff.sell runs.
// Redis non-nil enables pool.Manager Acquire/Release so concurrent jobs do not
// double-lease the same proxy/platform. Nil Redis uses static first-candidate
// proxy selection without leasing (same StaticProvider HTTP path as steam.sell).
//
// Injected *source.BuffSellSource still wins (tests/httptest): proxy selection,
// HTTP client, and leasing are only wired when src == nil.
//
// Manager is built inside the pipeline from Redis+cfg when Redis != nil (keeps run.go thin).
// WorkerID is optional; empty → hostname-pid (or worker-<hex> fallback), stable for the Run call.
type BuffSellRuntime struct {
	Redis    *redis.Client
	WorkerID string // optional override for tests / ops
}

// RunBuffSellFromConfig opens PG and runs all matching buff.sell jobs once.
// Cookie is taken from src, then BUFFGO_BUFF_COOKIE env (account-backed path).
// onlyAppID 0 = all enabled appids; >0 runs jobs for that game only (worker --appid).
func RunBuffSellFromConfig(ctx context.Context, cfg *config.Config, sourcesAllowlist string, src *source.BuffSellSource) ([]BuffSellRunResult, error) {
	return RunBuffSellFromConfigAppID(ctx, cfg, sourcesAllowlist, 0, src, BuffSellRuntime{})
}

// RunBuffSellFromConfigAppID is RunBuffSellFromConfig with an optional appid filter
// and optional runtime deps (Redis for pool leases + StaticProvider HTTP proxy).
//
// When src is nil (production worker path):
//   - Cookie: BUFFGO_BUFF_COOKIE (needs_account; skip or error when missing)
//   - Proxy: config [[proxies]] via StaticProvider; empty → proxy_id=direct
//   - When rt.Redis != nil: pool.Manager Acquire per job (platform=buff; line prefer
//     cn/dual/hk enforced by Manager); Release after job; SetCooldown on HTTP 429
//   - When rt.Redis == nil: first ordered candidate without lease (tests without Redis)
//
// When src is non-nil (tests/httptest): used as-is for all jobs; no proxy/lease rewiring.
func RunBuffSellFromConfigAppID(ctx context.Context, cfg *config.Config, sourcesAllowlist string, onlyAppID int64, src *source.BuffSellSource, rt BuffSellRuntime) ([]BuffSellRunResult, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if onlyAppID > 0 && !cfg.HasAppID(onlyAppID) {
		return nil, fmt.Errorf("appid=%d not in enabled_appids=%v", onlyAppID, cfg.Games.EnabledAppIDs)
	}
	jobs := BuffJobsFromConfigAppID(cfg, sourcesAllowlist, onlyAppID)
	if len(jobs) == 0 {
		return nil, nil
	}
	return RunBuffSellJobs(ctx, cfg, jobs, rt, src, sourcesAllowlist)
}

// RunBuffSellJobs runs an explicit job list.
// sourcesAllowlist is only used for needs_account error vs skip when cookie missing.
func RunBuffSellJobs(ctx context.Context, cfg *config.Config, jobs []source.JobSpec, rt BuffSellRuntime, src *source.BuffSellSource, sourcesAllowlist string) ([]BuffSellRunResult, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if len(jobs) == 0 {
		return nil, nil
	}

	injected := src != nil
	cookie := ""
	if injected {
		cookie = src.Cookie()
	}
	if cookie == "" {
		cookie = BuffCookieFromEnv()
	}
	if injected && src.Cookie() == "" && cookie != "" {
		src.SetCookie(cookie)
	}
	// Account-backed: without a session cookie, skip rather than call Buff unauthenticated
	// (empty --sources still runs steam.sell). Explicit cookie via BUFFGO_BUFF_COOKIE or
	// NewBuffSellSource(Cookie: …) enables the live path; httptest injects its own source.
	if cookie == "" {
		explicit := strings.TrimSpace(sourcesAllowlist) != "" && source.MatchSources(sourcesAllowlist, source.NameBuffAsk)
		if explicit {
			return nil, fmt.Errorf("buff.sell: needs_account — set BUFFGO_BUFF_COOKIE (session cookie) or pass a BuffSellSource with Cookie")
		}
		log.Printf("buffgo worker: buff.sell skipped (no account cookie; set BUFFGO_BUFF_COOKIE for live fetch)")
		return nil, nil
	}

	var prov *pool.StaticProvider
	var mgr *pool.Manager
	workerID := resolveWorkerID(rt.WorkerID)
	if !injected {
		prov = staticProviderFromConfig(cfg)
		if prov.IsDirectMode() {
			log.Printf("buffgo worker: buff.sell proxies empty/disabled → direct mode (node_ref=%s)", telemetry.SafeNodeRef(pool.ProxyIDDirect))
		} else {
			log.Printf("buffgo worker: buff.sell static proxies configured=%d (enabled listed via StaticProvider)", prov.LenConfigured())
		}
		if rt.Redis != nil {
			m, err := poolManagerFromConfig(cfg, rt.Redis)
			if err != nil {
				return nil, fmt.Errorf("buff.sell pool manager: %w", err)
			}
			mgr = m
			log.Printf("buffgo worker: buff.sell pool manager enabled worker_ref=%s lease_ttl=%s default_cooldown=%s",
				telemetry.SafeWorkerRef(workerID), mgr.LeaseTTL(), mgr.DefaultCooldown())
		} else {
			log.Printf("buffgo worker: redis nil → buff.sell static first-candidate (no proxy lease)")
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

	results := make([]BuffSellRunResult, 0, len(jobs))
	for _, job := range jobs {
		res, err := runBuffSellJobOnce(ctx, buffSellJobDeps{
			db:       db,
			src:      src,
			injected: injected,
			cookie:   cookie,
			prov:     prov,
			mgr:      mgr,
			workerID: workerID,
			job:      job,
		})
		if err != nil {
			// Soft-skip: no lease, or Buff HTTP 429. Pool cooldown already applied on
			// Release in runBuffSellJobOnce. Once-mode returns nil when only soft errors.
			if isSoftSkipJobErr(err) {
				if source.IsBuffRateLimited(err) {
					log.Printf("buffgo worker: rate limited soft-skip job_ref=%s error_ref=%s",
						telemetry.SafeJobRef(job.Key), telemetry.SafeErrorRef(err))
				} else {
					log.Printf("buffgo worker: buff.sell skip job_ref=%s appid=%d error_ref=%s",
						telemetry.SafeJobRef(job.Key), job.AppID, telemetry.SafeErrorRef(err))
				}
				res.Completeness = CompletenessPartial
				if res.NextStart == 0 {
					res.NextStart = job.Start
				}
				res.Skipped = append(res.Skipped, telemetry.SafeErrorRef(err))
				results = append(results, res)
				continue
			}
			results = append(results, res)
			return results, telemetry.WrapError("buff sell job", err)
		}
		results = append(results, res)
	}
	return results, nil
}

// buffSellJobDeps is the per-job wiring for runBuffSellJobOnce (keeps defer Release safe in loops).
type buffSellJobDeps struct {
	db       *sql.DB
	src      *source.BuffSellSource
	injected bool
	cookie   string
	prov     *pool.StaticProvider
	mgr      *pool.Manager
	workerID string
	job      source.JobSpec
}

// runBuffSellJobOnce selects/leases a proxy (production), fetches, and always releases a held lease.
func runBuffSellJobOnce(ctx context.Context, d buffSellJobDeps) (BuffSellRunResult, error) {
	job := d.job
	jobSrc := d.src
	proxyID := pool.ProxyIDDirect
	var lease *pool.Lease
	// runCtx is cancelled if lease Renew fails (fail-closed); defaults to parent ctx.
	runCtx := ctx
	var stopRenewer func()

	if !d.injected {
		var ep pool.ProxyEndpoint
		if d.mgr != nil {
			// Acquire ordered candidates until one succeeds (platform=buff).
			l, endpoint, err := acquireBuffProxyLease(ctx, d.mgr, d.prov, d.workerID, job.AppID)
			if err != nil {
				return BuffSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}, err
			}
			lease = l
			ep = endpoint
			// Keep exclusive lease alive for the duration of the buff fetch (LeaseTTL default 30s).
			runCtx, stopRenewer = startLeaseRenewer(ctx, d.mgr, lease.ID, d.workerID, leaseRenewInterval(d.mgr.LeaseTTL()))
		} else {
			// No Redis / no Manager: static first candidate without lease (CandidatesFromProvider order).
			var err error
			ep, err = resolveProxyForAppID(ctx, d.prov, job.AppID)
			if err != nil {
				return BuffSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}, fmt.Errorf("proxy select: %w", err)
			}
		}
		proxyID = ep.ProxyID()
		if lease != nil {
			proxyID = lease.Proxy
		}
		client, err := HTTPClientForEndpoint(ep)
		if err != nil {
			if stopRenewer != nil {
				stopRenewer()
			}
			if lease != nil {
				releaseSteamProxyLease(ctx, d.mgr, lease, d.workerID, pool.ReleaseOptions{})
			}
			return BuffSellRunResult{JobKey: job.Key, AppID: job.AppID, Completeness: CompletenessPartial}, fmt.Errorf("http client: %w", err)
		}
		jobSrc = source.NewBuffSellSource(source.BuffSellOptions{
			Cookie:     d.cookie,
			HTTPClient: client,
		})
	}

	// Always stop renewer then Release a held lease; set pool default cooldown after buff HTTP 429.
	var fetchErr error
	if lease != nil {
		defer func() {
			if stopRenewer != nil {
				stopRenewer()
			}
			opts := releaseOptsForBuffFetchErr(fetchErr)
			releaseSteamProxyLease(context.Background(), d.mgr, lease, d.workerID, opts)
		}()
	}

	runner := NewBuffSellRunner(d.db, jobSrc)
	runner.WorkerID = d.workerID
	runner.ProxyID = proxyID
	runner.Cookie = d.cookie
	leaseRef := ""
	if lease != nil {
		runner.LeaseID = lease.ID
		leaseRef = telemetry.SafeLeaseRef(lease.ID)
	}
	log.Printf("buffgo worker: buff.sell fetch appid=%d job_ref=%s needs_account=true cookie=%v node_ref=%s account_ref=%s lease_ref=%s worker_ref=%s",
		job.AppID, telemetry.SafeJobRef(job.Key), runner.Cookie != "", telemetry.SafeNodeRef(proxyID),
		telemetry.SafeAccountRef(runner.Cookie), leaseRef, telemetry.SafeWorkerRef(d.workerID))
	jctx := runCtx
	jcancel := func() {}
	if job.MaxPages > 0 {
		jctx, jcancel = context.WithTimeout(runCtx, 2*time.Minute)
	}
	res, err := runner.RunJob(jctx, job)
	jcancel()
	fetchErr = err
	if err != nil {
		return res, err
	}
	log.Printf("buffgo worker: buff.sell appid=%d node_ref=%s account_ref=%s fetched=%d resolved=%d quotes=%d unresolved=%d",
		res.AppID, telemetry.SafeNodeRef(proxyID), telemetry.SafeAccountRef(runner.Cookie),
		res.Fetched, res.Resolved, res.QuotesWritten, res.Unresolved)
	for _, q := range res.Sample {
		log.Printf("buffgo worker: sample buff quote item_id=%d price_cents=%d currency=CNY",
			q.ItemID, q.PriceCents)
	}
	return res, nil
}

// acquireBuffProxyLease lists ordered candidates and leases the first available (platform=buff).
// Line match is enforced by Manager.Acquire (defaults buff→cn/dual/hk).
// Returns the lease and matching ProxyEndpoint for HTTP client construction.
func acquireBuffProxyLease(
	ctx context.Context,
	mgr *pool.Manager,
	prov pool.ProxyProvider,
	workerID string,
	appid int64,
) (*pool.Lease, pool.ProxyEndpoint, error) {
	if mgr == nil {
		return nil, pool.ProxyEndpoint{}, fmt.Errorf("pool manager is nil")
	}
	cands, err := pool.CandidatesFromProvider(ctx, prov, appid)
	if err != nil {
		return nil, pool.ProxyEndpoint{}, fmt.Errorf("list proxy candidates: %w", err)
	}
	lease, _, err := TryAcquireCandidates(ctx, mgr.Acquire, workerID, source.PlatformBuff, appid, cands)
	if err != nil {
		return nil, pool.ProxyEndpoint{}, err
	}
	ep, err := lookupEndpointByProxyID(ctx, prov, lease.Proxy)
	if err != nil {
		_ = mgr.Release(ctx, lease.ID, workerID, pool.ReleaseOptions{})
		return nil, pool.ProxyEndpoint{}, fmt.Errorf("lookup proxy endpoint: %w", err)
	}
	log.Printf("buffgo worker: buff.sell leased node_ref=%s lease_ref=%s worker_ref=%s appid=%d line=%s",
		telemetry.SafeNodeRef(lease.Proxy), telemetry.SafeLeaseRef(lease.ID), telemetry.SafeWorkerRef(workerID), appid, lease.LineType)
	return lease, ep, nil
}

// releaseOptsForBuffFetchErr sets pool cooldown when fetch failed with Buff HTTP 429.
// Cooldown duration 0 → Manager DefaultCooldown (pool.default_platform_cooldown).
func releaseOptsForBuffFetchErr(err error) pool.ReleaseOptions {
	if err == nil || !source.IsBuffRateLimited(err) {
		return pool.ReleaseOptions{}
	}
	return pool.ReleaseOptions{SetCooldown: true}
}
