package telemetry

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type captureRecorder struct {
	events []Event
}

func (r *captureRecorder) Record(e Event) {
	r.events = append(r.events, e)
}

func TestMemory_LeaseRateLimitAndSuccessRate(t *testing.T) {
	m := NewMemory(64)

	// lease acquire + release
	RecordAcquire(m, PoolOutcomeOK, LeaseLabels{
		Platform: "buff", AppID: 252490, WorkerID: "w1", Proxy: "p1", LeaseID: "L1",
	}, "")
	RecordRelease(m, PoolOutcomeOK, true, LeaseLabels{
		Platform: "buff", AppID: 252490, WorkerID: "w1", Proxy: "p1", LeaseID: "L1",
	}, "cooldown=2m")

	// conflict + cooling (限频)
	RecordAcquire(m, PoolOutcomeBusy, LeaseLabels{
		Platform: "buff", AppID: 252490, WorkerID: "w2", Proxy: "p1",
	}, "busy_proxy")
	RecordAcquire(m, PoolOutcomeCooling, LeaseLabels{
		Platform: "buff", AppID: 252490, WorkerID: "w2", Proxy: "p1",
	}, "")
	RecordAcquire(m, PoolOutcomeQuota, LeaseLabels{
		Platform: "steam", AppID: 252490, WorkerID: "w3", Proxy: "p2",
	}, "max_proxy_leases")

	// steam same proxy after buff cool — acquire ok
	RecordAcquire(m, PoolOutcomeOK, LeaseLabels{
		Platform: "steam", AppID: 252490, WorkerID: "w2", Proxy: "p1", LeaseID: "L2",
	}, "")

	// fetch success / fail → success rate
	RecordJobOK(m, "steam.sell", "steam", "steam_sell_rust", 252490, 10, 10)
	RecordJobFail(m, "buff.sell", "buff", "buff_sell_rust", 252490, true, ReasonError, "http 429")
	RecordJobOK(m, "buff.sell", "buff", "buff_sell_rust", 252490, 8, 7)

	snap := m.Snapshot()
	if snap.LeaseAcquire != 2 {
		t.Fatalf("lease_acquire=%d want 2", snap.LeaseAcquire)
	}
	if snap.LeaseRelease != 1 {
		t.Fatalf("lease_release=%d want 1", snap.LeaseRelease)
	}
	if snap.LeaseConflict != 1 {
		t.Fatalf("lease_conflict=%d want 1 (busy)", snap.LeaseConflict)
	}
	if snap.RateLimitHit != 2 {
		t.Fatalf("rate_limit=%d want 2 (cooling+quota)", snap.RateLimitHit)
	}
	if snap.FetchOK != 2 || snap.FetchFail != 1 {
		t.Fatalf("fetch ok=%d fail=%d", snap.FetchOK, snap.FetchFail)
	}
	if snap.JobOK != 2 || snap.JobFail != 1 {
		t.Fatalf("job ok=%d fail=%d", snap.JobOK, snap.JobFail)
	}
	// 2/3 ≈ 0.666…
	rate := snap.FetchSuccessRate()
	if rate < 0.66 || rate > 0.67 {
		t.Fatalf("fetch success rate=%v want ~0.666", rate)
	}
	if m.Count(KindRateLimit, ReasonCooling) != 1 {
		t.Fatalf("cooling count=%d", m.Count(KindRateLimit, ReasonCooling))
	}
	if m.CountLabeled(KindLeaseAcquire, ReasonOK, "buff", "", 252490) != 1 {
		t.Fatalf("labeled buff acquire missing")
	}
	if m.CountLabeled(KindRateLimit, ReasonQuota, "steam", "", 252490) != 1 {
		t.Fatalf("labeled steam quota missing")
	}

	// events retained and ordered
	ev := m.Events()
	if len(ev) < 8 {
		t.Fatalf("events=%d want >=8", len(ev))
	}
	// first should be lease.acquire
	if ev[0].Kind != KindLeaseAcquire || ev[0].Platform != "buff" {
		t.Fatalf("first event: %+v", ev[0])
	}
}

func TestClassifyAcquireRelease(t *testing.T) {
	cases := []struct {
		out  string
		kind Kind
		rsn  Reason
	}{
		{PoolOutcomeOK, KindLeaseAcquire, ReasonOK},
		{PoolOutcomeCooling, KindRateLimit, ReasonCooling},
		{PoolOutcomeQuota, KindRateLimit, ReasonQuota},
		{PoolOutcomeBusy, KindLeaseConflict, ReasonBusy},
		{PoolOutcomeLineMismatch, KindLeaseConflict, ReasonLineMismatch},
		{PoolOutcomeAppID, KindLeaseConflict, ReasonAppIDNotAllowed},
	}
	for _, tc := range cases {
		k, r := ClassifyAcquire(tc.out)
		if k != tc.kind || r != tc.rsn {
			t.Fatalf("acquire %s: got %s/%s want %s/%s", tc.out, k, r, tc.kind, tc.rsn)
		}
	}
	k, r := ClassifyRelease(PoolOutcomeOK, true)
	if k != KindLeaseRelease || r != ReasonCooldownSet {
		t.Fatalf("release cooldown: %s/%s", k, r)
	}
	if !IsRateLimitReason(ReasonCooling) || !IsRateLimitReason(ReasonQuota) {
		t.Fatal("IsRateLimitReason")
	}
	if IsRateLimitReason(ReasonBusy) {
		t.Fatal("busy is conflict not rate limit")
	}
}

func TestLog_StructuredLine(t *testing.T) {
	var buf bytes.Buffer
	lg := NewLogWriter(&buf)
	Emit(lg, Event{
		Kind:     KindRateLimit,
		Reason:   ReasonCooling,
		Platform: "buff",
		AppID:    252490,
		WorkerID: "w1",
		Proxy:    "1.2.3.4:8080",
		Detail:   "proxy platform is cooling",
	})
	line := buf.String()
	for _, want := range []string{
		"buffgo observ:",
		"event=rate_limit.hit",
		"reason=cooling",
		"platform=buff",
		"appid=252490",
		"worker_ref=worker_",
		"node_ref=node_",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log missing %q in %q", want, line)
		}
	}
	if strings.Contains(line, "1.2.3.4:8080") || strings.Contains(line, "proxy platform is cooling") {
		t.Fatalf("log leaked sensitive input: %q", line)
	}
}

func TestRecorders_RedactSensitiveFieldsAndPreserveCorrelation(t *testing.T) {
	const (
		proxyA  = "http://proxy-user:proxy-pass@10.0.0.1:8080"
		proxyB  = "http://proxy-user:other-pass@10.0.0.2:8080"
		account = "sessionid=secret-session; steamLoginSecure=secret-login"
		leaseID = "lease-secret-id"
		detail  = "GET https://example.test Cookie: " + account + " proxy=" + proxyA
	)

	mem := NewMemory(8)
	Emit(mem, Event{
		Kind: KindJobFail, Reason: ReasonError, Platform: "steam", Source: "steam.ask",
		WorkerID: "host-name-123", JobKey: "steam_ask_730", Proxy: proxyA, Account: account, LeaseID: leaseID, Detail: detail,
	})
	// Direct Record calls must cross the same boundary as Emit.
	mem.Record(Event{Kind: KindRateLimit, Reason: ReasonCooling, WorkerID: "host-name-123", Proxy: proxyA, Account: account, LeaseID: leaseID, Detail: detail})
	mem.Record(Event{Kind: KindRateLimit, Reason: ReasonCooling, Proxy: proxyB, Account: account})

	events := mem.Events()
	if len(events) != 3 {
		t.Fatalf("events=%d want 3", len(events))
	}
	for _, event := range events {
		serialized := event.Proxy + " " + event.Account + " " + event.LeaseID + " " + event.Detail
		for _, secret := range []string{proxyA, proxyB, account, leaseID, detail, "proxy-pass", "secret-session", "secret-login"} {
			if strings.Contains(serialized, secret) {
				t.Fatalf("memory event leaked %q in %+v", secret, event)
			}
		}
	}
	if !strings.HasPrefix(events[0].WorkerID, "worker_v1_") || !strings.HasPrefix(events[0].JobKey, "job_v1_") ||
		!strings.HasPrefix(events[0].Proxy, "node_v1_") || !strings.HasPrefix(events[0].Account, "account_v1_") ||
		!strings.HasPrefix(events[0].LeaseID, "lease_") || !strings.HasPrefix(events[0].Detail, "detail_") {
		t.Fatalf("unexpected safe references: %+v", events[0])
	}
	if events[0].Proxy != events[1].Proxy || events[0].Account != events[1].Account || events[0].LeaseID != events[1].LeaseID || events[0].Detail != events[1].Detail {
		t.Fatalf("same resource did not preserve correlation: first=%+v second=%+v", events[0], events[1])
	}
	if events[0].Proxy == events[2].Proxy {
		t.Fatalf("different nodes share reference %q", events[0].Proxy)
	}
	if events[0].Platform != "steam" || events[0].Source != "steam.ask" {
		t.Fatalf("safe operational labels changed: %+v", events[0])
	}
}

func TestRecorders_ResanitizeModifiedRecordedEvent(t *testing.T) {
	const secret = "sessionid=modified-event-secret"
	mem := NewMemory(1)
	mem.Record(Event{Kind: KindJobFail, Account: "original"})
	event := mem.Events()[0]
	event.Account = secret
	event.Detail = secret

	var buf bytes.Buffer
	NewLogWriter(&buf).Record(event)
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("modified recorded event bypassed redaction: %q", buf.String())
	}
}

func TestEmitAndMulti_RedactBeforeCustomRecorder(t *testing.T) {
	const secret = "sessionid=custom-recorder-secret"

	direct := &captureRecorder{}
	Emit(direct, Event{Kind: KindJobFail, Account: secret, Detail: secret})
	if len(direct.events) != 1 || strings.Contains(direct.events[0].Account+direct.events[0].Detail, secret) {
		t.Fatalf("Emit leaked to custom recorder: %+v", direct.events)
	}

	viaMulti := &captureRecorder{}
	Multi{viaMulti}.Record(Event{Kind: KindJobFail, Account: secret, Detail: secret})
	if len(viaMulti.events) != 1 || strings.Contains(viaMulti.events[0].Account+viaMulti.events[0].Detail, secret) {
		t.Fatalf("Multi leaked to custom recorder: %+v", viaMulti.events)
	}
}

func TestLog_RedactsSensitiveFields(t *testing.T) {
	const (
		proxy   = "socks5://proxy-user:proxy-pass@10.0.0.8:1080"
		account = "Cookie: sessionid=log-secret"
		detail  = "Authorization: Bearer token-secret " + proxy
	)
	var buf bytes.Buffer
	lg := NewLogWriter(&buf)
	// Direct Record must be safe even when callers bypass Emit.
	lg.Record(Event{Kind: KindJobFail, Reason: ReasonError, Proxy: proxy, Account: account, LeaseID: "lease-secret", Detail: detail})

	line := buf.String()
	for _, secret := range []string{proxy, account, detail, "proxy-user", "proxy-pass", "log-secret", "token-secret", "lease-secret"} {
		if strings.Contains(line, secret) {
			t.Fatalf("log leaked %q in %q", secret, line)
		}
	}
	for _, want := range []string{"node_ref=node_v1_", "account_ref=account_v1_", "lease_ref=lease_v1_", "detail_ref=detail_v1_"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log missing %q in %q", want, line)
		}
	}
}

func TestSafeReferences_AreScopedAndIdempotent(t *testing.T) {
	const value = "low-entropy-value"
	node := SafeNodeRef(value)
	if node != SafeNodeRef(value) {
		t.Fatal("same value did not preserve node correlation")
	}
	if node == SafeAccountRef(value) || node == SafeWorkerRef(value) || node == SafeJobRef(value) {
		t.Fatal("reference domains are not separated")
	}

	mem := NewMemory(2)
	Emit(mem, Event{Kind: KindJobFail, Proxy: value, Account: value, WorkerID: value, JobKey: value, Detail: value})
	first := mem.Events()[0]
	// Re-recording an event already sanitized by Emit must not change references.
	mem.Record(first)
	second := mem.Events()[1]
	if first.Proxy != second.Proxy || first.Account != second.Account || first.WorkerID != second.WorkerID || first.JobKey != second.JobKey || first.Detail != second.Detail {
		t.Fatalf("event was sanitized twice: first=%+v second=%+v", first, second)
	}
}

func TestDefault_RecordRedactsCustomRecorder(t *testing.T) {
	prev := Default()
	t.Cleanup(func() { SetDefault(prev) })

	const secret = "Cookie: default-recorder-secret"
	capture := &captureRecorder{}
	SetDefault(capture)
	Default().Record(Event{Kind: KindJobFail, Account: secret, Detail: secret})
	if len(capture.events) != 1 || strings.Contains(capture.events[0].Account+capture.events[0].Detail, secret) {
		t.Fatalf("Default leaked to custom recorder: %+v", capture.events)
	}
}

func TestSafe_DirectRecordRedactsCustomRecorder(t *testing.T) {
	const secret = "Authorization: Bearer safe-wrapper-secret"
	capture := &captureRecorder{}
	Safe(capture).Record(Event{Kind: KindJobFail, Detail: secret})
	if len(capture.events) != 1 || strings.Contains(capture.events[0].Detail, secret) {
		t.Fatalf("Safe wrapper leaked to custom recorder: %+v", capture.events)
	}
}

func TestSafeReference_ForgedPrefixCannotBypass(t *testing.T) {
	forged := "node_v1_00000000000000000000000000000000_00000000000000000000000000000000"
	if got := SafeNodeRef(forged); got == forged {
		t.Fatalf("forged safe reference bypassed redaction: %q", got)
	}
}

func TestLog_EscapesOperationalLabelControlCharacters(t *testing.T) {
	var buf bytes.Buffer
	NewLogWriter(&buf).Record(Event{
		Kind: "job.fail\nforged_kind", Reason: "error\rforged_reason",
		Platform: "steam\nforged=true", Source: "steam\task", JobKey: "job\rnext",
	})
	line := buf.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("operational label injected a log line: %q", line)
	}
	for _, escaped := range []string{`event="job.fail\nforged_kind"`, `reason="error\rforged_reason"`, `platform="steam\nforged=true"`, `source="steam\task"`} {
		if !strings.Contains(line, escaped) {
			t.Fatalf("log missing escaped label %q in %q", escaped, line)
		}
	}
}

func TestWrapError_HidesTextAndPreservesCause(t *testing.T) {
	cause := errors.New("proxy password=secret-password")
	err := WrapError("steam request", cause)
	if strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("wrapped error leaked cause: %q", err)
	}
	if !strings.Contains(err.Error(), "error_ref=detail_v1_") {
		t.Fatalf("wrapped error missing reference: %q", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped error lost cause")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		formatted := fmt.Sprintf(format, err)
		if strings.Contains(formatted, "secret-password") || !strings.Contains(formatted, "detail_v1_") {
			t.Fatalf("format %q exposed cause or lost reference: %q", format, formatted)
		}
	}
}

func TestMulti_Concurrent(t *testing.T) {
	mem := NewMemory(1000)
	var buf bytes.Buffer
	var mu sync.Mutex
	lg := &Log{
		Prefix: "buffgo observ",
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			// discard body; just ensure no race
			_ = format
			_ = args
			buf.WriteByte('.')
		},
	}
	r := Multi{mem, lg}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				RecordAcquire(r, PoolOutcomeOK, LeaseLabels{Platform: "steam", AppID: 252490, WorkerID: "w"}, "")
			} else {
				RecordAcquire(r, PoolOutcomeCooling, LeaseLabels{Platform: "buff", AppID: 730, WorkerID: "w"}, "")
			}
		}(i)
	}
	wg.Wait()
	snap := mem.Snapshot()
	if snap.LeaseAcquire != 25 || snap.RateLimitHit != 25 {
		t.Fatalf("concurrent snap: %+v", snap)
	}
}

func TestSnapshot_StringAndZeroRates(t *testing.T) {
	var s Snapshot
	if s.FetchSuccessRate() != 0 || s.JobSuccessRate() != 0 {
		t.Fatal("zero rates")
	}
	s.FetchOK, s.FetchFail = 3, 1
	if s.FetchSuccessRate() != 0.75 {
		t.Fatalf("rate=%v", s.FetchSuccessRate())
	}
	if !strings.Contains(s.String(), "fetch_ok=3") {
		t.Fatalf("string: %s", s.String())
	}
}

func TestDefaultRecorder(t *testing.T) {
	prev := Default()
	t.Cleanup(func() { SetDefault(prev) })

	mem := NewMemory(8)
	SetDefault(mem)
	Emit(Default(), Event{Kind: KindJobOK, Reason: ReasonOK, Source: "steam.sell", AppID: 252490})
	if mem.Snapshot().JobOK != 1 {
		t.Fatalf("default not recording: %+v", mem.Snapshot())
	}
	SetDefault(nil)
	// should not panic
	Emit(Default(), Event{Kind: KindJobFail, Reason: ReasonError})
}
