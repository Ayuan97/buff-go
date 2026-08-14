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

type watermarkResponse struct {
	Region    resource.NodeRegion `json:"region"`
	MinUsable int                 `json:"min_usable"`
	Usable    int                 `json:"usable"`
	Revision  int64               `json:"revision"`
}

type watermarkSetRequest struct {
	Region           resource.NodeRegion `json:"region"`
	ExpectedRevision int64               `json:"expected_revision"`
	MinUsable        int                 `json:"min_usable"`
}

type providerResponse struct {
	ID            resource.ProviderID   `json:"id"`
	Name          string                `json:"name"`
	Enabled       bool                  `json:"enabled"`
	Priority      int                   `json:"priority"`
	Regions       []resource.NodeRegion `json:"regions"`
	HasCredential bool                  `json:"has_credential"`
	Revision      int64                 `json:"revision"`
}

type providerCreateRequest struct {
	Name       string                `json:"name"`
	Enabled    *bool                 `json:"enabled"`
	Priority   int                   `json:"priority"`
	Regions    []resource.NodeRegion `json:"regions"`
	Credential string                `json:"credential"`
}

type providerUpdateRequest struct {
	ExpectedRevision int64                 `json:"expected_revision"`
	Name             string                `json:"name"`
	Enabled          bool                  `json:"enabled"`
	Priority         int                   `json:"priority"`
	Regions          []resource.NodeRegion `json:"regions"`
}

type providerCredentialRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Credential       string `json:"credential"`
}

func (h *Handler) serveWatermarks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/watermarks" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		marks, err := h.providers.ListWatermarks(r.Context())
		if err != nil {
			writeProviderError(w, err)
			return
		}
		out := make([]watermarkResponse, 0, len(marks))
		for _, mark := range marks {
			out = append(out, toWatermarkResponse(mark))
		}
		writeAccountJSON(w, http.StatusOK, out)
		return
	}
	if r.Method == http.MethodPost {
		var input watermarkSetRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		mark, err := h.providers.SetWatermark(r.Context(), input.Region, input.ExpectedRevision, input.MinUsable)
		if err != nil {
			writeProviderError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toWatermarkResponse(mark))
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func (h *Handler) serveProviders(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/providers" {
		if r.Method == http.MethodGet {
			providers, err := h.providers.ListProviders(r.Context())
			if err != nil {
				writeProviderError(w, err)
				return
			}
			out := make([]providerResponse, 0, len(providers))
			for _, provider := range providers {
				out = append(out, toProviderResponse(provider))
			}
			writeAccountJSON(w, http.StatusOK, out)
			return
		}
		if r.Method == http.MethodPost {
			var input providerCreateRequest
			if !decodeJSON(w, r, &input) {
				return
			}
			if strings.TrimSpace(input.Credential) == "" {
				writeError(w, http.StatusBadRequest, "credential_required")
				return
			}
			if len(input.Credential) > 64<<10 {
				writeError(w, http.StatusRequestEntityTooLarge, "credential_too_large")
				return
			}
			enabled := true
			if input.Enabled != nil {
				enabled = *input.Enabled
			}
			provider, err := h.providers.CreateProvider(r.Context(), input.Name, enabled, input.Priority, input.Regions, []byte(input.Credential))
			if err != nil {
				writeProviderError(w, err)
				return
			}
			writeAccountJSON(w, http.StatusCreated, toProviderResponse(provider))
			return
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/providers/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || value < 1 {
		writeError(w, http.StatusBadRequest, "invalid_provider_id")
		return
	}
	id := resource.ProviderID(value)
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch parts[1] {
	case "update":
		var input providerUpdateRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		provider, err := h.providers.UpdateProvider(r.Context(), id, input.ExpectedRevision, input.Name, input.Enabled, input.Priority, input.Regions)
		if err != nil {
			writeProviderError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toProviderResponse(provider))
	case "credential":
		var input providerCredentialRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		if strings.TrimSpace(input.Credential) == "" {
			writeError(w, http.StatusBadRequest, "credential_required")
			return
		}
		if len(input.Credential) > 64<<10 {
			writeError(w, http.StatusRequestEntityTooLarge, "credential_too_large")
			return
		}
		provider, err := h.providers.ReplaceProviderCredential(r.Context(), id, input.ExpectedRevision, []byte(input.Credential))
		if err != nil {
			writeProviderError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toProviderResponse(provider))
	case "delete":
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(body) != 0 {
			writeError(w, http.StatusBadRequest, "body_not_allowed")
			return
		}
		if err := h.providers.DeleteProvider(r.Context(), id); err != nil {
			writeProviderError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, struct {
			Deleted bool `json:"deleted"`
		}{Deleted: true})
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func toWatermarkResponse(mark resource.RegionWatermark) watermarkResponse {
	return watermarkResponse{
		Region: mark.Region, MinUsable: mark.MinUsable, Usable: mark.Usable, Revision: mark.Revision,
	}
}

func toProviderResponse(provider resource.ProxyProvider) providerResponse {
	return providerResponse{
		ID: provider.ID, Name: provider.Name, Enabled: provider.Enabled,
		Priority: provider.Priority, Regions: append([]resource.NodeRegion(nil), provider.Regions...),
		HasCredential: provider.HasCredential, Revision: provider.Revision,
	}
}

func writeProviderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrResourceNotFound):
		writeError(w, http.StatusNotFound, "provider_not_found")
	case errors.Is(err, postgres.ErrResourceRevisionConflict):
		writeError(w, http.StatusConflict, "provider_revision_conflict")
	case errors.Is(err, postgres.ErrProviderConflict):
		writeError(w, http.StatusConflict, "provider_conflict")
	case errors.Is(err, postgres.ErrInvalidResource):
		writeError(w, http.StatusBadRequest, "invalid_provider")
	case errors.Is(err, postgres.ErrResourceStorage):
		writeError(w, http.StatusServiceUnavailable, "resource_storage_unavailable")
	case errors.Is(err, postgres.ErrResourceIntegrity):
		writeError(w, http.StatusInternalServerError, "resource_integrity")
	default:
		writeError(w, http.StatusBadRequest, "invalid_provider")
	}
}
