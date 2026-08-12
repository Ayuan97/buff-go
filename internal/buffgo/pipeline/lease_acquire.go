package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/pool"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/steam"
	"buff-go/internal/telemetry"

	"github.com/go-redis/redis/v8"
)

// ErrNoProxyLease means every candidate was skippable-busy/cooling/mismatch or the list was empty.
// Callers should log and soft-skip the job (do not crash the worker process).
var ErrNoProxyLease = errors.New("no proxy lease available")

// isSoftSkipJobErr reports job errors that must not fail Run*FromConfigAppID (or once-mode
// worker exit 1). Soft-skips: no proxy lease; steam search budget/429 cooling; buff HTTP 429.
// Hard errors (parse, PG, invalid config, unexpected network) still fail the pass.
func isSoftSkipJobErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoProxyLease) {
		return true
	}
	if steam.IsSearchRateLimit(err) {
		return true
	}
	if source.IsBuffRateLimited(err) {
		return true
	}
	return false
}

// LeaseAcquireFunc is pool.Manager.Acquire for injection in tests.
type LeaseAcquireFunc func(ctx context.Context, req pool.AcquireRequest) (*pool.Lease, error)

// isSkippableAcquireErr reports errors that mean "try next candidate".
func isSkippableAcquireErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, pool.ErrBusy) ||
		errors.Is(err, pool.ErrCooling) ||
		errors.Is(err, pool.ErrLineMismatch) ||
		errors.Is(err, pool.ErrAppIDNotAllowed) ||
		errors.Is(err, pool.ErrQuotaExceeded)
}

// TryAcquireCandidates walks ordered candidates and acquires the first available lease.
//
// Skippable outcomes (try next): ErrBusy, ErrCooling, ErrLineMismatch, ErrAppIDNotAllowed, ErrQuotaExceeded.
// Empty candidates or all skippable → (nil, -1, ErrNoProxyLease).
// Unexpected acquire errors stop the loop and are returned as-is.
//
// platform is typically source.PlatformSteam ("steam").
func TryAcquireCandidates(
	ctx context.Context,
	acquire LeaseAcquireFunc,
	workerID, platform string,
	appid int64,
	cands []pool.ProxyCandidate,
) (*pool.Lease, int, error) {
	if acquire == nil {
		return nil, -1, fmt.Errorf("acquire func is nil")
	}
	workerID = strings.TrimSpace(workerID)
	platform = strings.TrimSpace(platform)
	if workerID == "" {
		return nil, -1, fmt.Errorf("%w: worker_id required", pool.ErrInvalidRequest)
	}
	if platform == "" {
		return nil, -1, fmt.Errorf("%w: platform required", pool.ErrInvalidRequest)
	}
	if appid <= 0 {
		return nil, -1, fmt.Errorf("%w: appid required", pool.ErrInvalidRequest)
	}
	if len(cands) == 0 {
		return nil, -1, ErrNoProxyLease
	}

	var lastSkip error
	for i, c := range cands {
		req := pool.AcquireRequest{
			WorkerID:   workerID,
			Proxy:      c.ID,
			Platform:   platform,
			LineType:   c.LineType,
			AppID:      appid,
			OnlyAppIDs: c.OnlyAppIDs,
		}
		lease, err := acquire(ctx, req)
		if err == nil && lease != nil {
			return lease, i, nil
		}
		if isSkippableAcquireErr(err) {
			lastSkip = err
			continue
		}
		if err != nil {
			return nil, -1, err
		}
		// nil lease without error — treat as skip
		lastSkip = ErrNoProxyLease
	}
	if lastSkip != nil {
		return nil, -1, fmt.Errorf("%w: last=%v", ErrNoProxyLease, lastSkip)
	}
	return nil, -1, ErrNoProxyLease
}

// lookupEndpointByProxyID finds the full ProxyEndpoint for a leased proxy id.
// Falls back to DirectEndpoint when id is direct or not found.
func lookupEndpointByProxyID(ctx context.Context, prov pool.ProxyProvider, proxyID string) (pool.ProxyEndpoint, error) {
	proxyID = strings.TrimSpace(proxyID)
	if proxyID == "" || strings.EqualFold(proxyID, pool.ProxyIDDirect) || strings.EqualFold(proxyID, steam.ProxyDirect) {
		return pool.DirectEndpoint(), nil
	}
	if prov == nil {
		return pool.DirectEndpoint(), nil
	}
	list, err := prov.List(ctx)
	if err != nil {
		return pool.ProxyEndpoint{}, err
	}
	for _, e := range list {
		if e.ProxyID() == proxyID {
			return e, nil
		}
	}
	// Unknown id — treat as URL-only endpoint without auth (best effort).
	return pool.ProxyEndpoint{
		Endpoint: proxyID,
		LineType: pool.LineDual,
		Enabled:  true,
	}, nil
}

// poolManagerFromConfig builds a pool.Manager from config + Redis.
func poolManagerFromConfig(cfg *config.Config, rdb *redis.Client) (*pool.Manager, error) {
	if rdb == nil {
		return nil, fmt.Errorf("redis client is required")
	}
	mo := config.PoolManagerOptions{
		LeaseTTL:        30 * time.Second,
		DefaultCooldown: 2 * time.Minute,
	}
	if cfg != nil {
		mo = cfg.PoolManagerOptions()
	}
	return pool.NewManager(rdb, pool.OptionsFromConfig(
		mo.LeaseTTL,
		mo.DefaultCooldown,
		mo.PlatformLines,
		mo.MaxProxyLeases,
	))
}

// resolveWorkerID returns rt override, else a stable process-scoped id.
// Format: hostname-pid (e.g. "box-12345"). Falls back to "worker-<hex8>" if hostname fails.
func resolveWorkerID(override string) string {
	if s := strings.TrimSpace(override); s != "" {
		return s
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "worker-" + shortRandomID()
	}
	return fmt.Sprintf("%s-%d", strings.TrimSpace(host), os.Getpid())
}

func shortRandomID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano()%1e8)
	}
	return hex.EncodeToString(b[:])
}

// releaseOptsForFetchErr sets pool cooldown when fetch failed with steam search rate limit / 429 cooling.
func releaseOptsForFetchErr(err error, cooldown time.Duration) pool.ReleaseOptions {
	if err == nil || !steam.IsSearchRateLimit(err) {
		return pool.ReleaseOptions{}
	}
	if cooldown <= 0 {
		cooldown = 3 * time.Minute
	}
	return pool.ReleaseOptions{
		SetCooldown: true,
		Cooldown:    cooldown,
	}
}

// leaseRenewInterval returns how often to Renew: min(LeaseTTL/3, 10s), floored at 1s.
// Keeps exclusive leases alive for multi-page fetches that outlive the default 30s LeaseTTL.
func leaseRenewInterval(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	every := ttl / 3
	if every > 10*time.Second {
		every = 10 * time.Second
	}
	if every < time.Second {
		every = time.Second
	}
	return every
}

// leaseRenewFunc is pool.Manager.Renew (error-only) for injection in tests.
type leaseRenewFunc func(ctx context.Context, leaseID, workerID string) error

// startLeaseRenewer periodically Renews a held lease until stop() is called or parent ctx ends.
//
// every is typically leaseRenewInterval(mgr.LeaseTTL()) — min(LeaseTTL/3, 10s).
// Returns jobCtx: a child of ctx cancelled if Renew fails (fail-closed so the job stops
// rather than continuing under a silent double-lease). Call stop() before Release;
// stop ends the ticker and releases the cancel resource (safe after the job returns).
func startLeaseRenewer(ctx context.Context, mgr *pool.Manager, leaseID, workerID string, every time.Duration) (jobCtx context.Context, stop func()) {
	if mgr == nil {
		return ctx, func() {}
	}
	return startLeaseRenewerFn(ctx, func(c context.Context, id, wid string) error {
		_, err := mgr.Renew(c, id, wid)
		return err
	}, leaseID, workerID, every)
}

// startLeaseRenewerFn is the testable core of startLeaseRenewer.
func startLeaseRenewerFn(ctx context.Context, renew leaseRenewFunc, leaseID, workerID string, every time.Duration) (jobCtx context.Context, stop func()) {
	jobCtx, cancelJob := context.WithCancel(ctx)
	leaseID = strings.TrimSpace(leaseID)
	workerID = strings.TrimSpace(workerID)
	if renew == nil || leaseID == "" || workerID == "" {
		return jobCtx, cancelJob
	}
	if every <= 0 {
		every = 10 * time.Second
	}

	done := make(chan struct{})
	exited := make(chan struct{})
	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(done)
			cancelJob()
		})
		<-exited
	}

	go func() {
		defer close(exited)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			default:
			}
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				default:
				}
				// Independent of jobCtx so a mid-renew cancel race does not mask ownership errors.
				rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := renew(rctx, leaseID, workerID)
				rcancel()
				if err != nil {
					log.Printf("buffgo worker: lease renew failed lease_ref=%s worker_ref=%s error_ref=%s (cancelling job)",
						telemetry.SafeLeaseRef(leaseID), telemetry.SafeWorkerRef(workerID), telemetry.SafeErrorRef(err))
					cancelJob()
					return
				}
			}
		}
	}()
	return jobCtx, stop
}

// acquireSteamProxyLease lists ordered candidates and leases the first available (platform=steam).
// Returns the lease and matching ProxyEndpoint for HTTP client construction.
func acquireSteamProxyLease(
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
	lease, _, err := TryAcquireCandidates(ctx, mgr.Acquire, workerID, source.PlatformSteam, appid, cands)
	if err != nil {
		return nil, pool.ProxyEndpoint{}, err
	}
	ep, err := lookupEndpointByProxyID(ctx, prov, lease.Proxy)
	if err != nil {
		// Best-effort release so we do not leak the lease when endpoint lookup fails.
		_ = mgr.Release(ctx, lease.ID, workerID, pool.ReleaseOptions{})
		return nil, pool.ProxyEndpoint{}, fmt.Errorf("lookup proxy endpoint: %w", err)
	}
	log.Printf("buffgo worker: leased node_ref=%s lease_ref=%s worker_ref=%s appid=%d line=%s",
		telemetry.SafeNodeRef(lease.Proxy), telemetry.SafeLeaseRef(lease.ID), telemetry.SafeWorkerRef(workerID), appid, lease.LineType)
	return lease, ep, nil
}

// leaseReleaseAPI is the pool.Manager surface used by releaseSteamProxyLease (testable).
type leaseReleaseAPI interface {
	Release(ctx context.Context, leaseID, workerID string, opts pool.ReleaseOptions) error
	SetCooldown(ctx context.Context, proxy, platform string, d time.Duration) error
	DefaultCooldown() time.Duration
}

// releaseSteamProxyLease frees a lease; logs cooldown when set.
//
// R2: when rate-limit cooldown is requested and Release fails (e.g. lease already
// expired → ErrNotFound), still write the (proxy, platform) cooldown key via
// SetCooldown so the pool layer does not re-rent the same IP immediately.
func releaseSteamProxyLease(ctx context.Context, mgr *pool.Manager, lease *pool.Lease, workerID string, opts pool.ReleaseOptions) {
	if mgr == nil || lease == nil {
		return
	}
	releaseProxyLease(ctx, mgr, lease, workerID, opts)
}

// releaseProxyLease is the testable implementation of releaseSteamProxyLease.
func releaseProxyLease(ctx context.Context, mgr leaseReleaseAPI, lease *pool.Lease, workerID string, opts pool.ReleaseOptions) {
	if mgr == nil || lease == nil {
		return
	}
	proxy := lease.Proxy
	platform := lease.Platform
	err := mgr.Release(ctx, lease.ID, workerID, opts)
	if err != nil {
		log.Printf("buffgo worker: release node_ref=%s platform=%s lease_ref=%s cooldown=%v error_ref=%s",
			telemetry.SafeNodeRef(proxy), platform, telemetry.SafeLeaseRef(lease.ID), opts.SetCooldown, telemetry.SafeErrorRef(err))
		// Cooldown was requested but Release could not apply it (missing/expired lease, etc.).
		if opts.SetCooldown {
			cd := opts.Cooldown
			if cd <= 0 {
				cd = mgr.DefaultCooldown()
			}
			if cdErr := mgr.SetCooldown(ctx, proxy, platform, cd); cdErr != nil {
				log.Printf("buffgo worker: cooldown fallback node_ref=%s platform=%s duration=%s error_ref=%s",
					telemetry.SafeNodeRef(proxy), platform, cd, telemetry.SafeErrorRef(cdErr))
			} else {
				log.Printf("buffgo worker: cooldown fallback node_ref=%s platform=%s duration=%s (release failed)",
					telemetry.SafeNodeRef(proxy), platform, cd)
			}
		}
		return
	}
	if opts.SetCooldown {
		cd := opts.Cooldown
		if cd <= 0 {
			cd = mgr.DefaultCooldown()
		}
		log.Printf("buffgo worker: release node_ref=%s platform=%s lease_ref=%s cooldown=%v duration=%s",
			telemetry.SafeNodeRef(proxy), platform, telemetry.SafeLeaseRef(lease.ID), true, cd)
		return
	}
	log.Printf("buffgo worker: release node_ref=%s platform=%s lease_ref=%s cooldown=false",
		telemetry.SafeNodeRef(proxy), platform, telemetry.SafeLeaseRef(lease.ID))
}
