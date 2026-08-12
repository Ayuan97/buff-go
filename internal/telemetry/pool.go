package telemetry

import "fmt"

// Pool outcome reason strings (match manager Lua / Go branches).
const (
	PoolOutcomeOK           = "ok"
	PoolOutcomeCooling      = "cooling"
	PoolOutcomeBusy         = "busy"
	PoolOutcomeQuota        = "quota"
	PoolOutcomeLineMismatch = "line_mismatch"
	PoolOutcomeAppID        = "appid_not_allowed"
	PoolOutcomeNotOwner     = "not_owner"
	PoolOutcomeNotFound     = "not_found"
	PoolOutcomeInvalid      = "invalid"
	PoolOutcomeError        = "error"
)

// LeaseLabels carries common lease dimensions for event emission.
type LeaseLabels struct {
	Platform string
	AppID    int64
	WorkerID string
	Proxy    string
	Account  string
	LeaseID  string
}

// JobResources links a job event to the worker and resource combination used.
// Values may be legacy raw identifiers; the recorder converts them to safe refs.
type JobResources struct {
	WorkerID string
	Proxy    string
	Account  string
	LeaseID  string
}

// ClassifyAcquire maps an Acquire outcome string to Kind+Reason.
func ClassifyAcquire(outcome string) (Kind, Reason) {
	switch outcome {
	case PoolOutcomeOK:
		return KindLeaseAcquire, ReasonOK
	case PoolOutcomeCooling:
		return KindRateLimit, ReasonCooling
	case PoolOutcomeQuota:
		return KindRateLimit, ReasonQuota
	case PoolOutcomeBusy:
		return KindLeaseConflict, ReasonBusy
	case PoolOutcomeLineMismatch:
		return KindLeaseConflict, ReasonLineMismatch
	case PoolOutcomeAppID:
		return KindLeaseConflict, ReasonAppIDNotAllowed
	case PoolOutcomeInvalid:
		return KindLeaseConflict, ReasonInvalid
	default:
		return KindLeaseConflict, ReasonError
	}
}

// ClassifyRelease maps a Release outcome string to Kind+Reason.
func ClassifyRelease(outcome string, cooldownSet bool) (Kind, Reason) {
	switch outcome {
	case PoolOutcomeOK:
		if cooldownSet {
			return KindLeaseRelease, ReasonCooldownSet
		}
		return KindLeaseRelease, ReasonOK
	case PoolOutcomeNotOwner:
		return KindLeaseConflict, ReasonNotOwner
	case PoolOutcomeNotFound:
		return KindLeaseConflict, ReasonNotFound
	case PoolOutcomeInvalid:
		return KindLeaseConflict, ReasonInvalid
	default:
		return KindLeaseConflict, ReasonError
	}
}

// RecordAcquire emits the appropriate lease/rate-limit event for an Acquire outcome.
func RecordAcquire(r Recorder, outcome string, lab LeaseLabels, detail string) {
	kind, reason := ClassifyAcquire(outcome)
	Emit(r, Event{
		Kind:     kind,
		Reason:   reason,
		Platform: lab.Platform,
		AppID:    lab.AppID,
		WorkerID: lab.WorkerID,
		Proxy:    lab.Proxy,
		Account:  lab.Account,
		LeaseID:  lab.LeaseID,
		Detail:   detail,
	})
}

// RecordRelease emits the appropriate event for a Release outcome.
func RecordRelease(r Recorder, outcome string, cooldownSet bool, lab LeaseLabels, detail string) {
	kind, reason := ClassifyRelease(outcome, cooldownSet)
	Emit(r, Event{
		Kind:     kind,
		Reason:   reason,
		Platform: lab.Platform,
		AppID:    lab.AppID,
		WorkerID: lab.WorkerID,
		Proxy:    lab.Proxy,
		Account:  lab.Account,
		LeaseID:  lab.LeaseID,
		Detail:   detail,
	})
}

// RecordJobOK emits job.ok (+ fetch.ok) for a successful pipeline pass.
func RecordJobOK(r Recorder, source, platform, jobKey string, appid int64, fetched, quotes int) {
	RecordJobOKWithResources(r, source, platform, jobKey, appid, fetched, quotes, JobResources{})
}

// RecordJobOKWithResources emits successful fetch/job events with resource labels.
func RecordJobOKWithResources(r Recorder, source, platform, jobKey string, appid int64, fetched, quotes int, resources JobResources) {
	RecordFetchOKWithResources(r, source, platform, jobKey, appid, fetched, resources)
	Emit(r, Event{
		Kind:     KindJobOK,
		Reason:   ReasonOK,
		Platform: platform,
		Source:   source,
		AppID:    appid,
		JobKey:   jobKey,
		WorkerID: resources.WorkerID,
		Proxy:    resources.Proxy,
		Account:  resources.Account,
		LeaseID:  resources.LeaseID,
		Count:    int64(quotes),
		Detail:   fmt.Sprintf("fetched=%d quotes=%d", fetched, quotes),
	})
}

// RecordFetchOKWithResources emits a successful fetch event with resource labels.
func RecordFetchOKWithResources(r Recorder, source, platform, jobKey string, appid int64, fetched int, resources JobResources) {
	Emit(r, Event{
		Kind:     KindFetchOK,
		Reason:   ReasonOK,
		Platform: platform,
		Source:   source,
		AppID:    appid,
		JobKey:   jobKey,
		WorkerID: resources.WorkerID,
		Proxy:    resources.Proxy,
		Account:  resources.Account,
		LeaseID:  resources.LeaseID,
		Count:    int64(fetched),
	})
}

// RecordFetchFailWithResources emits a failed fetch event without deciding the
// final job outcome. A partial fetch may still persist completed pages first.
func RecordFetchFailWithResources(r Recorder, source, platform, jobKey string, appid int64, reason Reason, detail string, resources JobResources) {
	if reason == "" {
		reason = ReasonError
	}
	Emit(r, Event{
		Kind:     KindFetchFail,
		Reason:   reason,
		Platform: platform,
		Source:   source,
		AppID:    appid,
		JobKey:   jobKey,
		WorkerID: resources.WorkerID,
		Proxy:    resources.Proxy,
		Account:  resources.Account,
		LeaseID:  resources.LeaseID,
		Detail:   detail,
	})
}

// RecordJobFail emits job.fail and optionally fetch.fail when the fetch stage failed.
func RecordJobFail(r Recorder, source, platform, jobKey string, appid int64, fetchFailed bool, reason Reason, detail string) {
	RecordJobFailWithResources(r, source, platform, jobKey, appid, fetchFailed, reason, detail, JobResources{})
}

// RecordJobFailWithResources emits failure events linked to safe resource refs.
func RecordJobFailWithResources(r Recorder, source, platform, jobKey string, appid int64, fetchFailed bool, reason Reason, detail string, resources JobResources) {
	if reason == "" {
		reason = ReasonError
	}
	if fetchFailed {
		RecordFetchFailWithResources(r, source, platform, jobKey, appid, reason, detail, resources)
	}
	Emit(r, Event{
		Kind:     KindJobFail,
		Reason:   reason,
		Platform: platform,
		Source:   source,
		AppID:    appid,
		JobKey:   jobKey,
		WorkerID: resources.WorkerID,
		Proxy:    resources.Proxy,
		Account:  resources.Account,
		LeaseID:  resources.LeaseID,
		Detail:   detail,
	})
}

// IsRateLimitReason reports whether reason is a platform 限频 class outcome.
func IsRateLimitReason(reason Reason) bool {
	return reason == ReasonCooling || reason == ReasonQuota
}
