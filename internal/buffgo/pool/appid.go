package pool

// ProxyCandidate is a proxy row used when selecting candidates for Acquire.
// only_appids / prefer_appids match the proxies table (ARCHITECTURE §3.6 / §4.2).
type ProxyCandidate struct {
	// ID is the proxy identity passed to AcquireRequest.Proxy (endpoint or id).
	ID string
	// LineType is cn / hk / oversea / dual.
	LineType string
	// OnlyAppIDs, when non-empty, restricts this proxy to those games (hard split).
	// Empty means shared pool.
	OnlyAppIDs []int64
	// PreferAppIDs soft-affinity: shared proxies that prefer this game rank
	// after dedicated and before plain shared (optional).
	PreferAppIDs []int64
}

// AppIDAllowed reports whether a proxy may serve appid given only_appids.
// Empty only_appids → shared pool (allowed for any appid).
// Non-empty → appid must be listed.
func AppIDAllowed(appid int64, onlyAppIDs []int64) bool {
	if appid <= 0 {
		return false
	}
	if len(onlyAppIDs) == 0 {
		return true
	}
	for _, id := range onlyAppIDs {
		if id == appid {
			return true
		}
	}
	return false
}

// ContainsAppID reports membership in an appid list.
func ContainsAppID(list []int64, appid int64) bool {
	for _, id := range list {
		if id == appid {
			return true
		}
	}
	return false
}

// OrderProxiesForAppID filters and orders candidates for a game:
//
//  1. drop proxies whose only_appids is non-empty and does not contain appid
//  2. dedicated (only_appids contains appid) first
//  3. then shared with prefer_appids containing appid
//  4. then remaining shared (only_appids empty)
//
// Relative order within each bucket is stable (input order).
// Callers then loop Acquire; dedicated pool is tried first (ARCHITECTURE §4.5).
func OrderProxiesForAppID(appid int64, proxies []ProxyCandidate) []ProxyCandidate {
	if len(proxies) == 0 {
		return nil
	}
	var dedicated, preferred, shared []ProxyCandidate
	for _, p := range proxies {
		if !AppIDAllowed(appid, p.OnlyAppIDs) {
			continue
		}
		if len(p.OnlyAppIDs) > 0 {
			// dedicated: only_appids non-empty and contains appid
			dedicated = append(dedicated, p)
			continue
		}
		if ContainsAppID(p.PreferAppIDs, appid) {
			preferred = append(preferred, p)
			continue
		}
		shared = append(shared, p)
	}
	out := make([]ProxyCandidate, 0, len(dedicated)+len(preferred)+len(shared))
	out = append(out, dedicated...)
	out = append(out, preferred...)
	out = append(out, shared...)
	return out
}

// MaxProxyLeasesFor returns the soft quota for appid from a map.
// Missing key or value <= 0 means unlimited (no soft cap).
func MaxProxyLeasesFor(appid int64, quotas map[int64]int) int {
	if quotas == nil {
		return 0
	}
	n := quotas[appid]
	if n < 0 {
		return 0
	}
	return n
}
