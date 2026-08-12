package pool

import (
	"fmt"
	"strings"
)

const keyPrefix = "buffgo:pool:"

// Redis key layout (all under buffgo:pool:):
//
//	lease:{lease_id}                         → JSON Lease, TTL = lease_ttl
//	lock:proxy_platform:{proxy}:{platform}   → lease_id, TTL = lease_ttl
//	lock:account:{account}                   → lease_id, TTL = lease_ttl (optional)
//	cooldown:proxy_platform:{proxy}:{platform} → "1", TTL = platform cooldown
//	quota:appid:{appid}                      → ZSET lease_id → exp unix (soft max_proxy_leases)
//
// Locks are per (proxy, platform) so Buff cooldown does not block Steam on the same IP.
// Account locks are global (one worker holds a given account at a time).
// Quota ZSET scores are lease expiry; Acquire trims expired members before ZCARD.

func leaseKey(leaseID string) string {
	return keyPrefix + "lease:" + leaseID
}

func proxyPlatformLockKey(proxy, platform string) string {
	return keyPrefix + "lock:proxy_platform:" + normalizeID(proxy) + ":" + normalizeID(platform)
}

func accountLockKey(account string) string {
	return keyPrefix + "lock:account:" + normalizeID(account)
}

func proxyPlatformCooldownKey(proxy, platform string) string {
	return keyPrefix + "cooldown:proxy_platform:" + normalizeID(proxy) + ":" + normalizeID(platform)
}

func appidQuotaKey(appid int64) string {
	return keyPrefix + "quota:appid:" + fmt.Sprintf("%d", appid)
}

func normalizeID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Key helpers exported for tests / ops inspection.

// LeaseKey returns the Redis key for a lease document.
func LeaseKey(leaseID string) string { return leaseKey(leaseID) }

// ProxyPlatformLockKey returns the exclusive lock key for (proxy, platform).
func ProxyPlatformLockKey(proxy, platform string) string {
	return proxyPlatformLockKey(proxy, platform)
}

// AccountLockKey returns the exclusive lock key for an account.
func AccountLockKey(account string) string { return accountLockKey(account) }

// ProxyPlatformCooldownKey returns the cooldown key for (proxy, platform).
func ProxyPlatformCooldownKey(proxy, platform string) string {
	return proxyPlatformCooldownKey(proxy, platform)
}

// AppIDQuotaKey returns the ZSET key tracking active leases for an appid (soft quota).
func AppIDQuotaKey(appid int64) string { return appidQuotaKey(appid) }

// FormatLeaseID builds a stable-looking lease id (caller may use any unique string).
func FormatLeaseID(workerID, proxy, platform string, appid int64, n int64) string {
	return fmt.Sprintf("%s-%s-%s-%d-%d",
		normalizeID(workerID), normalizeID(proxy), normalizeID(platform), appid, n)
}
