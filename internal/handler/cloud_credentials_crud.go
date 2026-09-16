package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/cloudcreds"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
)

func (h *CloudCredentialHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, map[string]any{"items": cloudcreds.ListProviders()})
}

// --- Project-scoped CRUD ----------------------------------------------

// List handles GET /api/v1/projects/{project_id}/cloud-credentials/.
func (h *CloudCredentialHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	rows, err := h.queries.ListCloudCredentialsForProject(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cloud credentials")
		return
	}
	out := make([]CloudCredentialResponse, 0, len(rows))
	for _, row := range rows {
		resp, err := h.rowToResponse(r.Context(), row, false)
		if err != nil {
			// One row decode failure shouldn't fail the whole list; surface
			// a redacted placeholder so the UI still renders the rest.
			out = append(out, h.rowToErrorResponse(row, err))
			continue
		}
		out = append(out, resp)
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Get handles GET /api/v1/projects/{project_id}/cloud-credentials/{id}/.
func (h *CloudCredentialHandler) Get(w http.ResponseWriter, r *http.Request) {
	row, ok := h.loadCredentialForRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.rowToResponse(r.Context(), row, true)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InvalidBody, "Failed to decode credential")
		return
	}
	RespondJSON(w, http.StatusOK, resp)
}

// Create handles POST /api/v1/projects/{project_id}/cloud-credentials/.
func (h *CloudCredentialHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	project, err := h.queries.GetProjectByID(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	var req CloudCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if err := validateCredentialName(req.Name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, err.Error())
		return
	}
	if _, ok := cloudcreds.LookupProvider(req.Provider); !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidProvider, fmt.Sprintf("Unknown provider %q", req.Provider))
		return
	}
	// Reject sentinel values on create — there's nothing to preserve.
	for k, v := range req.Data {
		if s, isStr := v.(string); isStr && s == cloudcreds.SecretSentinel {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, fmt.Sprintf("Cannot use sentinel value on create for key %q", k))
			return
		}
	}
	if err := cloudcreds.Validate(req.Provider, req.Data); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryption key not configured; cannot store credentials")
		return
	}
	plain, err := cloudcreds.EncodeBlob(req.Data)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	ciphertext, err := h.encryptor.Encrypt(string(plain))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt credential")
		return
	}
	targetRefs, err := h.canonicaliseTargetRefs(r.Context(), projectID, req.TargetRefs, req.Name)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidTargetRefs, err.Error())
		return
	}
	refsJSON, err := json.Marshal(targetRefs)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode target refs")
		return
	}
	// Uniqueness (project_id, name) is enforced by the DB; we check
	// here so we can return a clean 409 rather than a generic 500.
	if _, err := h.queries.GetCloudCredentialByProjectAndName(r.Context(), sqlc.GetCloudCredentialByProjectAndNameParams{
		ProjectID: projectID,
		Name:      req.Name,
	}); err == nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A credential with that name already exists in this project")
		return
	}
	createParams := sqlc.CreateCloudCredentialParams{
		ProjectID:     projectID,
		Name:          req.Name,
		Provider:      strings.ToLower(req.Provider),
		Description:   req.Description,
		DataEncrypted: ciphertext,
		TargetRefs:    refsJSON,
		CreatedBy:     userIDFromRequest(r),
	}
	created, err := executeMutation(r, h.runTx,
		func(q CloudCredentialMutationTx) (sqlc.CloudCredential, error) {
			created, createErr := q.CreateCloudCredential(r.Context(), createParams)
			if createErr != nil {
				return sqlc.CloudCredential{}, createErr
			}
			if stageErr := h.stageMaterializationRefs(r.Context(), q, created, targetRefs, "apply"); stageErr != nil {
				return sqlc.CloudCredential{}, stageErr
			}
			return created, nil
		},
		func(created sqlc.CloudCredential) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cloud_credentials.created", resourceType: "cloud_credential",
				resourceID: created.ID.String(), resourceName: created.Name, status: http.StatusCreated,
				detail: map[string]any{"project_id": created.ProjectID.String(), "project_name": project.Name, "provider": created.Provider, "target_count": len(targetRefs)},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create credential")
		return
	}
	resp, err := h.rowToResponse(r.Context(), created, true)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InvalidBody, "Failed to render created credential")
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/", projectID.String(), created.ID.String()))
	RespondJSON(w, http.StatusCreated, resp)
}

// Update handles PUT /api/v1/projects/{project_id}/cloud-credentials/{id}/.
// Honors the SecretSentinel preserve-stored-value rule.
func (h *CloudCredentialHandler) Update(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.loadCredentialForRequest(w, r)
	if !ok {
		return
	}
	var req CloudCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	// Name + provider are immutable post-create — operators delete +
	// recreate to switch providers. This matches the Rancher UX and
	// keeps the materialization story simple (no provider-switch
	// midflight where the rendered Secret shape would change).
	if req.Name != "" && req.Name != existing.Name {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ImmutableName, "Credential name cannot be changed")
		return
	}
	if req.Provider != "" && !strings.EqualFold(req.Provider, existing.Provider) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ImmutableProvider, "Credential provider cannot be changed")
		return
	}
	// Decrypt existing for the merge step (sentinel-preserves-stored).
	priorBlob, err := h.decryptToMap(existing.DataEncrypted)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DecryptError, "Failed to decrypt stored credential")
		return
	}
	// Validate the incoming patch first (which uses raw map[string]any).
	if req.Data != nil {
		// We accept SecretSentinel for required keys at patch time —
		// that's the preserve-stored-value rule. So we run a relaxed
		// validation that only rejects unknown keys + non-string
		// values + non-sentinel empty strings.
		if err := validatePatchData(existing.Provider, req.Data); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
			return
		}
	}
	// Re-encode the merged blob.
	merged := priorBlob
	if req.Data != nil {
		patchStrings := make(map[string]string, len(req.Data))
		for k, v := range req.Data {
			s, _ := v.(string)
			patchStrings[k] = s
		}
		merged = cloudcreds.MergePatch(existing.Provider, priorBlob, patchStrings)
	}
	// Re-validate the FINAL merged blob (full-blob validation, no sentinel
	// allowance) — this is the safety net so a PUT can't strip a required
	// key down to empty.
	mergedAny := make(map[string]any, len(merged))
	for k, v := range merged {
		mergedAny[k] = v
	}
	if err := cloudcreds.Validate(existing.Provider, mergedAny); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryption key not configured; cannot store credentials")
		return
	}
	plain, err := cloudcreds.EncodeBlob(mergedAny)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode credential")
		return
	}
	ciphertext, err := h.encryptor.Encrypt(string(plain))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt credential")
		return
	}
	// Target refs: omitted in the request body → preserve stored.
	// Non-nil (even empty) → overwrite. JSON decoders make the
	// distinction tricky on slices since Go zero-values to nil; we
	// accept that nil means "preserve" and an explicit `[]` (also
	// nil after JSON decode in Go) means "preserve" too — operators
	// who want to drop all targets PATCH /targets/ separately or
	// send a structured "empty list" sentinel. For now: if the
	// request includes the field, we use it; we approximate by
	// "non-nil" (which Go gives us on a `[]` body if the JSON
	// decoder was instructed to differentiate; default encoding/json
	// hands back a non-nil empty slice — good enough).
	refsCanonical := decodeStoredTargetRefs(existing.TargetRefs)
	if req.TargetRefs != nil {
		canon, err := h.canonicaliseTargetRefs(r.Context(), existing.ProjectID, req.TargetRefs, existing.Name)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidTargetRefs, err.Error())
			return
		}
		refsCanonical = canon
	}
	refsJSON, err := json.Marshal(refsCanonical)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode target refs")
		return
	}
	description := existing.Description
	if req.Description != "" || (req.Data == nil && req.TargetRefs == nil) {
		// Empty description overwrite is intentional only when the
		// operator clearly meant a "full PUT" (no other fields touched).
		description = req.Description
	}
	updateParams := sqlc.UpdateCloudCredentialParams{
		ID:            existing.ID,
		Description:   description,
		DataEncrypted: ciphertext,
		TargetRefs:    refsJSON,
	}
	droppedRefs := diffTargetRefs(decodeStoredTargetRefs(existing.TargetRefs), refsCanonical)
	updated, err := executeMutation(r, h.runTx,
		func(q CloudCredentialMutationTx) (sqlc.CloudCredential, error) {
			updated, updateErr := q.UpdateCloudCredential(r.Context(), updateParams)
			if updateErr != nil {
				return sqlc.CloudCredential{}, updateErr
			}
			if stageErr := h.stageDeleteMaterializationRefs(r.Context(), q, updated.ID, droppedRefs); stageErr != nil {
				return sqlc.CloudCredential{}, stageErr
			}
			if orphanErr := q.DeleteOrphanCloudCredentialMaterializations(r.Context(), sqlc.DeleteOrphanCloudCredentialMaterializationsParams{
				CredentialID: updated.ID,
				TargetRefs:   refsJSON,
			}); orphanErr != nil {
				return sqlc.CloudCredential{}, orphanErr
			}
			if stageErr := h.stageMaterializationRefs(r.Context(), q, updated, refsCanonical, "apply"); stageErr != nil {
				return sqlc.CloudCredential{}, stageErr
			}
			return updated, nil
		},
		func(updated sqlc.CloudCredential) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cloud_credentials.updated", resourceType: "cloud_credential",
				resourceID: updated.ID.String(), resourceName: updated.Name, status: http.StatusOK,
				detail: map[string]any{"project_id": updated.ProjectID.String(), "provider": updated.Provider, "target_count": len(refsCanonical)},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update credential")
		return
	}
	resp, err := h.rowToResponse(r.Context(), updated, true)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InvalidBody, "Failed to render updated credential")
		return
	}
	RespondJSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /api/v1/projects/{project_id}/cloud-credentials/{id}/.
// Enqueues the in-cluster Secret deletion for every target_ref BEFORE
// purging the row so the worker still has the (cluster, namespace,
// secret_name) tuple in flight.
func (h *CloudCredentialHandler) Delete(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.loadCredentialForRequest(w, r)
	if !ok {
		return
	}
	stored := decodeStoredTargetRefs(existing.TargetRefs)
	_, err := executeMutation(r, h.runTx,
		func(q CloudCredentialMutationTx) (sqlc.CloudCredential, error) {
			if stageErr := h.stageDeleteMaterializationRefs(r.Context(), q, existing.ID, stored); stageErr != nil {
				return sqlc.CloudCredential{}, stageErr
			}
			return existing, q.DeleteCloudCredential(r.Context(), existing.ID)
		},
		func(deleted sqlc.CloudCredential) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cloud_credentials.deleted", resourceType: "cloud_credential",
				resourceID: deleted.ID.String(), resourceName: deleted.Name, status: http.StatusNoContent,
				detail: map[string]any{"project_id": deleted.ProjectID.String(), "provider": deleted.Provider, "target_count": len(stored)},
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete credential")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /api/v1/projects/{project_id}/cloud-credentials/{id}/test/.
// Decrypts the stored blob and forwards to the provider tester. The
// Astronomer server's network is what reaches AWS/GCP/Azure for these
// checks — member-cluster workloads have their own network constraints.
func (h *CloudCredentialHandler) Test(w http.ResponseWriter, r *http.Request) {
	row, ok := h.loadCredentialForRequest(w, r)
	if !ok {
		return
	}
	blob, err := h.decryptToMap(row.DataEncrypted)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DecryptError, "Failed to decrypt stored credential")
		return
	}
	if h.tester == nil {
		cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues(row.Provider, "unsupported")...).Inc()
		if err := h.requireTestAudit(r, row, "unsupported"); err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
			return
		}
		RespondJSON(w, http.StatusOK, CloudTestResult{OK: false, Message: "no test available (tester not configured)"})
		return
	}
	var (
		result CloudTestResult
		terr   error
	)
	switch strings.ToLower(row.Provider) {
	case "aws":
		result, terr = h.tester.TestAWS(r.Context(), blob)
	case "gcp":
		result, terr = h.tester.TestGCP(r.Context(), blob)
	case "azure":
		result, terr = h.tester.TestAzure(r.Context(), blob)
	case "digitalocean", "doks":
		doTester, supported := h.tester.(digitalOceanCloudTester)
		if !supported {
			cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues("digitalocean", "unsupported")...).Inc()
			if err := h.requireTestAudit(r, row, "unsupported"); err != nil {
				RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
				return
			}
			RespondJSON(w, http.StatusOK, CloudTestResult{OK: false, Message: "DigitalOcean credential testing is not configured"})
			return
		}
		result, terr = doTester.TestDigitalOcean(r.Context(), blob)
	case "generic":
		// Generic has no SDK to call; surface a clear "no-op" answer.
		cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues("generic", "unsupported")...).Inc()
		if err := h.requireTestAudit(r, row, "unsupported"); err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
			return
		}
		RespondJSON(w, http.StatusOK, CloudTestResult{OK: false, Message: "no test available for generic provider"})
		return
	default:
		cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues(row.Provider, "unsupported")...).Inc()
		if err := h.requireTestAudit(r, row, "unsupported"); err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
			return
		}
		RespondJSON(w, http.StatusOK, CloudTestResult{OK: false, Message: fmt.Sprintf("no test available for provider %q", row.Provider)})
		return
	}
	outcome := "failed"
	if terr != nil {
		result = CloudTestResult{OK: false, Message: redaction.String(terr.Error())}
	} else if result.OK {
		outcome = "ok"
	}
	cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues(strings.ToLower(row.Provider), outcome)...).Inc()
	if err := h.requireTestAudit(r, row, outcome); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, result)
}
