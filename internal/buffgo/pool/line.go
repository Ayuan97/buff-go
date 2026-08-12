package pool

import "strings"

// Proxy line_type values (ARCHITECTURE §4.3).
const (
	LineCN      = "cn"
	LineHK      = "hk"
	LineOversea = "oversea"
	LineDual    = "dual"
)

// KnownLineTypes is the closed set of valid line_type values.
var KnownLineTypes = []string{LineCN, LineHK, LineOversea, LineDual}

// DefaultPlatformLines is the product default prefer map when pool.platform_lines
// is unset or omits a platform:
//
//	buff  → cn, dual, hk
//	steam → oversea, dual, hk
var DefaultPlatformLines = map[string][]string{
	"buff":  {LineCN, LineDual, LineHK},
	"steam": {LineOversea, LineDual, LineHK},
}

// NormalizeLineType lowercases and trims a line_type token.
func NormalizeLineType(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidLineType reports whether s is one of cn/hk/oversea/dual.
func ValidLineType(s string) bool {
	switch NormalizeLineType(s) {
	case LineCN, LineHK, LineOversea, LineDual:
		return true
	default:
		return false
	}
}

// PreferLinesFor returns the allowed line_types for platform.
// Uses prefer when it has a non-empty entry for the platform; otherwise
// DefaultPlatformLines; unknown platforms with no config get nil (no match).
func PreferLinesFor(platform string, prefer map[string][]string) []string {
	p := normalizeID(platform)
	if p == "" {
		return nil
	}
	if prefer != nil {
		if lines, ok := prefer[p]; ok && len(lines) > 0 {
			return normalizeLineList(lines)
		}
		// allow non-normalized keys from raw config maps
		for k, lines := range prefer {
			if normalizeID(k) == p && len(lines) > 0 {
				return normalizeLineList(lines)
			}
		}
	}
	if lines, ok := DefaultPlatformLines[p]; ok {
		return append([]string(nil), lines...)
	}
	return nil
}

// LineAllowed reports whether proxy lineType may be leased for platform
// under the given prefer map (nil prefer → defaults).
func LineAllowed(platform, lineType string, prefer map[string][]string) bool {
	line := NormalizeLineType(lineType)
	if line == "" || !ValidLineType(line) {
		return false
	}
	allowed := PreferLinesFor(platform, prefer)
	for _, a := range allowed {
		if a == line {
			return true
		}
	}
	return false
}

// NormalizePlatformLines copies and normalizes a platform→lines map
// (keys and line values lowercased; empty platforms/lines dropped).
func NormalizePlatformLines(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, lines := range in {
		pk := normalizeID(k)
		if pk == "" {
			continue
		}
		norm := normalizeLineList(lines)
		if len(norm) == 0 {
			continue
		}
		out[pk] = norm
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergePlatformLines returns defaults overlaid by overrides (per-platform replace).
func MergePlatformLines(overrides map[string][]string) map[string][]string {
	out := make(map[string][]string, len(DefaultPlatformLines)+len(overrides))
	for k, v := range DefaultPlatformLines {
		out[k] = append([]string(nil), v...)
	}
	for k, v := range NormalizePlatformLines(overrides) {
		out[k] = v
	}
	return out
}

func normalizeLineList(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(lines))
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		n := NormalizeLineType(l)
		if n == "" || !ValidLineType(n) {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// buildLineAllowSet converts platform→[]line into platform→set for O(1) checks.
func buildLineAllowSet(prefer map[string][]string) map[string]map[string]struct{} {
	merged := MergePlatformLines(prefer)
	out := make(map[string]map[string]struct{}, len(merged))
	for platform, lines := range merged {
		set := make(map[string]struct{}, len(lines))
		for _, l := range lines {
			set[l] = struct{}{}
		}
		out[platform] = set
	}
	return out
}

func (m *Manager) lineAllowed(platform, lineType string) bool {
	line := NormalizeLineType(lineType)
	if line == "" {
		return false
	}
	p := normalizeID(platform)
	set, ok := m.lineAllow[p]
	if !ok {
		// platform not in defaults and not configured → reject
		return false
	}
	_, ok = set[line]
	return ok
}
