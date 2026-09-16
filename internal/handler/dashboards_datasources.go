package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/dashboards"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *DashboardHandler) AdminListDatasources(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListPrometheusDatasources(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]DatasourceResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, datasourceToResponse(row))
	}
	page, pagination := pageWindow(r, out)
	paging.Write(w, page, pagination)
}

// AdminCreateDatasource handles POST /api/v1/admin/prometheus-datasources/.
func (h *DashboardHandler) AdminCreateDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req DatasourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if err := validateDatasourceRequest(req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	encrypted, err := h.sealAuth(req)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	params := sqlc.CreatePrometheusDatasourceParams{
		Name:          req.Name,
		Url:           req.URL,
		AuthEncrypted: encrypted,
		TlsSkipVerify: req.TLSSkipVerify,
		Enabled:       enabled,
	}
	row, err := executeMutation(r, h.runTx,
		func(q DashboardMutationTx) (sqlc.PrometheusDatasource, error) {
			return q.CreatePrometheusDatasource(r.Context(), params)
		},
		func(row sqlc.PrometheusDatasource) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.prometheus_datasource.created", resourceType: "prometheus_datasource",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: datasourceAuditDetail(row),
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	w.Header().Set("Location", "/api/v1/admin/prometheus-datasources/"+row.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, datasourceToResponse(row))
}

// AdminUpdateDatasource handles PUT /api/v1/admin/prometheus-datasources/{id}/.
func (h *DashboardHandler) AdminUpdateDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid datasource ID")
		return
	}
	var req DatasourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if err := validateDatasourceRequest(req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	// Seal replacement credentials before opening a database transaction; no
	// cleartext secret is retained in state. Empty auth fields retain their PUT
	// compatibility meaning of "preserve existing ciphertext".
	replaceAuth := req.BasicAuthUser != "" || req.BasicAuthPass != "" || req.BearerToken != ""
	var replacementCiphertext string
	if replaceAuth {
		enc, err := h.sealAuth(req)
		if err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, err.Error())
			return
		}
		replacementCiphertext = enc
	}
	updateDatasource := func(q DashboardQuerier) (sqlc.PrometheusDatasource, error) {
		existing, getErr := q.GetPrometheusDatasourceByID(r.Context(), id)
		if getErr != nil {
			return sqlc.PrometheusDatasource{}, getErr
		}
		encrypted := existing.AuthEncrypted
		if replaceAuth {
			encrypted = replacementCiphertext
		}
		enabled := existing.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		return q.UpdatePrometheusDatasource(r.Context(), sqlc.UpdatePrometheusDatasourceParams{
			ID: id, Name: req.Name, Url: req.URL, AuthEncrypted: encrypted,
			TlsSkipVerify: req.TLSSkipVerify, Enabled: enabled,
		})
	}
	row, err := executeMutation(r, h.runTx,
		func(q DashboardMutationTx) (sqlc.PrometheusDatasource, error) { return updateDatasource(q) },
		func(row sqlc.PrometheusDatasource) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.prometheus_datasource.updated", resourceType: "prometheus_datasource",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
				detail: datasourceAuditDetail(row),
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Datasource not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, datasourceToResponse(row))
}

// AdminDeleteDatasource handles DELETE /api/v1/admin/prometheus-datasources/{id}/.
func (h *DashboardHandler) AdminDeleteDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid datasource ID")
		return
	}
	deleteDatasource := func(q DashboardQuerier) (sqlc.PrometheusDatasource, error) {
		row, getErr := q.GetPrometheusDatasourceByID(r.Context(), id)
		if getErr != nil {
			return sqlc.PrometheusDatasource{}, getErr
		}
		if deleteErr := q.DeletePrometheusDatasource(r.Context(), id); deleteErr != nil {
			return sqlc.PrometheusDatasource{}, deleteErr
		}
		return row, nil
	}
	_, err = executeMutation(r, h.runTx,
		func(q DashboardMutationTx) (sqlc.PrometheusDatasource, error) { return deleteDatasource(q) },
		func(row sqlc.PrometheusDatasource) mutationAuditEvent {
			return mutationAuditEvent{
				action: "admin.prometheus_datasource.deleted", resourceType: "prometheus_datasource",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusNoContent,
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Datasource not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminTestDatasource handles POST /api/v1/admin/prometheus-datasources/{id}/test/.
// Runs a single instant query against the upstream (`up{}`) — we don't
// care about the result body, just whether the round-trip succeeds and
// returns HTTP 2xx. A failure here typically points at network policy
// or auth misconfiguration.
func (h *DashboardHandler) AdminTestDatasource(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid datasource ID")
		return
	}
	row, err := h.queries.GetPrometheusDatasourceByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Datasource not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	ds, err := h.resolveDatasource(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, _, err = dashboards.EvalStat(ctx, h.cache, ds, `up`)
	if err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Datasource reachable"})
}

// ── Public render ─────────────────────────────────────────────────────

// RenderGlobal handles GET /api/v1/dashboards/global/.
