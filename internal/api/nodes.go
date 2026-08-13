package api

import (
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/market"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

type nodeResponse struct {
	ID                      resource.NodeID     `json:"id"`
	Name                    string              `json:"name"`
	Kind                    resource.NodeKind   `json:"kind"`
	Region                  resource.NodeRegion `json:"region"`
	EgressMode              resource.EgressMode `json:"egress_mode"`
	State                   resource.NodeState  `json:"state"`
	EgressRevision          int64               `json:"egress_revision"`
	AssignmentRevision      int64               `json:"assignment_revision"`
	AppID                   *int64              `json:"appid,omitempty"`
	Sides                   []nodeSideResponse  `json:"sides"`
	HasProxyCredential      bool                `json:"has_proxy_credential"`
	StickySessionValidUntil *time.Time          `json:"sticky_session_valid_until,omitempty"`
	ExitVerification        *nodeExitResponse   `json:"exit,omitempty"`
}

type nodeSideResponse struct {
	Platform resource.Platform `json:"platform"`
	Side     market.Side       `json:"side"`
}

type nodeExitResponse struct {
	Address    string    `json:"address"`
	VerifiedAt time.Time `json:"verified_at"`
	ValidUntil time.Time `json:"valid_until"`
}

type nodeCreateRequest struct {
	Name                    string              `json:"name"`
	Kind                    resource.NodeKind   `json:"kind"`
	Region                  resource.NodeRegion `json:"region"`
	EgressMode              resource.EgressMode `json:"egress_mode"`
	StickySessionValidUntil *time.Time          `json:"sticky_session_valid_until"`
	ProxyCredential         string              `json:"proxy_credential"`
}

type nodeConnectionReplaceRequest struct {
	ExpectedEgressRevision  int64               `json:"expected_egress_revision"`
	Kind                    resource.NodeKind   `json:"kind"`
	Region                  resource.NodeRegion `json:"region"`
	EgressMode              resource.EgressMode `json:"egress_mode"`
	StickySessionValidUntil *time.Time          `json:"sticky_session_valid_until"`
	ProxyCredential         string              `json:"proxy_credential"`
}

type nodeExitRequest struct {
	ExpectedEgressRevision int64     `json:"expected_egress_revision"`
	Address                string    `json:"address"`
	ValidUntil             time.Time `json:"valid_until"`
}

type nodeAssignGameRequest struct {
	ExpectedAssignmentRevision int64  `json:"expected_assignment_revision"`
	AppID                      *int64 `json:"appid"`
}

type nodeAssignSideRequest struct {
	ExpectedAssignmentRevision int64             `json:"expected_assignment_revision"`
	Platform                   resource.Platform `json:"platform"`
	Side                       *market.Side      `json:"side"`
}

func (h *Handler) serveNodes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/nodes" {
		if r.Method == http.MethodGet {
			nodes, err := h.nodes.ListNodes(r.Context())
			if err != nil {
				writeNodeError(w, err)
				return
			}
			out := make([]nodeResponse, 0, len(nodes))
			for _, node := range nodes {
				out = append(out, toNodeResponse(node))
			}
			writeAccountJSON(w, http.StatusOK, out)
			return
		}
		if r.Method == http.MethodPost {
			var input nodeCreateRequest
			if !decodeJSON(w, r, &input) {
				return
			}
			node, err := h.nodes.CreateNode(r.Context(), input.Name, nodeConnectionInput(input.Kind, input.Region, input.EgressMode, input.StickySessionValidUntil, input.ProxyCredential))
			if err != nil {
				writeNodeError(w, err)
				return
			}
			writeAccountJSON(w, http.StatusCreated, toNodeResponse(node))
			return
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/nodes/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || value < 1 {
		writeError(w, http.StatusBadRequest, "invalid_node_id")
		return
	}
	id := resource.NodeID(value)
	if len(parts) == 1 && r.Method == http.MethodGet {
		node, found, err := h.nodes.Node(r.Context(), id)
		if err != nil {
			writeNodeError(w, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "node_not_found")
			return
		}
		writeAccountJSON(w, http.StatusOK, toNodeResponse(node))
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch parts[1] {
	case "connection":
		var input nodeConnectionReplaceRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		node, err := h.nodes.ReplaceNodeConnection(r.Context(), id, input.ExpectedEgressRevision, nodeConnectionInput(input.Kind, input.Region, input.EgressMode, input.StickySessionValidUntil, input.ProxyCredential))
		if err != nil {
			writeNodeError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toNodeResponse(node))
	case "exit":
		var input nodeExitRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		address, parseErr := netip.ParseAddr(input.Address)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_exit_address")
			return
		}
		node, err := h.nodes.RecordNodeExit(r.Context(), id, input.ExpectedEgressRevision, address, input.ValidUntil)
		if err != nil {
			writeNodeError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toNodeResponse(node))
	case "assign-game":
		var input nodeAssignGameRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		var appID int64
		if input.AppID != nil {
			appID = *input.AppID
		}
		node, err := h.nodes.AssignNodeGame(r.Context(), id, input.ExpectedAssignmentRevision, appID)
		if err != nil {
			writeNodeError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toNodeResponse(node))
	case "assign-side":
		var input nodeAssignSideRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		var side market.Side
		if input.Side != nil {
			side = *input.Side
		}
		node, err := h.nodes.AssignNodeSide(r.Context(), id, input.ExpectedAssignmentRevision, input.Platform, side)
		if err != nil {
			writeNodeError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toNodeResponse(node))
	case "delete":
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(body) != 0 {
			writeError(w, http.StatusBadRequest, "body_not_allowed")
			return
		}
		if err := h.nodes.DeleteNode(r.Context(), id); err != nil {
			writeNodeError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, struct {
			Deleted bool `json:"deleted"`
		}{Deleted: true})
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func nodeConnectionInput(kind resource.NodeKind, region resource.NodeRegion, mode resource.EgressMode, sticky *time.Time, credential string) resource.NodeConnectionInput {
	input := resource.NodeConnectionInput{Kind: kind, Region: region, EgressMode: mode, StickySessionValidUntil: sticky}
	if credential != "" {
		input.ProxyCredential = []byte(credential)
	}
	return input
}

func toNodeResponse(node resource.AccessNode) nodeResponse {
	out := nodeResponse{
		ID: node.ID, Name: node.Name, Kind: node.Kind, Region: node.Region,
		EgressMode: node.EgressMode, State: node.State, EgressRevision: node.EgressRevision,
		AssignmentRevision: node.AssignmentRevision, HasProxyCredential: node.HasProxyCredential,
		StickySessionValidUntil: node.StickySessionValidUntil,
		Sides:                   make([]nodeSideResponse, 0, len(node.Sides)),
	}
	if node.AppID > 0 {
		appID := node.AppID
		out.AppID = &appID
	}
	for _, assignment := range node.Sides {
		out.Sides = append(out.Sides, nodeSideResponse{Platform: assignment.Platform, Side: assignment.Side})
	}
	if node.ExitVerification != nil {
		out.ExitVerification = &nodeExitResponse{
			Address:    node.ExitVerification.Address.String(),
			VerifiedAt: node.ExitVerification.VerifiedAt,
			ValidUntil: node.ExitVerification.ValidUntil,
		}
	}
	return out
}

func writeNodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, resource.ErrResourceOccupied):
		writeError(w, http.StatusConflict, "node_occupied")
	case errors.Is(err, postgres.ErrResourceDependency):
		writeError(w, http.StatusConflict, "node_in_use")
	case errors.Is(err, postgres.ErrResourceNotFound):
		writeError(w, http.StatusNotFound, "node_not_found")
	case errors.Is(err, postgres.ErrResourceRevisionConflict):
		writeError(w, http.StatusConflict, "node_revision_conflict")
	case errors.Is(err, postgres.ErrNodeAssignmentConflict):
		writeError(w, http.StatusConflict, "node_assignment_conflict")
	case errors.Is(err, postgres.ErrNodeNameConflict):
		writeError(w, http.StatusConflict, "node_name_conflict")
	case errors.Is(err, postgres.ErrCombinationIncompatible):
		writeError(w, http.StatusConflict, "combination_incompatible")
	case errors.Is(err, postgres.ErrInvalidResource):
		writeError(w, http.StatusBadRequest, "invalid_node")
	case errors.Is(err, postgres.ErrResourceStorage):
		writeError(w, http.StatusServiceUnavailable, "resource_storage_unavailable")
	case errors.Is(err, postgres.ErrResourceIntegrity):
		writeError(w, http.StatusInternalServerError, "resource_integrity")
	default:
		writeError(w, http.StatusBadRequest, "invalid_node")
	}
}
