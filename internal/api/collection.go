package api

import (
	"errors"
	"io"
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
	// 采集顺序决定这一轮先采到哪一头，改它会作废当前批次。
	SortColumn collection.SortColumn    `json:"sort_column"`
	SortDir    collection.SortDirection `json:"sort_dir"`
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

type targetSortRequest struct {
	ExpectedRevision collection.Revision      `json:"expected_revision"`
	Column           collection.SortColumn    `json:"sort_column"`
	Direction        collection.SortDirection `json:"sort_dir"`
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

type pageResponse struct {
	PageSequence int64     `json:"page_sequence"`
	CursorBefore string    `json:"cursor_before,omitempty"`
	CursorAfter  string    `json:"cursor_after,omitempty"`
	CollectedAt  time.Time `json:"collected_at"`
	CommittedAt  time.Time `json:"committed_at"`
	PayloadBytes int64     `json:"payload_bytes"`
	// 归属在本能力上线之前提交的页上是空的。
	AccountID    int64  `json:"account_id,omitempty"`
	AccountAlias string `json:"account_alias,omitempty"`
	ExitAddress  string `json:"exit_address,omitempty"`
}

type pageAttemptResponse struct {
	ProductID   int64      `json:"product_id"`
	AppID       int64      `json:"appid"`
	Name        string     `json:"name"`
	Platform    string     `json:"platform"`
	Side        string     `json:"side"`
	Status      string     `json:"status"`
	ReasonCode  string     `json:"reason_code,omitempty"`
	CollectedAt time.Time  `json:"collected_at"`
	SourceTime  *time.Time `json:"source_time,omitempty"`
	PriceCents  *int64     `json:"present_cents,omitempty"`
	OrderCount  *int64     `json:"present_order_count,omitempty"`
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasPrefix(path, "/api/runs/") {
		h.serveRunPages(w, r, path)
		return
	}
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
	if len(parts) == 2 && parts[1] == "sort" && r.Method == http.MethodPost {
		var input targetSortRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		order := collection.SortOrder{Column: input.Column, Direction: input.Direction}
		target, err := h.collection.SetTargetSortOrder(r.Context(), id, input.ExpectedRevision, order)
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toTargetResponse(target))
		return
	}
	if len(parts) == 2 && parts[1] == "delete" && r.Method == http.MethodPost {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(body) != 0 {
			writeError(w, http.StatusBadRequest, "body_not_allowed")
			return
		}
		if err := h.collection.DeleteTarget(r.Context(), id); err != nil {
			writeCollectionError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, struct {
			Deleted bool `json:"deleted"`
		}{Deleted: true})
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

// serveRunPages 处理批次下钻：页列表、单页原始响应、单页写入的行情。全部只读。
func (h *Handler) serveRunPages(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/runs/"), "/")
	runValue, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || runValue < 1 {
		writeError(w, http.StatusBadRequest, "invalid_run_id")
		return
	}
	runID := collection.RunID(runValue)
	if len(parts) == 2 && parts[1] == "pages" {
		summaries, err := h.collection.PageSummaries(r.Context(), runID)
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		out := make([]pageResponse, 0, len(summaries))
		for _, summary := range summaries {
			out = append(out, pageResponse{
				PageSequence: summary.PageSequence,
				CursorBefore: summary.CursorBefore,
				CursorAfter:  summary.CursorAfter,
				CollectedAt:  summary.CollectedAt,
				CommittedAt:  summary.CommittedAt,
				PayloadBytes: summary.PayloadBytes,
				AccountID:    summary.AccountID,
				AccountAlias: summary.AccountAlias,
				ExitAddress:  summary.ExitAddress,
			})
		}
		writeAccountJSON(w, http.StatusOK, out)
		return
	}
	if len(parts) != 4 || parts[1] != "pages" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	pageValue, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || pageValue < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page_sequence")
		return
	}
	sequence := collection.Sequence(pageValue)
	switch parts[3] {
	case "payload":
		payload, err := h.collection.PagePayload(r.Context(), runID, sequence)
		if errors.Is(err, postgres.ErrPagePayloadNotFound) {
			writeError(w, http.StatusNotFound, "page_payload_not_found")
			return
		}
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		// 原样回传平台响应，控制台只作展示，不再解释内容
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	case "attempts":
		attempts, err := h.collection.PageAttempts(r.Context(), runID, sequence)
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		out := make([]pageAttemptResponse, 0, len(attempts))
		for _, attempt := range attempts {
			out = append(out, pageAttemptResponse{
				ProductID:   attempt.ProductID,
				AppID:       attempt.AppID,
				Name:        attempt.Name,
				Platform:    attempt.Platform,
				Side:        attempt.Side,
				Status:      attempt.Status,
				ReasonCode:  attempt.ReasonCode,
				CollectedAt: attempt.CollectedAt,
				SourceTime:  attempt.SourceTime,
				PriceCents:  attempt.PriceCents,
				OrderCount:  attempt.OrderCount,
			})
		}
		writeAccountJSON(w, http.StatusOK, out)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
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
		SortColumn:    target.Sort().Column,
		SortDir:       target.Sort().Direction,
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
	case errors.Is(err, postgres.ErrTargetNotStopped):
		writeError(w, http.StatusConflict, "target_not_stopped")
	case errors.Is(err, postgres.ErrTargetInUse):
		writeError(w, http.StatusConflict, "collection_in_use")
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
	ProductID int64  `json:"product_id"`
	AppID     int64  `json:"appid"`
	Name      string `json:"name"`
	// IconPath 是平台图片路径片段，前端拼上 CDN 前缀才是图片地址。
	IconPath           string     `json:"icon_path,omitempty"`
	ItemType           string     `json:"item_type,omitempty"`
	NameColor          string     `json:"name_color,omitempty"`
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
	query := r.URL.Query()
	appid := int64(0)
	if raw := strings.TrimSpace(query.Get("appid")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "invalid_appid")
			return
		}
		appid = value
	}
	limit := 60
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = value
	}
	offset := 0
	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset")
			return
		}
		offset = value
	}
	filter := postgres.MarketQuoteFilter{
		AppID:    appid,
		Platform: strings.TrimSpace(query.Get("platform")),
		Side:     market.Side(strings.TrimSpace(query.Get("side"))),
		Keyword:  strings.TrimSpace(query.Get("keyword")),
		ItemType: strings.TrimSpace(query.Get("item_type")),
		Sort:     postgres.QuoteSort(strings.TrimSpace(query.Get("sort"))),
		Limit:    limit,
		Offset:   offset,
	}
	for _, bound := range []struct {
		name   string
		target **int64
	}{{"min_cents", &filter.MinCents}, {"max_cents", &filter.MaxCents}} {
		raw := strings.TrimSpace(query.Get(bound.name))
		if raw == "" {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writeError(w, http.StatusBadRequest, "invalid_price_range")
			return
		}
		*bound.target = &value
	}
	result, err := h.market.ListQuotes(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_quotes")
		return
	}
	out := struct {
		Total  int64           `json:"total"`
		Quotes []quoteResponse `json:"quotes"`
	}{Total: result.Total, Quotes: make([]quoteResponse, 0, len(result.Quotes))}
	for _, quote := range result.Quotes {
		out.Quotes = append(out.Quotes, toQuoteResponse(quote))
	}
	writeAccountJSON(w, http.StatusOK, out)
}

// serveQuoteFacets 给行情页的筛选器提供已采数据里出现过的游戏与分类。
func (h *Handler) serveQuoteFacets(w http.ResponseWriter, r *http.Request) {
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
	facets, err := h.market.QuoteFacets(r.Context(), appid)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_quotes")
		return
	}
	writeAccountJSON(w, http.StatusOK, struct {
		AppIDs    []int64  `json:"appids"`
		ItemTypes []string `json:"item_types"`
	}{AppIDs: facets.AppIDs, ItemTypes: facets.ItemTypes})
}

func toQuoteResponse(quote postgres.MarketQuote) quoteResponse {
	out := quoteResponse{
		ProductID:          int64(quote.ProductID),
		AppID:              quote.AppID,
		Name:               quote.Name,
		IconPath:           quote.Media.IconPath,
		ItemType:           quote.Media.ItemType,
		NameColor:          quote.Media.NameColor,
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
