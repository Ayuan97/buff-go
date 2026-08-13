package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/storage/postgres"
)

type targetResponse struct {
	ID            collection.TargetID     `json:"id"`
	Revision      collection.Revision     `json:"revision"`
	Platform      collection.Platform     `json:"platform"`
	AppID         int64                   `json:"appid"`
	Side          market.Side             `json:"side"`
	Desired       collection.DesiredState `json:"desired"`
	Actual        collection.ActualState  `json:"actual"`
	SwitchVersion collection.Revision     `json:"switch_version"`
	Reason        collection.TargetReason `json:"reason,omitempty"`
	Recovery      collection.RecoveryMode `json:"recovery,omitempty"`
	RecheckAt     *time.Time              `json:"recheck_at,omitempty"`
	ChangedAt     time.Time               `json:"changed_at"`
}

type targetCreateRequest struct {
	Platform collection.Platform     `json:"platform"`
	AppID    int64                   `json:"appid"`
	Side     market.Side             `json:"side"`
	Desired  collection.DesiredState `json:"desired"`
}

type targetDesiredRequest struct {
	ExpectedRevision collection.Revision     `json:"expected_revision"`
	Desired          collection.DesiredState `json:"desired"`
}

type runResponse struct {
	ID               collection.RunID        `json:"id"`
	TargetID         *collection.TargetID    `json:"target_id,omitempty"`
	TaskType         collection.TaskType     `json:"task_type"`
	Platform         collection.Platform     `json:"platform"`
	AppID            int64                   `json:"appid"`
	Side             *market.Side            `json:"side,omitempty"`
	State            collection.RunState     `json:"state"`
	Completeness     collection.Completeness `json:"completeness,omitempty"`
	Reason           collection.RunReason    `json:"reason,omitempty"`
	Cursor           string                  `json:"cursor,omitempty"`
	LastPageSequence int64                   `json:"last_page_sequence"`
	CreatedAt        time.Time               `json:"created_at"`
	StartedAt        *time.Time              `json:"started_at,omitempty"`
	FinishedAt       *time.Time              `json:"finished_at,omitempty"`
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/runs" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				writeError(w, http.StatusBadRequest, "invalid_limit")
				return
			}
			limit = value
		}
		runs, err := h.collection.ListRecentRuns(r.Context(), limit)
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		out := make([]runResponse, 0, len(runs))
		for _, run := range runs {
			out = append(out, toRunResponse(run))
		}
		writeAccountJSON(w, http.StatusOK, out)
		return
	}
	if path == "/api/targets" {
		if r.Method == http.MethodGet {
			targets, err := h.collection.ListTargets(r.Context())
			if err != nil {
				writeCollectionError(w, err)
				return
			}
			out := make([]targetResponse, 0, len(targets))
			for _, target := range targets {
				out = append(out, toTargetResponse(target))
			}
			writeAccountJSON(w, http.StatusOK, out)
			return
		}
		if r.Method == http.MethodPost {
			var input targetCreateRequest
			if !decodeJSON(w, r, &input) {
				return
			}
			if input.Desired == "" {
				input.Desired = collection.DesiredDisabled
			}
			target, err := h.collection.CreateSummaryTarget(r.Context(), input.Platform, input.AppID, input.Side, input.Desired)
			if err != nil {
				writeCollectionError(w, err)
				return
			}
			writeAccountJSON(w, http.StatusCreated, toTargetResponse(target))
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/targets/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || value < 1 {
		writeError(w, http.StatusBadRequest, "invalid_target_id")
		return
	}
	id := collection.TargetID(value)
	if len(parts) == 1 && r.Method == http.MethodGet {
		target, found, err := h.collection.Target(r.Context(), id)
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "target_not_found")
			return
		}
		writeAccountJSON(w, http.StatusOK, toTargetResponse(target))
		return
	}
	if len(parts) == 2 && parts[1] == "desired" && r.Method == http.MethodPost {
		var input targetDesiredRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		target, err := h.collection.SetTargetDesired(r.Context(), id, input.ExpectedRevision, input.Desired)
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toTargetResponse(target))
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func toTargetResponse(target collection.Target) targetResponse {
	appID, _ := target.AppID()
	side, _ := target.Side()
	recheck, hasRecheck := target.RecheckAt()
	out := targetResponse{
		ID:            target.ID(),
		Revision:      target.Revision(),
		Platform:      target.Platform(),
		AppID:         appID,
		Side:          side,
		Desired:       target.Desired(),
		Actual:        target.Actual(),
		SwitchVersion: target.SwitchVersion(),
		Reason:        target.Reason(),
		Recovery:      target.Recovery(),
		ChangedAt:     target.ChangedAt(),
	}
	if hasRecheck {
		out.RecheckAt = &recheck
	}
	return out
}

func toRunResponse(run collection.Run) runResponse {
	out := runResponse{
		ID:               run.ID(),
		TaskType:         run.TaskType(),
		Platform:         run.Platform(),
		AppID:            run.AppID(),
		State:            run.State(),
		Completeness:     run.Completeness(),
		Reason:           run.Reason(),
		Cursor:           string(run.CurrentCursor().Bytes()),
		LastPageSequence: run.LastPageSequence(),
		CreatedAt:        run.CreatedAt(),
	}
	if id, ok := run.TargetID(); ok {
		out.TargetID = &id
	}
	if side, ok := run.Side(); ok {
		out.Side = &side
	}
	if started, ok := run.StartedAt(); ok {
		out.StartedAt = &started
	}
	if finished, ok := run.FinishedAt(); ok {
		out.FinishedAt = &finished
	}
	return out
}

func writeCollectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, collection.ErrNotFound):
		writeError(w, http.StatusNotFound, "collection_not_found")
	case errors.Is(err, collection.ErrConflict):
		writeError(w, http.StatusConflict, "collection_conflict")
	case errors.Is(err, collection.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_collection")
	case errors.Is(err, collection.ErrStorage):
		writeError(w, http.StatusServiceUnavailable, "collection_storage_unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "collection_error")
	}
}

type quoteResponse struct {
	ProductID          int64      `json:"product_id"`
	AppID              int64      `json:"appid"`
	Name               string     `json:"name"`
	Platform           string     `json:"platform"`
	Side               string     `json:"side"`
	Status             string     `json:"status"`
	ReasonCode         string     `json:"reason_code,omitempty"`
	CollectedAt        time.Time  `json:"collected_at"`
	SourceTime         *time.Time `json:"source_time,omitempty"`
	PresentCents       *int64     `json:"present_cents,omitempty"`
	PresentOrderCount  *int64     `json:"present_order_count,omitempty"`
	PresentCollectedAt *time.Time `json:"present_collected_at,omitempty"`
}

func (h *Handler) serveQuotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	appid := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("appid")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "invalid_appid")
			return
		}
		appid = value
	}
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = value
	}
	quotes, err := h.market.ListQuotes(r.Context(), appid, platform, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_quotes")
		return
	}
	out := make([]quoteResponse, 0, len(quotes))
	for _, quote := range quotes {
		out = append(out, toQuoteResponse(quote))
	}
	writeAccountJSON(w, http.StatusOK, out)
}

func toQuoteResponse(quote postgres.MarketQuote) quoteResponse {
	out := quoteResponse{
		ProductID:          int64(quote.ProductID),
		AppID:              quote.AppID,
		Name:               quote.Name,
		Platform:           quote.Platform,
		Side:               string(quote.Side),
		Status:             string(quote.Status),
		ReasonCode:         quote.ReasonCode,
		CollectedAt:        quote.CollectedAt,
		SourceTime:         quote.SourceTime,
		PresentOrderCount:  quote.PresentOrderCount,
		PresentCollectedAt: quote.PresentCollectedAt,
	}
	if quote.PresentCents != nil {
		cents := int64(*quote.PresentCents)
		out.PresentCents = &cents
	}
	return out
}
