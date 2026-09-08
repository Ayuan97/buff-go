package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/catalog"
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
	// 采集顺序决定补货从哪一头开始，改它会清空该方向队列。
	SortColumn collection.SortColumn    `json:"sort_column"`
	SortDir    collection.SortDirection `json:"sort_dir"`
	// 出售搜索交给 Steam 的人民币分区间。空表示不限。求购没有对应参数。
	PriceMinCents *int64 `json:"price_min_cents,omitempty"`
	PriceMaxCents *int64 `json:"price_max_cents,omitempty"`
	// Rust 出售搜索交给 Steam 的分类多选。空表示不限。求购没有对应参数。
	SteamCats   []string `json:"steam_cats"`
	ItemClasses []string `json:"item_classes"`
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

type targetPriceRangeRequest struct {
	ExpectedRevision collection.Revision `json:"expected_revision"`
	MinCents         *int64              `json:"min_cents"`
	MaxCents         *int64              `json:"max_cents"`
}

type targetSteamFacetsRequest struct {
	ExpectedRevision collection.Revision `json:"expected_revision"`
	SteamCats        []string            `json:"steam_cats"`
	ItemClasses      []string            `json:"item_classes"`
}

type workerItemResponse struct {
	ProductID  int64  `json:"product_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	PriceCents *int64 `json:"present_cents,omitempty"`
}

type workerClaimResponse struct {
	TargetID  collection.TargetID  `json:"target_id"`
	TaskID    collection.TaskID    `json:"task_id,omitempty"`
	AppID     int64                `json:"appid"`
	Side      market.Side          `json:"side"`
	Platform  collection.Platform  `json:"platform"`
	Kind      collection.TaskKind  `json:"kind,omitempty"`
	Endpoint  string               `json:"endpoint,omitempty"`
	ClaimedAt *time.Time           `json:"claimed_at,omitempty"`
	Active    bool                 `json:"active"`
	Items     []workerItemResponse `json:"items"`
}

type workerPageResponse struct {
	TargetID    collection.TargetID  `json:"target_id"`
	AppID       int64                `json:"appid"`
	Side        market.Side          `json:"side"`
	Platform    collection.Platform  `json:"platform"`
	CommittedAt time.Time            `json:"committed_at"`
	Items       []workerItemResponse `json:"items"`
}

type workerResponse struct {
	CombinationID int64                `json:"combination_id"`
	AccountID     int64                `json:"account_id"`
	AccountAlias  string               `json:"account_alias"`
	Platform      string               `json:"platform"`
	NodeID        int64                `json:"node_id"`
	NodeName      string               `json:"node_name"`
	ExitAddress   string               `json:"exit_address,omitempty"`
	Region        string               `json:"region"`
	SessionState  string               `json:"session_state"`
	Idle          bool                 `json:"idle"`
	Claim         *workerClaimResponse `json:"claim,omitempty"`
	ActiveWaits   []workerWaitResponse `json:"active_waits"`
	LastPage      *workerPageResponse  `json:"last_page,omitempty"`
}

type workerWaitResponse struct {
	Scope         collection.WorkerWaitScope  `json:"scope"`
	Reason        collection.WorkerWaitReason `json:"reason"`
	RetryAt       time.Time                   `json:"retry_at"`
	RetryAfterSec *int64                      `json:"retry_after_sec,omitempty"`
	Platform      collection.Platform         `json:"platform"`
	Endpoint      string                      `json:"endpoint,omitempty"`
	Side          market.Side                 `json:"side,omitempty"`
	AccountID     int64                       `json:"account_id,omitempty"`
	ExitAddress   string                      `json:"exit_address,omitempty"`
	NodeID        int64                       `json:"node_id,omitempty"`
	CombinationID int64                       `json:"combination_id,omitempty"`
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/workers" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		workers, err := h.collection.ListWorkers(r.Context())
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		out := make([]workerResponse, 0, len(workers))
		for _, worker := range workers {
			out = append(out, toWorkerResponse(worker))
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
	if len(parts) == 2 && parts[1] == "price-range" && r.Method == http.MethodPost {
		var input targetPriceRangeRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		bounds := collection.PriceRange{MinCents: input.MinCents, MaxCents: input.MaxCents}
		target, err := h.collection.SetTargetPriceRange(r.Context(), id, input.ExpectedRevision, bounds)
		if err != nil {
			writeCollectionError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toTargetResponse(target))
		return
	}
	if len(parts) == 2 && parts[1] == "steam-facets" && r.Method == http.MethodPost {
		var input targetSteamFacetsRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		facets := collection.SteamFacets{Cats: input.SteamCats, Classes: input.ItemClasses}
		target, err := h.collection.SetTargetSteamFacets(r.Context(), id, input.ExpectedRevision, facets)
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

func toWorkerResponse(worker collection.WorkerSnapshot) workerResponse {
	out := workerResponse{
		CombinationID: int64(worker.Combination.ID),
		AccountID:     int64(worker.Combination.AccountID),
		AccountAlias:  worker.AccountAlias,
		Platform:      string(worker.Combination.Platform),
		NodeID:        int64(worker.Combination.NodeID),
		NodeName:      worker.NodeName,
		ExitAddress:   worker.ExitAddress,
		Region:        string(worker.Region),
		SessionState:  string(worker.SessionState),
		Idle:          worker.Idle,
		ActiveWaits:   make([]workerWaitResponse, 0, len(worker.ActiveWaits)),
	}
	if worker.Claim != nil {
		claim := &workerClaimResponse{
			TargetID: worker.Claim.TargetID,
			TaskID:   worker.Claim.TaskID,
			AppID:    worker.Claim.AppID,
			Side:     worker.Claim.Side,
			Platform: worker.Claim.Platform,
			Kind:     worker.Claim.Kind,
			Endpoint: string(worker.Claim.Endpoint),
			Active:   worker.Claim.Active,
			Items:    toWorkerItemResponses(worker.Claim.Items),
		}
		if !worker.Claim.ClaimedAt.IsZero() {
			claimedAt := worker.Claim.ClaimedAt
			claim.ClaimedAt = &claimedAt
		}
		out.Claim = claim
	}
	for _, wait := range worker.ActiveWaits {
		item := workerWaitResponse{
			Scope: wait.Scope, Reason: wait.Reason, RetryAt: wait.RetryAt,
			Platform: wait.Platform, Endpoint: string(wait.Endpoint), Side: wait.Side,
			AccountID: int64(wait.AccountID), ExitAddress: wait.ExitAddress,
			NodeID: int64(wait.NodeID), CombinationID: int64(wait.CombinationID),
		}
		if !wait.RetryAt.IsZero() {
			sec := int64(time.Until(wait.RetryAt) / time.Second)
			if sec < 0 {
				sec = 0
			}
			item.RetryAfterSec = &sec
		}
		out.ActiveWaits = append(out.ActiveWaits, item)
	}
	if worker.LastPage != nil {
		out.LastPage = &workerPageResponse{
			TargetID:    worker.LastPage.TargetID,
			AppID:       worker.LastPage.AppID,
			Side:        worker.LastPage.Side,
			Platform:    worker.LastPage.Platform,
			CommittedAt: worker.LastPage.CommittedAt,
			Items:       toWorkerItemResponses(worker.LastPage.Items),
		}
	}
	return out
}

func toWorkerItemResponses(items []collection.WorkerItem) []workerItemResponse {
	out := make([]workerItemResponse, 0, len(items))
	for _, item := range items {
		out = append(out, workerItemResponse{
			ProductID: item.ProductID, Name: item.Name, Status: item.Status, PriceCents: item.PriceCents,
		})
	}
	return out
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
		PriceMinCents: target.PriceRange().MinCents,
		PriceMaxCents: target.PriceRange().MaxCents,
		SteamCats:     jsonStrings(target.SteamFacets().Cats),
		ItemClasses:   jsonStrings(target.SteamFacets().Classes),
	}
	if hasRecheck {
		out.RecheckAt = &recheck
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
	DropCents          *int64     `json:"drop_cents,omitempty"`
	HighCents          *int64     `json:"high_cents,omitempty"`
	DropPctBP          *int64     `json:"drop_pct_bp,omitempty"`
	DropCount          *int64     `json:"drop_count,omitempty"`
	LastDropAt         *time.Time `json:"last_drop_at,omitempty"`
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
	productID := int64(0)
	if raw := strings.TrimSpace(query.Get("product_id")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "invalid_product_id")
			return
		}
		productID = value
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
	filter := market.QuoteFilter{
		AppID:      appid,
		ProductID:  productID,
		Platform:   strings.TrimSpace(query.Get("platform")),
		Side:       market.Side(strings.TrimSpace(query.Get("side"))),
		Keyword:    strings.TrimSpace(query.Get("keyword")),
		ItemType:   strings.TrimSpace(query.Get("item_type")),
		ItemTypes:  queryList(query, "item_types"),
		SteamCats:  queryList(query, "steam_cats"),
		Sort:       market.QuoteSort(strings.TrimSpace(query.Get("sort"))),
		DropWindow: market.DropWindow(strings.TrimSpace(query.Get("drop_window"))),
		DropsOnly:  droppedOnly(query.Get("dropped")),
		Limit:      limit,
		Offset:     offset,
	}
	for _, bound := range []struct {
		name   string
		target **int64
	}{{"min_cents", &filter.MinCents}, {"max_cents", &filter.MaxCents}, {"min_drop_cents", &filter.MinDropCents}} {
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
		if errors.Is(err, market.ErrStorage) {
			writeError(w, http.StatusServiceUnavailable, "market_storage_unavailable")
			return
		}
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
		if errors.Is(err, market.ErrStorage) {
			writeError(w, http.StatusServiceUnavailable, "market_storage_unavailable")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_quotes")
		return
	}
	writeAccountJSON(w, http.StatusOK, struct {
		AppIDs    []int64  `json:"appids"`
		ItemTypes []string `json:"item_types"`
	}{AppIDs: facets.AppIDs, ItemTypes: facets.ItemTypes})
}

// serveSteamFacets 返回 Rust 市场勾选词表，给配置页和行情筛选用。
func (h *Handler) serveSteamFacets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("appid"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "invalid_appid")
		return
	}
	appid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || appid < 1 {
		writeError(w, http.StatusBadRequest, "invalid_appid")
		return
	}
	out := struct {
		Categories  []catalog.RustCategory  `json:"categories"`
		ItemClasses []catalog.RustItemClass `json:"item_classes"`
	}{Categories: []catalog.RustCategory{}, ItemClasses: []catalog.RustItemClass{}}
	if appid == catalog.AppIDRust {
		out.Categories = catalog.RustCategories()
		out.ItemClasses = catalog.RustItemClasses()
	}
	writeAccountJSON(w, http.StatusOK, out)
}

func droppedOnly(raw string) bool {
	value := strings.TrimSpace(raw)
	return value == "1" || value == "true"
}

func queryList(query map[string][]string, key string) []string {
	raw := query[key]
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func jsonStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func toQuoteResponse(quote market.Quote) quoteResponse {
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
	out.DropCents = quote.DropCents
	out.HighCents = quote.HighCents
	out.DropPctBP = quote.DropPctBP
	out.DropCount = quote.DropCount
	out.LastDropAt = quote.LastDropAt
	return out
}
