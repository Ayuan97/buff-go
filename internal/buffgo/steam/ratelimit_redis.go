package steam

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

const redisKeyPrefix = "buffgo:steam:rl:"

// RedisSearchLimiter shares per-proxy search budgets across workers via Redis.
// Key layout (under buffgo:steam:rl:):
//
//	search:{proxy}:n    → integer count, TTL = window
//	search:{proxy}:cd   → "1", TTL = cooldown_on_429
//	search:{proxy}:last → unix nanoseconds of last successful AllowSearch
type RedisSearchLimiter struct {
	rdb    *redis.Client
	cfg    SearchLimitConfig
	prefix string
}

// RedisSearchLimitOptions configures NewRedisSearchLimiter.
type RedisSearchLimitOptions struct {
	// KeyPrefix overrides default "buffgo:steam:rl:" (tests use unique prefixes).
	KeyPrefix string
}

// NewRedisSearchLimiter builds a Redis-backed SearchLimiter. rdb must be non-nil.
func NewRedisSearchLimiter(rdb *redis.Client, cfg SearchLimitConfig, opt RedisSearchLimitOptions) (*RedisSearchLimiter, error) {
	if rdb == nil {
		return nil, fmt.Errorf("steam.RedisSearchLimiter: redis client is required")
	}
	prefix := opt.KeyPrefix
	if prefix == "" {
		prefix = redisKeyPrefix
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return &RedisSearchLimiter{
		rdb:    rdb,
		cfg:    cfg.Normalize(),
		prefix: prefix,
	}, nil
}

// Config returns the effective config.
func (r *RedisSearchLimiter) Config() SearchLimitConfig {
	if r == nil {
		return SearchLimitConfig{}.Normalize()
	}
	return r.cfg
}

func (r *RedisSearchLimiter) countKey(proxy string) string {
	return r.prefix + EndpointSearch + ":" + proxy + ":n"
}

func (r *RedisSearchLimiter) coolKey(proxy string) string {
	return r.prefix + EndpointSearch + ":" + proxy + ":cd"
}

func (r *RedisSearchLimiter) lastKey(proxy string) string {
	return r.prefix + EndpointSearch + ":" + proxy + ":last"
}

// allowSearchScript atomically enforces cooldown, soft budget, and min_interval (R6).
//
// KEYS[1]=countKey KEYS[2]=coolKey KEYS[3]=lastKey
// ARGV[1]=softMax ARGV[2]=windowSec ARGV[3]=minIntervalNs ARGV[4]=nowNs
//
// Returns a 2-element array {code, waitNs}:
//
//	0,0  — allowed: INCR count (+EXPIRE), SET last=nowNs
//	1,0  — cooling
//	2,0  — budget exhausted
//	3,w  — too soon; wait w nanoseconds then retry (no side effects)
//
// Min-interval + budget + last update run in one Redis thread so concurrent
// workers cannot both pass spacing and INCR in the same wall-time window.
var allowSearchScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[2]) == 1 then
  return {1, 0}
end
local n = tonumber(redis.call("GET", KEYS[1]) or "0")
local soft = tonumber(ARGV[1])
if n >= soft then
  return {2, 0}
end
local minNs = tonumber(ARGV[3]) or 0
local nowNs = tonumber(ARGV[4])
if minNs > 0 then
  local lastRaw = redis.call("GET", KEYS[3])
  if lastRaw then
    local lastNs = tonumber(lastRaw)
    if lastNs and lastNs > 0 then
      local elapsed = nowNs - lastNs
      if elapsed < minNs then
        return {3, minNs - elapsed}
      end
    end
  end
end
n = redis.call("INCR", KEYS[1])
if n == 1 then
  redis.call("EXPIRE", KEYS[1], tonumber(ARGV[2]))
end
redis.call("SET", KEYS[3], ARGV[4])
return {0, 0}
`)

// AllowSearch implements SearchLimiter.
// Uses a single Redis Lua script for budget + cooldown + min_interval reservation
// so multi-worker spacing is atomic (R6). On code=3, sleeps waitNs (ctx-aware) and retries.
func (r *RedisSearchLimiter) AllowSearch(ctx context.Context, proxyID string) error {
	if r == nil || r.rdb == nil {
		return nil
	}
	proxy := NormalizeProxyID(proxyID)

	winSec := int(r.cfg.Window / time.Second)
	if winSec < 1 {
		winSec = 1
	}
	minNs := r.cfg.MinInterval.Nanoseconds()
	if minNs < 0 {
		minNs = 0
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		nowNs := time.Now().UnixNano()
		raw, err := allowSearchScript.Run(ctx, r.rdb, []string{
			r.countKey(proxy),
			r.coolKey(proxy),
			r.lastKey(proxy),
		}, r.cfg.SoftMax, winSec, minNs, nowNs).Slice()
		if err != nil {
			return fmt.Errorf("steam search rate allow: %w", err)
		}
		code, waitNs, err := parseAllowScriptResult(raw)
		if err != nil {
			return err
		}
		switch code {
		case 0:
			return nil
		case 1:
			return ErrSearchCooling
		case 2:
			return ErrSearchBudget
		case 3:
			wait := time.Duration(waitNs)
			if wait < time.Millisecond {
				wait = time.Millisecond
			}
			// Cap single sleep to min_interval (script already computed remainder).
			if r.cfg.MinInterval > 0 && wait > r.cfg.MinInterval {
				wait = r.cfg.MinInterval
			}
			if err := sleepCtx(ctx, wait); err != nil {
				return err
			}
			// Retry: another worker may have advanced last; script re-checks atomically.
			continue
		default:
			return fmt.Errorf("steam search rate allow: unexpected code %d", code)
		}
	}
}

// parseAllowScriptResult unpacks Lua {code, waitNs} (redis may return int64 or string).
func parseAllowScriptResult(raw []interface{}) (code int, waitNs int64, err error) {
	if len(raw) < 2 {
		return 0, 0, fmt.Errorf("steam search rate allow: bad script result len=%d", len(raw))
	}
	code, err = redisInt(raw[0])
	if err != nil {
		return 0, 0, fmt.Errorf("steam search rate allow code: %w", err)
	}
	waitNs64, err := redisInt64(raw[1])
	if err != nil {
		return 0, 0, fmt.Errorf("steam search rate allow wait: %w", err)
	}
	if waitNs64 < 0 {
		waitNs64 = 0
	}
	return code, waitNs64, nil
}

func redisInt(v interface{}) (int, error) {
	n, err := redisInt64(v)
	return int(n), err
}

func redisInt64(v interface{}) (int64, error) {
	switch t := v.(type) {
	case int64:
		return t, nil
	case int:
		return int64(t), nil
	case string:
		return strconv.ParseInt(t, 10, 64)
	case []byte:
		return strconv.ParseInt(string(t), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected type %T", v)
	}
}

// MarkSearch429 implements SearchLimiter.
func (r *RedisSearchLimiter) MarkSearch429(ctx context.Context, proxyID string) error {
	if r == nil || r.rdb == nil {
		return nil
	}
	proxy := NormalizeProxyID(proxyID)
	sec := int(r.cfg.CooldownOn429 / time.Second)
	if sec < 1 {
		sec = 1
	}
	if err := r.rdb.Set(ctx, r.coolKey(proxy), "1", time.Duration(sec)*time.Second).Err(); err != nil {
		return fmt.Errorf("steam search rate cooldown: %w", err)
	}
	return nil
}
