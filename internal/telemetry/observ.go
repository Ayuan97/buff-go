// Package telemetry provides lightweight structured events and in-process counters
// for lease, platform rate-limit / cooldown, and fetch success/failure (P5.3).
//
// Design:
//   - Recorder is the single sink (Memory for tests, Log for local/ops, Multi for both).
//   - Events carry platform / source / appid so multi-game workers stay labeled.
//   - No external metrics backend required; counters + structured logs satisfy
//     ROADMAP “可从日志/指标看到”.
package telemetry

import (
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is the high-level observability event class.
type Kind string

const (
	// KindLeaseAcquire is emitted after a successful pool.Acquire.
	KindLeaseAcquire Kind = "lease.acquire"
	// KindLeaseRelease is emitted after a successful pool.Release.
	KindLeaseRelease Kind = "lease.release"
	// KindLeaseConflict is a rejected lease due to contention or policy
	// (busy, not_owner, line_mismatch, appid_not_allowed, invalid).
	KindLeaseConflict Kind = "lease.conflict"
	// KindRateLimit is platform cooldown or appid quota hit (限频).
	KindRateLimit Kind = "rate_limit.hit"
	// KindFetchOK is a Source.Fetch/Pull that returned offers.
	KindFetchOK Kind = "fetch.ok"
	// KindFetchFail is a Source.Fetch/Pull failure.
	KindFetchFail Kind = "fetch.fail"
	// KindJobOK is a full pipeline job (fetch→resolve→upsert) success.
	KindJobOK Kind = "job.ok"
	// KindJobFail is a full pipeline job failure.
	KindJobFail Kind = "job.fail"
)

// Reason is a machine-readable detail for filtering / counters.
type Reason string

const (
	ReasonOK              Reason = "ok"
	ReasonBusy            Reason = "busy"
	ReasonCooling         Reason = "cooling"
	ReasonQuota           Reason = "quota"
	ReasonLineMismatch    Reason = "line_mismatch"
	ReasonAppIDNotAllowed Reason = "appid_not_allowed"
	ReasonNotOwner        Reason = "not_owner"
	ReasonNotFound        Reason = "not_found"
	ReasonInvalid         Reason = "invalid"
	ReasonError           Reason = "error"
	ReasonEmpty           Reason = "empty"
	ReasonUnresolved      Reason = "unresolved"
	ReasonCooldownSet     Reason = "cooldown_set"
)

// Event is one structured observability sample. Platform and Source are
// controlled operational labels and must never contain credentials. WorkerID,
// Proxy, Account, JobKey, LeaseID and Detail accept legacy raw values but are
// converted to process-scoped references before a recorder receives the event.
type Event struct {
	Kind     Kind
	Reason   Reason
	Platform string
	Source   string // e.g. steam.sell
	AppID    int64
	WorkerID string
	Proxy    string
	Account  string
	JobKey   string
	LeaseID  string
	// Count is a non-negative payload (offers fetched, quotes written, etc.).
	Count int64
	// Extra free-form note (error text, cooldown duration, etc.).
	Detail string
	Ts     time.Time
}

// Recorder receives events. Implementations must be safe for concurrent use.
type Recorder interface {
	Record(e Event)
}

// Nop discards all events.
type Nop struct{}

func (Nop) Record(Event) {}

// Multi fans out to several recorders (nil entries skipped).
type Multi []Recorder

func (m Multi) Record(e Event) {
	e = sanitizeEvent(e)
	for _, r := range m {
		if r != nil {
			r.Record(e)
		}
	}
}

// ---------------------------------------------------------------------------
// Memory — in-process counters + ring of recent events (tests / local verify)
// ---------------------------------------------------------------------------

// Memory is a thread-safe in-process recorder with counters and event history.
type Memory struct {
	mu      sync.Mutex
	events  []Event
	maxKeep int
	// counts keyed by "kind\0reason\0platform\0source\0appid"
	counts map[string]int64
}

// NewMemory builds a Memory that retains the last maxKeep events (default 256).
func NewMemory(maxKeep int) *Memory {
	if maxKeep <= 0 {
		maxKeep = 256
	}
	return &Memory{
		maxKeep: maxKeep,
		counts:  make(map[string]int64),
	}
}

func (m *Memory) Record(e Event) {
	if m == nil {
		return
	}
	e = sanitizeEvent(e)
	if e.Ts.IsZero() {
		e.Ts = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = make(map[string]int64)
	}
	m.counts[counterKey(e.Kind, e.Reason, e.Platform, e.Source, e.AppID)]++
	// also roll up without labels for fast totals
	m.counts[counterKey(e.Kind, e.Reason, "", "", 0)]++
	m.counts[counterKey(e.Kind, "", "", "", 0)]++
	m.events = append(m.events, e)
	if len(m.events) > m.maxKeep {
		// drop oldest half to amortize
		cut := len(m.events) - m.maxKeep
		m.events = append([]Event(nil), m.events[cut:]...)
	}
}

// Count returns events matching kind+reason (empty reason = all reasons for kind).
// platform/source/appid zero-values match the unlabeled rollup only when all empty;
// pass filters for labeled counts.
func (m *Memory) Count(kind Kind, reason Reason) int64 {
	return m.CountLabeled(kind, reason, "", "", 0)
}

// CountLabeled counts events with exact label match. Empty platform/source and
// appid=0 select the unlabeled rollup counters written on every Record.
func (m *Memory) CountLabeled(kind Kind, reason Reason, platform, source string, appid int64) int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[counterKey(kind, reason, platform, source, appid)]
}

// Events returns a copy of retained events (oldest first).
func (m *Memory) Events() []Event {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Reset clears counters and history (tests).
func (m *Memory) Reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
	m.counts = make(map[string]int64)
}

// Snapshot is a flat, easy-to-assert view of totals.
type Snapshot struct {
	LeaseAcquire  int64
	LeaseRelease  int64
	LeaseConflict int64
	RateLimitHit  int64
	FetchOK       int64
	FetchFail     int64
	JobOK         int64
	JobFail       int64
}

// Snapshot returns rolled-up counters (unlabeled totals).
func (m *Memory) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return Snapshot{
		LeaseAcquire:  m.counts[counterKey(KindLeaseAcquire, "", "", "", 0)],
		LeaseRelease:  m.counts[counterKey(KindLeaseRelease, "", "", "", 0)],
		LeaseConflict: m.counts[counterKey(KindLeaseConflict, "", "", "", 0)],
		RateLimitHit:  m.counts[counterKey(KindRateLimit, "", "", "", 0)],
		FetchOK:       m.counts[counterKey(KindFetchOK, "", "", "", 0)],
		FetchFail:     m.counts[counterKey(KindFetchFail, "", "", "", 0)],
		JobOK:         m.counts[counterKey(KindJobOK, "", "", "", 0)],
		JobFail:       m.counts[counterKey(KindJobFail, "", "", "", 0)],
	}
}

// FetchSuccessRate is ok/(ok+fail); 0 when no samples.
func (s Snapshot) FetchSuccessRate() float64 {
	t := s.FetchOK + s.FetchFail
	if t == 0 {
		return 0
	}
	return float64(s.FetchOK) / float64(t)
}

// JobSuccessRate is ok/(ok+fail); 0 when no samples.
func (s Snapshot) JobSuccessRate() float64 {
	t := s.JobOK + s.JobFail
	if t == 0 {
		return 0
	}
	return float64(s.JobOK) / float64(t)
}

// String is a compact ops line.
func (s Snapshot) String() string {
	return fmt.Sprintf(
		"lease_acquire=%d lease_release=%d lease_conflict=%d rate_limit=%d fetch_ok=%d fetch_fail=%d fetch_success_rate=%.2f job_ok=%d job_fail=%d job_success_rate=%.2f",
		s.LeaseAcquire, s.LeaseRelease, s.LeaseConflict, s.RateLimitHit,
		s.FetchOK, s.FetchFail, s.FetchSuccessRate(),
		s.JobOK, s.JobFail, s.JobSuccessRate(),
	)
}

func counterKey(kind Kind, reason Reason, platform, source string, appid int64) string {
	return string(kind) + "\x00" + string(reason) + "\x00" + platform + "\x00" + source + "\x00" + fmt.Sprintf("%d", appid)
}

// ---------------------------------------------------------------------------
// Log — structured key=value lines to a logger / writer
// ---------------------------------------------------------------------------

// Log writes one structured line per event (local / production logs).
type Log struct {
	// Prefix defaults to "buffgo observ".
	Prefix string
	// Logf defaults to log.Printf. Tests may inject a buffer-backed func.
	Logf func(format string, args ...any)
}

// NewLog returns a Log recorder writing to the standard library logger.
func NewLog() *Log {
	return &Log{Prefix: "buffgo observ", Logf: log.Printf}
}

// NewLogWriter returns a Log that writes lines to w (tests / custom sinks).
func NewLogWriter(w io.Writer) *Log {
	l := log.New(w, "", 0)
	return &Log{
		Prefix: "buffgo observ",
		Logf:   l.Printf,
	}
}

func (l *Log) Record(e Event) {
	if l == nil {
		return
	}
	e = sanitizeEvent(e)
	logf := l.Logf
	if logf == nil {
		logf = log.Printf
	}
	prefix := l.Prefix
	if prefix == "" {
		prefix = "buffgo observ"
	}
	if e.Ts.IsZero() {
		e.Ts = time.Now().UTC()
	}
	// Keep a stable field order for grepping: event reason platform source appid …
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(": event=")
	b.WriteString(quoteField(string(e.Kind)))
	if e.Reason != "" {
		b.WriteString(" reason=")
		b.WriteString(quoteField(string(e.Reason)))
	}
	if e.Platform != "" {
		b.WriteString(" platform=")
		b.WriteString(quoteField(e.Platform))
	}
	if e.Source != "" {
		b.WriteString(" source=")
		b.WriteString(quoteField(e.Source))
	}
	if e.AppID != 0 {
		fmt.Fprintf(&b, " appid=%d", e.AppID)
	}
	if e.WorkerID != "" {
		b.WriteString(" worker_ref=")
		b.WriteString(e.WorkerID)
	}
	if e.Proxy != "" {
		b.WriteString(" node_ref=")
		b.WriteString(e.Proxy)
	}
	if e.Account != "" {
		b.WriteString(" account_ref=")
		b.WriteString(e.Account)
	}
	if e.JobKey != "" {
		b.WriteString(" job_ref=")
		b.WriteString(e.JobKey)
	}
	if e.LeaseID != "" {
		b.WriteString(" lease_ref=")
		b.WriteString(e.LeaseID)
	}
	if e.Count != 0 {
		fmt.Fprintf(&b, " count=%d", e.Count)
	}
	if e.Detail != "" {
		b.WriteString(" detail_ref=")
		b.WriteString(e.Detail)
	}
	b.WriteString(" ts=")
	b.WriteString(e.Ts.UTC().Format(time.RFC3339Nano))
	logf("%s", b.String())
}

func quoteField(s string) string {
	if strings.IndexFunc(s, func(r rune) bool {
		return r <= ' ' || r == '"' || r == '\\' || r == '=' || r == 0x7f
	}) < 0 {
		return s
	}
	return strconv.Quote(s)
}

// ---------------------------------------------------------------------------
// Helpers used by pool / pipeline call sites
// ---------------------------------------------------------------------------

// Emit is a nil-safe Record with Ts defaulted.
func Emit(r Recorder, e Event) {
	if r == nil {
		return
	}
	e = sanitizeEvent(e)
	if e.Ts.IsZero() {
		e.Ts = time.Now().UTC()
	}
	r.Record(e)
}

// Default is the process-wide optional recorder (set by run / tests).
// Zero value is Nop-safe via Get/Set.
type sanitizingRecorder struct {
	target Recorder
}

func (r sanitizingRecorder) Record(e Event) {
	if r.target != nil {
		r.target.Record(sanitizeEvent(e))
	}
}

// Safe wraps a custom recorder so direct Record calls receive sanitized events.
func Safe(r Recorder) Recorder {
	if r == nil {
		return Nop{}
	}
	if _, ok := r.(sanitizingRecorder); ok {
		return r
	}
	return sanitizingRecorder{target: r}
}

var (
	defaultMu sync.RWMutex
	defaultR  Recorder = sanitizingRecorder{target: Nop{}}
)

// SetDefault installs the process-wide recorder (worker wiring).
func SetDefault(r Recorder) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if r == nil {
		defaultR = Safe(nil)
		return
	}
	defaultR = Safe(r)
}

// Default returns the process-wide recorder (never nil).
func Default() Recorder {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	if defaultR == nil {
		return Nop{}
	}
	return defaultR
}

// StdoutMemory is a convenience for local demos: Log to stdout + Memory counters.
func StdoutMemory() (*Memory, Recorder) {
	mem := NewMemory(512)
	return mem, Multi{mem, NewLogWriter(os.Stdout)}
}

// FormatSnapshot returns a sorted multi-line dump of labeled counters (debug).
func (m *Memory) FormatSnapshot() string {
	if m == nil {
		return ""
	}
	snap := m.Snapshot()
	var lines []string
	lines = append(lines, "totals: "+snap.String())
	m.mu.Lock()
	type kv struct {
		k string
		v int64
	}
	var labeled []kv
	for k, v := range m.counts {
		parts := strings.Split(k, "\x00")
		if len(parts) != 5 {
			continue
		}
		// skip pure rollups (no platform/source/appid and empty reason already in totals)
		if parts[2] == "" && parts[3] == "" && parts[4] == "0" {
			continue
		}
		labeled = append(labeled, kv{
			k: fmt.Sprintf("kind=%s reason=%s platform=%s source=%s appid=%s count=%d",
				parts[0], parts[1], parts[2], parts[3], parts[4], v),
			v: v,
		})
	}
	m.mu.Unlock()
	sort.Slice(labeled, func(i, j int) bool { return labeled[i].k < labeled[j].k })
	for _, e := range labeled {
		lines = append(lines, e.k)
	}
	return strings.Join(lines, "\n")
}
