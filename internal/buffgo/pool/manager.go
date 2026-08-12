package pool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"buff-go/internal/telemetry"

	"github.com/go-redis/redis/v8"
)

// Options configures a Redis-backed lease Manager.
//
// Proxy listing is not owned by Manager: inject a ProxyProvider (typically
// StaticProvider from config.StaticProxyInputs) at the worker loop, then:
//
//	cands, _ := CandidatesFromProvider(ctx, provider, appid)
//	for _, c := range cands {
//	  lease, err := mgr.Acquire(ctx, AcquireRequest{
//	    WorkerID: w, Proxy: c.ID, Platform: platform,
//	    LineType: c.LineType, AppID: appid, OnlyAppIDs: c.OnlyAppIDs, ...
//	  })
//	}
//
// Empty static list → provider.List returns proxy_id=direct (no real HTTP proxy).
// Manager itself only needs Redis for locks/cooldown/quota.
type Options struct {
	// LeaseTTL is how long a lease lives without renew (default 30s).
	// Crash recovery: keys expire so dead workers cannot hold resources forever.
	LeaseTTL time.Duration
	// DefaultCooldown is applied on Release when SetCooldown is true and
	// ReleaseOptions.Cooldown is zero (default 2m).
	DefaultCooldown time.Duration
	// KeyPrefix overrides the default "buffgo:pool:" (tests use unique prefixes).
	KeyPrefix string
	// PlatformLines maps platform → allowed proxy line_types (pool.platform_lines).
	// nil uses DefaultPlatformLines (buff→cn/dual/hk, steam→oversea/dual/hk).
	// Explicit entries replace defaults for that platform only.
	PlatformLines map[string][]string
	// MaxProxyLeases maps appid → soft concurrent lease cap (games.quota.*.max_proxy_leases).
	// Missing or <=0 means unlimited for that appid.
	MaxProxyLeases map[int64]int
	// Recorder receives lease acquire/release/conflict and rate-limit events (P5.3).
	// nil = no observability sink (callers may still use telemetry.Default()).
	Recorder telemetry.Recorder
}

// OptionsFromConfig builds Manager Options from config-derived lease/cooldown/lines/quotas.
// ProxyProvider is separate (NewStaticProvider / NewStaticProviderFromInput).
func OptionsFromConfig(leaseTTL, defaultCooldown time.Duration, platformLines map[string][]string, maxProxyLeases map[int64]int) Options {
	return Options{
		LeaseTTL:        leaseTTL,
		DefaultCooldown: defaultCooldown,
		PlatformLines:   platformLines,
		MaxProxyLeases:  maxProxyLeases,
	}
}

// Manager provides Acquire / Renew / Release against Redis.
type Manager struct {
	rdb             *redis.Client
	leaseTTL        time.Duration
	defaultCooldown time.Duration
	prefix          string
	// lineAllow: platform → set of allowed line_types (merged defaults + config).
	lineAllow map[string]map[string]struct{}
	// platformLines keeps the merged prefer lists for inspection / tests.
	platformLines map[string][]string
	// maxProxyLeases: appid → soft concurrent lease cap (0 = unlimited).
	maxProxyLeases map[int64]int
	// rec optional observability sink (lease / 限频).
	rec telemetry.Recorder
}

// NewManager wires a go-redis client. rdb must be non-nil.
func NewManager(rdb *redis.Client, opt Options) (*Manager, error) {
	if rdb == nil {
		return nil, fmt.Errorf("redis client is required")
	}
	ttl := opt.LeaseTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	cd := opt.DefaultCooldown
	if cd <= 0 {
		cd = 2 * time.Minute
	}
	prefix := opt.KeyPrefix
	if prefix == "" {
		prefix = keyPrefix
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	merged := MergePlatformLines(opt.PlatformLines)
	quotas := make(map[int64]int, len(opt.MaxProxyLeases))
	for appid, n := range opt.MaxProxyLeases {
		if appid > 0 && n > 0 {
			quotas[appid] = n
		}
	}
	return &Manager{
		rdb:             rdb,
		leaseTTL:        ttl,
		defaultCooldown: cd,
		prefix:          prefix,
		lineAllow:       buildLineAllowSet(opt.PlatformLines),
		platformLines:   merged,
		maxProxyLeases:  quotas,
		rec:             opt.Recorder,
	}, nil
}

// SetRecorder installs or replaces the observability sink (tests / late wiring).
func (m *Manager) SetRecorder(r telemetry.Recorder) {
	if m == nil {
		return
	}
	m.rec = r
}

// Recorder returns the configured observability sink (may be nil).
func (m *Manager) Recorder() telemetry.Recorder {
	if m == nil {
		return nil
	}
	return m.rec
}

func (m *Manager) obs() telemetry.Recorder {
	if m != nil && m.rec != nil {
		return m.rec
	}
	// Fall back to process-wide default when Manager has no explicit sink.
	return telemetry.Default()
}

func (m *Manager) leaseLabels(platform, workerID, proxy, account, leaseID string, appid int64) telemetry.LeaseLabels {
	return telemetry.LeaseLabels{
		Platform: platform,
		AppID:    appid,
		WorkerID: workerID,
		Proxy:    proxy,
		Account:  account,
		LeaseID:  leaseID,
	}
}

// LeaseTTL returns configured lease TTL.
func (m *Manager) LeaseTTL() time.Duration { return m.leaseTTL }

// DefaultCooldown returns configured platform cooldown default.
func (m *Manager) DefaultCooldown() time.Duration { return m.defaultCooldown }

// PlatformLines returns a copy of the effective platform→prefer line_types map.
func (m *Manager) PlatformLines() map[string][]string {
	out := make(map[string][]string, len(m.platformLines))
	for k, v := range m.platformLines {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// AllowsLine reports whether lineType is in the platform prefer set.
func (m *Manager) AllowsLine(platform, lineType string) bool {
	return m.lineAllowed(platform, lineType)
}

// MaxProxyLeases returns the soft concurrent lease cap for appid (0 = unlimited).
func (m *Manager) MaxProxyLeases(appid int64) int {
	return MaxProxyLeasesFor(appid, m.maxProxyLeases)
}

// MaxProxyLeasesMap returns a copy of configured appid → max concurrent leases.
func (m *Manager) MaxProxyLeasesMap() map[int64]int {
	if len(m.maxProxyLeases) == 0 {
		return nil
	}
	out := make(map[int64]int, len(m.maxProxyLeases))
	for k, v := range m.maxProxyLeases {
		out[k] = v
	}
	return out
}

func (m *Manager) kLease(id string) string {
	return m.prefix + "lease:" + id
}
func (m *Manager) kProxyPlatform(proxy, platform string) string {
	return m.prefix + "lock:proxy_platform:" + normalizeID(proxy) + ":" + normalizeID(platform)
}
func (m *Manager) kAccount(account string) string {
	return m.prefix + "lock:account:" + normalizeID(account)
}
func (m *Manager) kCooldown(proxy, platform string) string {
	return m.prefix + "cooldown:proxy_platform:" + normalizeID(proxy) + ":" + normalizeID(platform)
}
func (m *Manager) kAppidQuota(appid int64) string {
	return m.prefix + "quota:appid:" + fmt.Sprintf("%d", appid)
}

// acquireScript atomically:
//  1. rejects if (proxy, platform) is cooling
//  2. trims expired quota members; rejects if appid at max_proxy_leases
//  3. SET NX proxy+platform lock
//  4. SET NX account lock when account non-empty
//  5. SET lease JSON with TTL; ZADD lease into appid quota ZSET
//
// On failure rolls back any partial locks.
// KEYS[1]=leaseKey KEYS[2]=proxyLock KEYS[3]=cooldownKey KEYS[4]=accountLock
// KEYS[5]=quotaZSet
// ARGV[1]=leaseID ARGV[2]=leaseJSON ARGV[3]=ttlSeconds ARGV[4]=hasAccount (0|1)
// ARGV[5]=nowUnix ARGV[6]=maxProxyLeases (0=unlimited) ARGV[7]=expUnix
var acquireScript = redis.NewScript(`
local leaseKey = KEYS[1]
local proxyLock = KEYS[2]
local cooldownKey = KEYS[3]
local accountLock = KEYS[4]
local quotaKey = KEYS[5]
local leaseID = ARGV[1]
local leaseJSON = ARGV[2]
local ttl = tonumber(ARGV[3])
local hasAccount = tonumber(ARGV[4])
local now = tonumber(ARGV[5])
local maxq = tonumber(ARGV[6])
local exp = tonumber(ARGV[7])

if redis.call("EXISTS", cooldownKey) == 1 then
  return "cooling"
end

redis.call("ZREMRANGEBYSCORE", quotaKey, "-inf", now)
if maxq > 0 and redis.call("ZCARD", quotaKey) >= maxq then
  return "quota"
end

if redis.call("SET", proxyLock, leaseID, "NX", "EX", ttl) == false then
  return "busy_proxy"
end
if hasAccount == 1 then
  if redis.call("SET", accountLock, leaseID, "NX", "EX", ttl) == false then
    redis.call("DEL", proxyLock)
    return "busy_account"
  end
end
redis.call("SET", leaseKey, leaseJSON, "EX", ttl)
redis.call("ZADD", quotaKey, exp, leaseID)
local qttl = ttl * 4
if qttl < 60 then qttl = 60 end
redis.call("EXPIRE", quotaKey, qttl)
return "ok"
`)

// releaseScript deletes lease + locks when owner matches; optionally sets cooldown;
// removes lease from appid quota ZSET.
// KEYS[1]=leaseKey KEYS[2]=proxyLock KEYS[3]=accountLock KEYS[4]=cooldownKey
// KEYS[5]=quotaZSet
// ARGV[1]=workerID ARGV[2]=setCooldown (0|1) ARGV[3]=cooldownSeconds ARGV[4]=hasAccount
// ARGV[5]=leaseID
var releaseScript = redis.NewScript(`
local leaseKey = KEYS[1]
local proxyLock = KEYS[2]
local accountLock = KEYS[3]
local cooldownKey = KEYS[4]
local quotaKey = KEYS[5]
local workerID = ARGV[1]
local setCooldown = tonumber(ARGV[2])
local cooldownSec = tonumber(ARGV[3])
local hasAccount = tonumber(ARGV[4])
local leaseID = ARGV[5]

local raw = redis.call("GET", leaseKey)
if not raw then
  return "missing"
end
local ok, obj = pcall(cjson.decode, raw)
if not ok then
  return "bad_json"
end
if obj["worker_id"] ~= workerID then
  return "not_owner"
end

redis.call("DEL", leaseKey)
if redis.call("GET", proxyLock) == leaseID then
  redis.call("DEL", proxyLock)
end
if hasAccount == 1 and accountLock ~= "" then
  if redis.call("GET", accountLock) == leaseID then
    redis.call("DEL", accountLock)
  end
end
redis.call("ZREM", quotaKey, leaseID)
if setCooldown == 1 and cooldownSec > 0 then
  redis.call("SET", cooldownKey, "1", "EX", cooldownSec)
end
return "ok"
`)

// Acquire writes a lease and exclusive locks. Fails if:
//   - proxy line_type ∉ platform prefer (ErrLineMismatch)
//   - proxy only_appids non-empty and appid not listed (ErrAppIDNotAllowed)
//   - appid concurrent leases >= max_proxy_leases (ErrQuotaExceeded)
//   - (proxy, platform) is cooling (ErrCooling)
//   - (proxy, platform) or account already leased (ErrBusy)
//
// Observability (P5.3): emits lease.acquire / lease.conflict / rate_limit.hit.
func (m *Manager) Acquire(ctx context.Context, req AcquireRequest) (*Lease, error) {
	proxy := strings.TrimSpace(req.Proxy)
	account := strings.TrimSpace(req.Account)
	workerID := strings.TrimSpace(req.WorkerID)
	platformRaw := strings.TrimSpace(req.Platform)
	lab := m.leaseLabels(normalizeID(platformRaw), workerID, proxy, account, "", req.AppID)

	if err := req.Validate(); err != nil {
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeInvalid, lab, err.Error())
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	platform := normalizeID(req.Platform)
	lab.Platform = platform
	lineType := NormalizeLineType(req.LineType)
	// Line match before Redis: mismatch must not rent (P3.2).
	if !m.lineAllowed(platform, lineType) {
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeLineMismatch, lab, ErrLineMismatch.Error())
		return nil, ErrLineMismatch
	}
	// only_appids: empty = shared; non-empty must contain appid (P3.3).
	if !AppIDAllowed(req.AppID, req.OnlyAppIDs) {
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeAppID, lab, ErrAppIDNotAllowed.Error())
		return nil, ErrAppIDNotAllowed
	}

	leaseID, err := newLeaseID()
	if err != nil {
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeError, lab, err.Error())
		return nil, err
	}
	now := time.Now().UTC()
	lease := Lease{
		ID:       leaseID,
		Proxy:    proxy,
		Account:  account,
		Platform: platform,
		LineType: lineType,
		AppID:    req.AppID,
		WorkerID: workerID,
		Exp:      now.Add(m.leaseTTL),
	}
	payload, err := json.Marshal(lease)
	if err != nil {
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeError, lab, err.Error())
		return nil, err
	}

	ttlSec := int64(m.leaseTTL.Seconds())
	if ttlSec < 1 {
		ttlSec = 1
	}
	hasAccount := 0
	accountKey := m.prefix + "lock:account:_none_"
	if account != "" {
		hasAccount = 1
		accountKey = m.kAccount(account)
	}
	maxq := m.MaxProxyLeases(req.AppID)
	nowUnix := now.Unix()
	expUnix := lease.Exp.Unix()

	res, err := acquireScript.Run(ctx, m.rdb, []string{
		m.kLease(leaseID),
		m.kProxyPlatform(proxy, platform),
		m.kCooldown(proxy, platform),
		accountKey,
		m.kAppidQuota(req.AppID),
	}, leaseID, string(payload), ttlSec, hasAccount, nowUnix, maxq, expUnix).Text()
	if err != nil {
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeError, lab, err.Error())
		return nil, fmt.Errorf("acquire redis: %w", err)
	}
	lab.LeaseID = leaseID
	switch res {
	case "ok":
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeOK, lab, "")
		return &lease, nil
	case "cooling":
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeCooling, lab, ErrCooling.Error())
		return nil, ErrCooling
	case "busy_proxy", "busy_account":
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeBusy, lab, res)
		return nil, ErrBusy
	case "quota":
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeQuota, lab, ErrQuotaExceeded.Error())
		return nil, ErrQuotaExceeded
	default:
		telemetry.RecordAcquire(m.obs(), telemetry.PoolOutcomeError, lab, res)
		return nil, fmt.Errorf("acquire unexpected: %s", res)
	}
}

// Renew extends lease TTL and related locks. Only the owning worker may renew.
func (m *Manager) Renew(ctx context.Context, leaseID, workerID string) (*Lease, error) {
	leaseID = strings.TrimSpace(leaseID)
	workerID = strings.TrimSpace(workerID)
	if leaseID == "" || workerID == "" {
		return nil, ErrInvalidRequest
	}
	// Need account flag / proxy from current lease for keys.
	cur, err := m.Get(ctx, leaseID)
	if err != nil {
		return nil, err
	}
	if cur.WorkerID != workerID {
		return nil, ErrNotOwner
	}

	ttlSec := int64(m.leaseTTL.Seconds())
	if ttlSec < 1 {
		ttlSec = 1
	}
	newExp := time.Now().UTC().Add(m.leaseTTL)
	// RFC3339Nano matches encoding/json time for cjson-friendly rewrite via script ARGV
	// but we rewrite exp in Go and SET full JSON for simplicity/correctness.
	cur.Exp = newExp
	payload, err := json.Marshal(cur)
	if err != nil {
		return nil, err
	}

	hasAccount := 0
	accountKey := m.prefix + "lock:account:_none_"
	if cur.Account != "" {
		hasAccount = 1
		accountKey = m.kAccount(cur.Account)
	}
	proxyKey := m.kProxyPlatform(cur.Proxy, cur.Platform)
	leaseKey := m.kLease(leaseID)

	// Atomic: verify owner still holds, rewrite payload, expire locks,
	// refresh quota ZSET score for this lease_id.
	const renewSimple = `
local raw = redis.call("GET", KEYS[1])
if not raw then return "missing" end
local obj = cjson.decode(raw)
if obj["worker_id"] ~= ARGV[1] then return "not_owner" end
redis.call("SET", KEYS[1], ARGV[2], "EX", tonumber(ARGV[3]))
redis.call("EXPIRE", KEYS[2], tonumber(ARGV[3]))
if tonumber(ARGV[4]) == 1 then
  redis.call("EXPIRE", KEYS[3], tonumber(ARGV[3]))
end
redis.call("ZADD", KEYS[4], tonumber(ARGV[5]), ARGV[6])
local qttl = tonumber(ARGV[3]) * 4
if qttl < 60 then qttl = 60 end
redis.call("EXPIRE", KEYS[4], qttl)
return "ok"
`
	quotaKey := m.kAppidQuota(cur.AppID)
	res, err := redis.NewScript(renewSimple).Run(ctx, m.rdb, []string{
		leaseKey, proxyKey, accountKey, quotaKey,
	}, workerID, string(payload), ttlSec, hasAccount, newExp.Unix(), leaseID).Text()
	if err != nil {
		return nil, fmt.Errorf("renew redis: %w", err)
	}
	switch res {
	case "ok":
		return cur, nil
	case "missing":
		return nil, ErrNotFound
	case "not_owner":
		return nil, ErrNotOwner
	default:
		return nil, fmt.Errorf("renew unexpected: %s", res)
	}
}

// Release frees the lease. Optionally sets (proxy, platform) cooldown.
// Cooldown is independent across platforms: Buff cooldown does not block Steam.
// Also removes the lease from the appid soft-quota ZSET.
//
// Observability (P5.3): emits lease.release (reason=ok|cooldown_set) or lease.conflict.
func (m *Manager) Release(ctx context.Context, leaseID, workerID string, opts ReleaseOptions) error {
	leaseID = strings.TrimSpace(leaseID)
	workerID = strings.TrimSpace(workerID)
	lab := m.leaseLabels("", workerID, "", "", leaseID, 0)
	if leaseID == "" || workerID == "" {
		telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeInvalid, false, lab, ErrInvalidRequest.Error())
		return ErrInvalidRequest
	}
	cur, err := m.Get(ctx, leaseID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeNotFound, false, lab, ErrNotFound.Error())
		} else {
			telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeError, false, lab, err.Error())
		}
		return err
	}
	lab = m.leaseLabels(cur.Platform, workerID, cur.Proxy, cur.Account, leaseID, cur.AppID)
	if cur.WorkerID != workerID {
		telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeNotOwner, false, lab, ErrNotOwner.Error())
		return ErrNotOwner
	}

	hasAccount := 0
	accountKey := m.prefix + "lock:account:_none_"
	if cur.Account != "" {
		hasAccount = 1
		accountKey = m.kAccount(cur.Account)
	}
	cdSec := int64(0)
	setCD := 0
	var cdDetail string
	if opts.SetCooldown {
		setCD = 1
		d := opts.Cooldown
		if d <= 0 {
			d = m.defaultCooldown
		}
		cdSec = int64(d.Seconds())
		if cdSec < 1 {
			cdSec = 1
		}
		cdDetail = fmt.Sprintf("cooldown=%s", d)
	}

	res, err := releaseScript.Run(ctx, m.rdb, []string{
		m.kLease(leaseID),
		m.kProxyPlatform(cur.Proxy, cur.Platform),
		accountKey,
		m.kCooldown(cur.Proxy, cur.Platform),
		m.kAppidQuota(cur.AppID),
	}, workerID, setCD, cdSec, hasAccount, leaseID).Text()
	if err != nil {
		telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeError, false, lab, err.Error())
		return fmt.Errorf("release redis: %w", err)
	}
	switch res {
	case "ok":
		telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeOK, opts.SetCooldown, lab, cdDetail)
		return nil
	case "missing":
		telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeNotFound, false, lab, ErrNotFound.Error())
		return ErrNotFound
	case "not_owner":
		telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeNotOwner, false, lab, ErrNotOwner.Error())
		return ErrNotOwner
	default:
		telemetry.RecordRelease(m.obs(), telemetry.PoolOutcomeError, false, lab, res)
		return fmt.Errorf("release unexpected: %s", res)
	}
}

// ActiveLeaseCount returns concurrent leases counted for appid after trimming
// expired quota members (used by tests / ops). Expired lease TTLs free capacity.
func (m *Manager) ActiveLeaseCount(ctx context.Context, appid int64) (int64, error) {
	key := m.kAppidQuota(appid)
	now := time.Now().Unix()
	if err := m.rdb.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", now)).Err(); err != nil {
		return 0, err
	}
	return m.rdb.ZCard(ctx, key).Result()
}

// Get loads a lease by id. Returns ErrNotFound when missing/expired.
func (m *Manager) Get(ctx context.Context, leaseID string) (*Lease, error) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return nil, ErrNotFound
	}
	raw, err := m.rdb.Get(ctx, m.kLease(leaseID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var l Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("decode lease: %w", err)
	}
	return &l, nil
}

// IsCooling reports whether (proxy, platform) is under platform cooldown.
func (m *Manager) IsCooling(ctx context.Context, proxy, platform string) (bool, error) {
	n, err := m.rdb.Exists(ctx, m.kCooldown(proxy, platform)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetCooldown marks (proxy, platform) cooling. Used by Release and tests.
// Other platforms are unaffected.
func (m *Manager) SetCooldown(ctx context.Context, proxy, platform string, d time.Duration) error {
	if d <= 0 {
		d = m.defaultCooldown
	}
	return m.rdb.Set(ctx, m.kCooldown(proxy, platform), "1", d).Err()
}

// ClearCooldown removes (proxy, platform) cooling (tests / ops).
func (m *Manager) ClearCooldown(ctx context.Context, proxy, platform string) error {
	return m.rdb.Del(ctx, m.kCooldown(proxy, platform)).Err()
}

func newLeaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("lease id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
