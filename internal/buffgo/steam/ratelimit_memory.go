package steam

import (
	"context"
	"sync"
	"time"
)

// MemorySearchLimiter is a process-local SearchLimiter.
// Safe for concurrent use. Prefer RedisSearchLimiter when multiple workers
// share the same egress proxy so budgets are global per IP.
type MemorySearchLimiter struct {
	cfg SearchLimitConfig
	mu  sync.Mutex
	// per NormalizeProxyID
	states map[string]*memProxyState
	// now is injectable for tests; nil → time.Now
	now func() time.Time
}

type memProxyState struct {
	// windowStart is when the current count window began.
	windowStart time.Time
	// count is reserved/successful search slots in the current window.
	count int
	// coolUntil is zero when not cooling; otherwise search blocked until this time.
	coolUntil time.Time
	// lastAllowedAt is wall time of the last successful AllowSearch for this proxy.
	lastAllowedAt time.Time
}

// NewMemorySearchLimiter builds an in-process limiter with cfg defaults applied.
func NewMemorySearchLimiter(cfg SearchLimitConfig) *MemorySearchLimiter {
	return &MemorySearchLimiter{
		cfg:    cfg.Normalize(),
		states: make(map[string]*memProxyState),
		now:    time.Now,
	}
}

// Config returns a copy of the effective config.
func (m *MemorySearchLimiter) Config() SearchLimitConfig {
	if m == nil {
		return SearchLimitConfig{}.Normalize()
	}
	return m.cfg
}

// AllowSearch implements SearchLimiter.
// Blocks (sleeps) until MinInterval has elapsed since the last successful allow
// for this proxy; does not return a hard error for that wait. Mutex is not held
// during sleep. After wait, re-checks cooldown and budget before allowing.
func (m *MemorySearchLimiter) AllowSearch(ctx context.Context, proxyID string) error {
	if m == nil {
		return nil
	}
	key := NormalizeProxyID(proxyID)

	for {
		m.mu.Lock()
		now := m.now()

		st := m.states[key]
		if st == nil {
			st = &memProxyState{}
			m.states[key] = st
		}

		// Cooldown after 429 takes precedence (hard deny, no sleep).
		if !st.coolUntil.IsZero() && now.Before(st.coolUntil) {
			m.mu.Unlock()
			return ErrSearchCooling
		}
		if !st.coolUntil.IsZero() && !now.Before(st.coolUntil) {
			st.coolUntil = time.Time{}
		}

		// Min-interval pacing uses wall clock (not injectable now) so sleep + re-check
		// cannot spin forever under test clocks, and production pacing is real-time.
		if m.cfg.MinInterval > 0 && !st.lastAllowedAt.IsZero() {
			elapsed := time.Since(st.lastAllowedAt)
			if elapsed < m.cfg.MinInterval {
				wait := m.cfg.MinInterval - elapsed
				m.mu.Unlock()
				if err := sleepCtx(ctx, wait); err != nil {
					return err
				}
				continue
			}
		}

		// Roll window if expired or never started.
		if st.windowStart.IsZero() || now.Sub(st.windowStart) >= m.cfg.Window {
			st.windowStart = now
			st.count = 0
		}

		// Soft budget (working point ≤ hard wall).
		if st.count >= m.cfg.SoftMax {
			m.mu.Unlock()
			return ErrSearchBudget
		}
		if st.count >= m.cfg.HardMax {
			m.mu.Unlock()
			return ErrSearchBudget
		}

		st.count++
		st.lastAllowedAt = time.Now()
		m.mu.Unlock()
		return nil
	}
}

// MarkSearch429 implements SearchLimiter.
func (m *MemorySearchLimiter) MarkSearch429(_ context.Context, proxyID string) error {
	if m == nil {
		return nil
	}
	key := NormalizeProxyID(proxyID)
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	st := m.states[key]
	if st == nil {
		st = &memProxyState{}
		m.states[key] = st
	}
	st.coolUntil = now.Add(m.cfg.CooldownOn429)
	return nil
}

// CountFor returns the current window count for proxyID (tests / diagnostics).
func (m *MemorySearchLimiter) CountFor(proxyID string) int {
	if m == nil {
		return 0
	}
	key := NormalizeProxyID(proxyID)
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[key]
	if st == nil {
		return 0
	}
	if st.windowStart.IsZero() || now.Sub(st.windowStart) >= m.cfg.Window {
		return 0
	}
	return st.count
}

// CoolUntil returns when cooldown ends for proxyID (zero if not cooling).
func (m *MemorySearchLimiter) CoolUntil(proxyID string) time.Time {
	if m == nil {
		return time.Time{}
	}
	key := NormalizeProxyID(proxyID)
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[key]
	if st == nil || st.coolUntil.IsZero() || !now.Before(st.coolUntil) {
		return time.Time{}
	}
	return st.coolUntil
}
