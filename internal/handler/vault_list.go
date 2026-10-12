package handler

import (
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"net/http"
)

// List handles GET /api/v1/admin/vault-connections/.
func (h *VaultHandler) List(w http.ResponseWriter, r *http.Request) {
	if !validateLegacyPage(w, r) {
		return
	}

	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	limit, offset := queryLimitOffset(r, 20)
	rows, err := h.queries.ListVaultConnectionsPage(r.Context(), sqlc.ListVaultConnectionsPageParams{
		QueryLimit: queryLimit32(r, 20), QueryOffset: queryOffset32(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list vault connections")
		return
	}
	out := make([]VaultConnectionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toVaultConnectionListResponse(row))
	}
	total, err := h.queries.CountVaultConnections(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to count vault connections")
		return
	}
	RespondJSON(w, http.StatusOK, struct {
		Items      []VaultConnectionResponse `json:"items"`
		Pagination paging.Metadata           `json:"pagination"`
	}{Items: out, Pagination: paging.Exact(total, limit, offset, len(out))})
}
