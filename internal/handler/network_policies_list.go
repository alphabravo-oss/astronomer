package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

// ListTemplates handles GET /api/v1/admin/network-policy-templates/.
func (h *NetworkPolicyHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	if !validateLegacyPage(w, r) {
		return
	}

	items, err := h.queries.ListNetworkPolicyTemplates(r.Context(), sqlc.ListNetworkPolicyTemplatesParams{
		Limit:  queryLimit32(r, 50),
		Offset: queryOffset32(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list network policy templates")
		return
	}
	total, _ := h.queries.CountNetworkPolicyTemplates(r.Context())
	resp := make([]NetworkPolicyTemplateResponse, 0, len(items))
	for _, t := range items {
		resp = append(resp, networkPolicyTemplateToResponse(t))
	}
	paging.Write(w, resp, paging.Exact(total, queryLimit(r, 50), queryOffset(r), len(resp)))
}
