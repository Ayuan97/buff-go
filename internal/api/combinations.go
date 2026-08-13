package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

type combinationResponse struct {
	ID        resource.CombinationID `json:"id"`
	Platform  resource.Platform      `json:"platform"`
	AccountID resource.AccountID     `json:"account_id"`
	NodeID    resource.NodeID        `json:"node_id"`
}

type combinationCreateRequest struct {
	AccountID resource.AccountID `json:"account_id"`
	NodeID    resource.NodeID    `json:"node_id"`
}

func (h *Handler) serveCombinations(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/combinations" {
		if r.Method == http.MethodGet {
			combinations, err := h.combinations.ListCombinations(r.Context())
			if err != nil {
				writeCombinationError(w, err)
				return
			}
			out := make([]combinationResponse, 0, len(combinations))
			for _, combination := range combinations {
				out = append(out, toCombinationResponse(combination))
			}
			writeAccountJSON(w, http.StatusOK, out)
			return
		}
		if r.Method == http.MethodPost {
			var input combinationCreateRequest
			if !decodeJSON(w, r, &input) {
				return
			}
			combination, err := h.combinations.CreateCombination(r.Context(), input.AccountID, input.NodeID)
			if err != nil {
				writeCombinationError(w, err)
				return
			}
			writeAccountJSON(w, http.StatusCreated, toCombinationResponse(combination))
			return
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/combinations/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || value < 1 {
		writeError(w, http.StatusBadRequest, "invalid_combination_id")
		return
	}
	id := resource.CombinationID(value)
	if len(parts) == 1 && r.Method == http.MethodGet {
		combination, found, err := h.combinations.Combination(r.Context(), id)
		if err != nil {
			writeCombinationError(w, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "combination_not_found")
			return
		}
		writeAccountJSON(w, http.StatusOK, toCombinationResponse(combination))
		return
	}
	if len(parts) != 2 || parts[1] != "delete" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if err != nil || len(body) != 0 {
		writeError(w, http.StatusBadRequest, "body_not_allowed")
		return
	}
	if err := h.combinations.DeleteCombination(r.Context(), id); err != nil {
		writeCombinationError(w, err)
		return
	}
	writeAccountJSON(w, http.StatusOK, struct {
		Deleted bool `json:"deleted"`
	}{Deleted: true})
}

func toCombinationResponse(combination resource.AccountNodeCombination) combinationResponse {
	return combinationResponse{
		ID: combination.ID, Platform: combination.Platform,
		AccountID: combination.AccountID, NodeID: combination.NodeID,
	}
}

func writeCombinationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, resource.ErrResourceOccupied):
		writeError(w, http.StatusConflict, "combination_occupied")
	case errors.Is(err, postgres.ErrCombinationIncompatible):
		writeError(w, http.StatusConflict, "combination_incompatible")
	case errors.Is(err, postgres.ErrCombinationConflict):
		writeError(w, http.StatusConflict, "combination_conflict")
	case errors.Is(err, postgres.ErrResourceNotFound):
		writeError(w, http.StatusNotFound, "combination_not_found")
	case errors.Is(err, postgres.ErrResourceDependency):
		writeError(w, http.StatusConflict, "resource_in_use")
	case errors.Is(err, postgres.ErrInvalidResource):
		writeError(w, http.StatusBadRequest, "invalid_combination")
	case errors.Is(err, postgres.ErrResourceStorage):
		writeError(w, http.StatusServiceUnavailable, "resource_storage_unavailable")
	case errors.Is(err, postgres.ErrResourceIntegrity):
		writeError(w, http.StatusInternalServerError, "resource_integrity")
	default:
		writeError(w, http.StatusBadRequest, "invalid_combination")
	}
}
