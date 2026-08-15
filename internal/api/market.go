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

type priceTickResponse struct {
	TickID      int64     `json:"tick_id"`
	ProductID   int64     `json:"product_id"`
	AppID       int64     `json:"appid"`
	Name        string    `json:"name"`
	Platform    string    `json:"platform"`
	Side        string    `json:"side"`
	PrevCents   *int64    `json:"prev_cents,omitempty"`
	PriceCents  int64     `json:"price_cents"`
	CollectedAt time.Time `json:"collected_at"`
}

func (h *Handler) servePriceTicks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	query := r.URL.Query()
	productID := int64(0)
	if raw := strings.TrimSpace(query.Get("product_id")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "invalid_product_id")
			return
		}
		productID = value
	}
	limit := 50
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			writeError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = value
	}
	side := market.Side(strings.TrimSpace(query.Get("side")))
	if side != "" && side != market.SideBid && side != market.SideAsk {
		writeError(w, http.StatusBadRequest, "invalid_side")
		return
	}
	filter := postgres.PriceTickFilter{
		ProductID: productID,
		Platform:  strings.TrimSpace(query.Get("platform")),
		Side:      side,
		Limit:     limit,
	}
	ticks, err := h.market.ListPriceTicks(r.Context(), filter)
	if err != nil {
		if errors.Is(err, collection.ErrStorage) {
			writeError(w, http.StatusServiceUnavailable, "collection_storage_unavailable")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_price_ticks")
		return
	}
	out := struct {
		Ticks []priceTickResponse `json:"ticks"`
	}{Ticks: make([]priceTickResponse, 0, len(ticks))}
	for _, tick := range ticks {
		out.Ticks = append(out.Ticks, priceTickResponse{
			TickID:      tick.TickID,
			ProductID:   int64(tick.ProductID),
			AppID:       tick.AppID,
			Name:        tick.Name,
			Platform:    tick.Platform,
			Side:        string(tick.Side),
			PrevCents:   tick.PrevCents,
			PriceCents:  tick.PriceCents,
			CollectedAt: tick.CollectedAt,
		})
	}
	writeAccountJSON(w, http.StatusOK, out)
}
