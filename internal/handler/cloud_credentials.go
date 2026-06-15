// Package handler — migration 053: cloud credentials (Rancher pattern).
//
// Operators store cloud secrets (AWS / GCP / Azure / Generic) once at the
// project level, then reference them from member-cluster workloads. This
// file owns the REST surface:
//
//   GET    /api/v1/projects/{project_id}/cloud-credentials/
//   POST   /api/v1/projects/{project_id}/cloud-credentials/
//   GET    /api/v1/projects/{project_id}/cloud-credentials/{id}/
//   PUT    /api/v1/projects/{project_id}/cloud-credentials/{id}/
//   DELETE /api/v1/projects/{project_id}/cloud-credentials/{id}/
//   POST   /api/v1/projects/{project_id}/cloud-credentials/{id}/test/
//   GET    /api/v1/cloud-credentials/providers/   (public — UI form-builder fuel)
//
// Encryption:
//   - The "data" blob (map[string]string) is JSON-encoded and Fernet-
//     encrypted at rest using the shared auth.Encryptor.
//   - GETs decrypt + redact each provider-flagged "SecretKey" with the
//     SecretSentinel constant; the PUT path treats sentinel values as
//     "preserve the stored value" so a natural GET → edit → PUT loop
//     doesn't blank credentials.
//
// Materialization:
//   - Every (cluster, namespace) entry in target_refs is upserted into
//     cloud_credential_materializations so the periodic drift sweep
//     can reconcile without re-parsing JSONB.
//   - The handler enqueues one materialize task per target_ref on
//     every write; the worker fans out asynchronously and stamps
//     status/last_applied_at/last_error on the row.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/cloudcreds"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// CloudCredentialQuerier is the slice of *sqlc.Queries the handler needs.
// Defined as an interface so tests can pass narrow fakes; the production
// wiring passes *sqlc.Queries (which satisfies this surface).
type CloudCredentialQuerier interface {
	// Projects + clusters for FK existence checks.
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	// Cloud-credentials CRUD.
	ListCloudCredentialsForProject(ctx context.Context, projectID uuid.UUID) ([]sqlc.CloudCredential, error)
	GetCloudCredentialByID(ctx context.Context, id uuid.UUID) (sqlc.CloudCredential, error)
	GetCloudCredentialByProjectAndName(ctx context.Context, arg sqlc.GetCloudCredentialByProjectAndNameParams) (sqlc.CloudCredential, error)
	CreateCloudCredential(ctx context.Context, arg sqlc.CreateCloudCredentialParams) (sqlc.CloudCredential, error)
	UpdateCloudCredential(ctx context.Context, arg sqlc.UpdateCloudCredentialParams) (sqlc.CloudCredential, error)
	DeleteCloudCredential(ctx context.Context, id uuid.UUID) error
	// Materializations.
	ListCloudCredentialMaterializations(ctx context.Context, credentialID uuid.UUID) ([]sqlc.CloudCredentialMaterialization, error)
	UpsertCloudCredentialMaterialization(ctx context.Context, arg sqlc.UpsertCloudCredentialMaterializationParams) (sqlc.CloudCredentialMaterialization, error)
	DeleteOrphanCloudCredentialMaterializations(ctx context.Context, arg sqlc.DeleteOrphanCloudCredentialMaterializationsParams) error
}

type cloudCredentialMaterializationTaskOutboxQuerier interface {
	UpsertCloudCredentialMaterializationWithTaskOutbox(ctx context.Context, arg sqlc.UpsertCloudCredentialMaterializationWithTaskOutboxParams) (sqlc.CloudCredentialMaterialization, error)
	DeleteCloudCredentialMaterializationWithTaskOutbox(ctx context.Context, arg sqlc.DeleteCloudCredentialMaterializationWithTaskOutboxParams) error
}

// CloudCredentialEnqueuer is the asynq surface the handler uses to fire
// materialize tasks. *asynq.Client satisfies this; tests pass a stub.
// Nil-safe — when unwired, writes still succeed and the periodic drift
// sweep eventually converges.
type CloudCredentialEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// CloudTester is the provider-test surface the /test/ endpoint dials.
// Each provider's "is this credential valid?" SDK call is wrapped behind
// this small interface so unit tests can swap in fakes without bringing
// up the real AWS/GCP/Azure SDKs. The default implementation is in
// cloud_credentials_test_endpoint.go.
type CloudTester interface {
	TestAWS(ctx context.Context, blob map[string]string) (CloudTestResult, error)
	TestGCP(ctx context.Context, blob map[string]string) (CloudTestResult, error)
	TestAzure(ctx context.Context, blob map[string]string) (CloudTestResult, error)
}

// CloudTestResult is the wire shape for the test endpoint and the
// outcome metric. OK=true means the SDK call succeeded; Message is a
// human-readable description ("authenticated as arn:aws:iam::…") or an
// error reason.
type CloudTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// CloudCredentialHandler owns /api/v1/projects/{project_id}/cloud-credentials/*.
type CloudCredentialHandler struct {
	queries    CloudCredentialQuerier
	auditor    any // auditWriterV1 surface — recordAudit type-asserts internally
	encryptor  *auth.Encryptor
	enqueuer   CloudCredentialEnqueuer
	taskOutbox tasks.TaskOutboxWriter
	tester     CloudTester
}

// NewCloudCredentialHandler wires the handler. The encryptor is required
// for any write path that stores cleartext; without it POST/PUT/test
// return 503 not_configured. Materializer + auditor are optional.
func NewCloudCredentialHandler(queries CloudCredentialQuerier) *CloudCredentialHandler {
	return &CloudCredentialHandler{queries: queries}
}

// SetAuditor wires the audit writer used to record cloud_credentials.*
// events. The argument is `any` because recordAudit's type assertion to
// the auditWriterV1 surface is internal to the audit_helpers code;
// production wires *sqlc.Queries, tests pass narrow fakes. Best-effort;
// failures inside recordAudit don't fail the request.
func (h *CloudCredentialHandler) SetAuditor(a any) {
	if h == nil {
		return
	}
	h.auditor = a
}

// SetEncryptor wires the Fernet encryptor. The handler 503s on POST/PUT
// when encryptor is nil so an operator can't accidentally store creds
// in plaintext.
func (h *CloudCredentialHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// SetEnqueuer wires the asynq client used to enqueue materialize tasks.
// Nil-safe — the periodic drift sweep is the safety net.
func (h *CloudCredentialHandler) SetEnqueuer(q CloudCredentialEnqueuer) {
	if h == nil {
		return
	}
	h.enqueuer = q
}

// SetTaskOutbox wires the durable task outbox used before direct Redis enqueue.
// Nil-safe — the periodic drift sweep remains the safety net when unwired.
func (h *CloudCredentialHandler) SetTaskOutbox(q tasks.TaskOutboxWriter) {
	if h == nil {
		return
	}
	h.taskOutbox = q
}

// SetTester wires the provider-validity tester used by the /test/
// endpoint. Nil → /test/ returns 503 not_configured for AWS/GCP/Azure
// and "no test available" for Generic.
func (h *CloudCredentialHandler) SetTester(t CloudTester) {
	if h == nil {
		return
	}
	h.tester = t
}

// --- Wire DTOs ----------------------------------------------------------

// TargetRef is one (cluster, namespace, secret_name) materialization
// target on a credential.
type TargetRef struct {
	ClusterID  uuid.UUID `json:"cluster_id"`
	Namespace  string    `json:"namespace"`
	SecretName string    `json:"secret_name"`
}

// CloudCredentialResponse is the wire shape on every GET / List / write
// echo. Data values listed in the provider's SecretKeys are redacted.
type CloudCredentialResponse struct {
	ID               uuid.UUID               `json:"id"`
	ProjectID        uuid.UUID               `json:"project_id"`
	Name             string                  `json:"name"`
	Provider         string                  `json:"provider"`
	Description      string                  `json:"description"`
	Data             map[string]string       `json:"data"`
	TargetRefs       []TargetRef             `json:"target_refs"`
	CreatedAt        string                  `json:"created_at"`
	UpdatedAt        string                  `json:"updated_at"`
	Materializations []MaterializationStatus `json:"materializations,omitempty"`
}

// MaterializationStatus is the per-(cluster, namespace) bookkeeping row
// the UI surfaces under the credential row.
type MaterializationStatus struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	Namespace     string    `json:"namespace"`
	SecretName    string    `json:"secret_name"`
	Status        string    `json:"status"`
	LastAppliedAt string    `json:"last_applied_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

// CloudCredentialRequest is the POST / PUT body. On POST every
// required key (per the provider spec) must be present and non-empty;
// PUT accepts a sentinel value for any secret key to preserve the
// stored value.
type CloudCredentialRequest struct {
	Name        string         `json:"name"`
	Provider    string         `json:"provider"`
	Description string         `json:"description"`
	Data        map[string]any `json:"data"`
	TargetRefs  []TargetRef    `json:"target_refs"`
}

// --- Metrics ------------------------------------------------------------

// cloudCredentialTestsTotal counts every /test/ endpoint invocation by
// provider + outcome (ok / failed / unsupported).
var cloudCredentialTestsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Name:      "cloud_credentials_test_total",
		Help:      "Cloud credentials /test/ endpoint outcomes by provider.",
	},
	observability.MetricLabels("provider", "outcome"),
)

func init() {
	prometheus.MustRegister(cloudCredentialTestsTotal)
}

// --- Validation helpers ------------------------------------------------

// nameRE is the strict allowed-character set for cloud_credentials.name.
// We constrain to RFC-1123 label-friendly characters so the default
// secret_name (= "astronomer-cred-<name>") is always a valid k8s
// resource name.
var nameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// validateCredentialName guards every write so a creative operator can't
// punctuate their way into a non-RFC-1123 secret name downstream.
func validateCredentialName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("name must be at most 64 characters")
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("name must match %s", nameRE.String())
	}
	return nil
}

// sanitiseSecretName converts a credential name into an RFC-1123
// k8s-resource-name-friendly default. Operators may override via the
// per-target_ref secret_name field.
func sanitiseSecretName(raw string) string {
	out := strings.ToLower(strings.TrimSpace(raw))
	out = nonRFC1123.ReplaceAllString(out, "-")
	out = multipleHyphens.ReplaceAllString(out, "-")
	out = strings.Trim(out, "-")
	if len(out) > 253 {
		out = out[:253]
	}
	return out
}

var (
	nonRFC1123      = regexp.MustCompile(`[^a-z0-9-]`)
	multipleHyphens = regexp.MustCompile(`-+`)
)

// defaultSecretName picks the in-cluster Secret name when an operator
// didn't override it on a target_ref. Pattern matches Rancher's
// "cred-<name>" convention with the "astronomer-cred-" prefix so the
// origin of the Secret is obvious from `kubectl get secrets`.
func defaultSecretName(credName string) string {
	base := sanitiseSecretName(credName)
	if base == "" {
		base = "credential"
	}
	return sanitiseSecretName("astronomer-cred-" + base)
}

// --- Public list of providers ------------------------------------------

// ListProviders handles GET /api/v1/cloud-credentials/providers/.
// Returns the registry as-is so a UI form-builder can render the wizard
// without any client-side knowledge of the available providers.
func (h *CloudCredentialHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, map[string]any{"items": cloudcreds.ListProviders()})
}

// --- Project-scoped CRUD ----------------------------------------------

// List handles GET /api/v1/projects/{project_id}/cloud-credentials/.
func (h *CloudCredentialHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.parseProjectID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Project not found")
		return
	}
	rows, err := h.queries.ListCloudCredentialsForProject(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list cloud credentials")
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
		RespondRequestError(w, r, http.StatusInternalServerError, "decode_error", "Failed to decode credential")
		return
	}
	RespondJSON(w, http.StatusOK, resp)
}

// Create handles POST /api/v1/projects/{project_id}/cloud-credentials/.
func (h *CloudCredentialHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.parseProjectID(w, r)
	if !ok {
		return
	}
	project, err := h.queries.GetProjectByID(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Project not found")
		return
	}
	var req CloudCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if err := validateCredentialName(req.Name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_name", err.Error())
		return
	}
	if _, ok := cloudcreds.LookupProvider(req.Provider); !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_provider", fmt.Sprintf("Unknown provider %q", req.Provider))
		return
	}
	// Reject sentinel values on create — there's nothing to preserve.
	for k, v := range req.Data {
		if s, isStr := v.(string); isStr && s == cloudcreds.SecretSentinel {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_data", fmt.Sprintf("Cannot use sentinel value on create for key %q", k))
			return
		}
	}
	if err := cloudcreds.Validate(req.Provider, req.Data); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_data", err.Error())
		return
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Encryption key not configured; cannot store credentials")
		return
	}
	plain, err := cloudcreds.EncodeBlob(req.Data)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_data", err.Error())
		return
	}
	ciphertext, err := h.encryptor.Encrypt(string(plain))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt credential")
		return
	}
	targetRefs, err := h.canonicaliseTargetRefs(r.Context(), req.TargetRefs, req.Name)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_target_refs", err.Error())
		return
	}
	refsJSON, err := json.Marshal(targetRefs)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "encode_error", "Failed to encode target refs")
		return
	}
	// Uniqueness (project_id, name) is enforced by the DB; we check
	// here so we can return a clean 409 rather than a generic 500.
	if _, err := h.queries.GetCloudCredentialByProjectAndName(r.Context(), sqlc.GetCloudCredentialByProjectAndNameParams{
		ProjectID: projectID,
		Name:      req.Name,
	}); err == nil {
		RespondRequestError(w, r, http.StatusConflict, "name_taken", "A credential with that name already exists in this project")
		return
	}
	created, err := h.queries.CreateCloudCredential(r.Context(), sqlc.CreateCloudCredentialParams{
		ProjectID:     projectID,
		Name:          req.Name,
		Provider:      strings.ToLower(req.Provider),
		Description:   req.Description,
		DataEncrypted: ciphertext,
		TargetRefs:    refsJSON,
		CreatedBy:     userIDFromRequest(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "create_error", "Failed to create credential")
		return
	}
	h.materializeCredentialRefs(r.Context(), created, targetRefs, "apply")
	h.audit(r, "cloud_credentials.created", created, project.Name, map[string]any{
		"provider":     created.Provider,
		"target_count": len(targetRefs),
	})
	resp, err := h.rowToResponse(r.Context(), created, true)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "decode_error", "Failed to render created credential")
		return
	}
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
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	// Name + provider are immutable post-create — operators delete +
	// recreate to switch providers. This matches the Rancher UX and
	// keeps the materialization story simple (no provider-switch
	// midflight where the rendered Secret shape would change).
	if req.Name != "" && req.Name != existing.Name {
		RespondRequestError(w, r, http.StatusBadRequest, "immutable_name", "Credential name cannot be changed")
		return
	}
	if req.Provider != "" && !strings.EqualFold(req.Provider, existing.Provider) {
		RespondRequestError(w, r, http.StatusBadRequest, "immutable_provider", "Credential provider cannot be changed")
		return
	}
	// Decrypt existing for the merge step (sentinel-preserves-stored).
	priorBlob, err := h.decryptToMap(existing.DataEncrypted)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "decrypt_error", "Failed to decrypt stored credential")
		return
	}
	// Validate the incoming patch first (which uses raw map[string]any).
	if req.Data != nil {
		// We accept SecretSentinel for required keys at patch time —
		// that's the preserve-stored-value rule. So we run a relaxed
		// validation that only rejects unknown keys + non-string
		// values + non-sentinel empty strings.
		if err := validatePatchData(existing.Provider, req.Data); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_data", err.Error())
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
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_data", err.Error())
		return
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Encryption key not configured; cannot store credentials")
		return
	}
	plain, err := cloudcreds.EncodeBlob(mergedAny)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "encode_error", "Failed to encode credential")
		return
	}
	ciphertext, err := h.encryptor.Encrypt(string(plain))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt credential")
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
		canon, err := h.canonicaliseTargetRefs(r.Context(), req.TargetRefs, existing.Name)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_target_refs", err.Error())
			return
		}
		refsCanonical = canon
	}
	refsJSON, err := json.Marshal(refsCanonical)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "encode_error", "Failed to encode target refs")
		return
	}
	description := existing.Description
	if req.Description != "" || (req.Data == nil && req.TargetRefs == nil) {
		// Empty description overwrite is intentional only when the
		// operator clearly meant a "full PUT" (no other fields touched).
		description = req.Description
	}
	updated, err := h.queries.UpdateCloudCredential(r.Context(), sqlc.UpdateCloudCredentialParams{
		ID:            existing.ID,
		Description:   description,
		DataEncrypted: ciphertext,
		TargetRefs:    refsJSON,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "update_error", "Failed to update credential")
		return
	}
	// On target_ref shrink: enqueue Secret deletion for the dropped pairs
	// so we don't strand a Secret in a cluster operators have un-targeted.
	droppedRefs := diffTargetRefs(decodeStoredTargetRefs(existing.TargetRefs), refsCanonical)
	h.deleteMaterializationRefs(r.Context(), updated.ID, droppedRefs)
	_ = h.queries.DeleteOrphanCloudCredentialMaterializations(r.Context(), sqlc.DeleteOrphanCloudCredentialMaterializationsParams{
		CredentialID: updated.ID,
		TargetRefs:   refsJSON,
	})
	h.materializeCredentialRefs(r.Context(), updated, refsCanonical, "apply")
	h.audit(r, "cloud_credentials.updated", updated, "", map[string]any{
		"provider":     updated.Provider,
		"target_count": len(refsCanonical),
	})
	resp, err := h.rowToResponse(r.Context(), updated, true)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "decode_error", "Failed to render updated credential")
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
	h.deleteMaterializationRefs(r.Context(), existing.ID, stored)
	if err := h.queries.DeleteCloudCredential(r.Context(), existing.ID); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "delete_error", "Failed to delete credential")
		return
	}
	h.audit(r, "cloud_credentials.deleted", existing, "", map[string]any{
		"provider":     existing.Provider,
		"target_count": len(stored),
	})
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
		RespondRequestError(w, r, http.StatusInternalServerError, "decrypt_error", "Failed to decrypt stored credential")
		return
	}
	if h.tester == nil {
		cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues(row.Provider, "unsupported")...).Inc()
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
	case "generic":
		// Generic has no SDK to call; surface a clear "no-op" answer.
		cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues("generic", "unsupported")...).Inc()
		h.audit(r, "cloud_credentials.test", row, "", map[string]any{"provider": "generic", "outcome": "unsupported"})
		RespondJSON(w, http.StatusOK, CloudTestResult{OK: false, Message: "no test available for generic provider"})
		return
	default:
		cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues(row.Provider, "unsupported")...).Inc()
		RespondJSON(w, http.StatusOK, CloudTestResult{OK: false, Message: fmt.Sprintf("no test available for provider %q", row.Provider)})
		return
	}
	outcome := "failed"
	if terr != nil {
		result = CloudTestResult{OK: false, Message: terr.Error()}
	} else if result.OK {
		outcome = "ok"
	}
	cloudCredentialTestsTotal.WithLabelValues(observability.MetricValues(strings.ToLower(row.Provider), outcome)...).Inc()
	h.audit(r, "cloud_credentials.test", row, "", map[string]any{"provider": row.Provider, "outcome": outcome})
	RespondJSON(w, http.StatusOK, result)
}

// --- Internal helpers --------------------------------------------------

// parseProjectID centralises the {project_id} param parse with a 400
// on a bad shape.
func (h *CloudCredentialHandler) parseProjectID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "project_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid project ID")
		return uuid.Nil, false
	}
	return id, true
}

// loadCredentialForRequest parses {project_id} + {id}, fetches the row,
// and verifies the credential belongs to the project. Returns the row +
// ok=true on success. On any failure it has already written a response.
func (h *CloudCredentialHandler) loadCredentialForRequest(w http.ResponseWriter, r *http.Request) (sqlc.CloudCredential, bool) {
	projectID, ok := h.parseProjectID(w, r)
	if !ok {
		return sqlc.CloudCredential{}, false
	}
	credentialID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid credential ID")
		return sqlc.CloudCredential{}, false
	}
	row, err := h.queries.GetCloudCredentialByID(r.Context(), credentialID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Credential not found")
		return sqlc.CloudCredential{}, false
	}
	if row.ProjectID != projectID {
		// Don't leak that the credential exists under a different project.
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Credential not found")
		return sqlc.CloudCredential{}, false
	}
	return row, true
}

// rowToResponse turns a DB row into the wire DTO, decrypting + redacting
// the data blob. includeMaterializations=true means "fetch + attach the
// per-(cluster, namespace) status rows" (only on single-row GET).
func (h *CloudCredentialHandler) rowToResponse(ctx context.Context, row sqlc.CloudCredential, includeMaterializations bool) (CloudCredentialResponse, error) {
	blob, err := h.decryptToMap(row.DataEncrypted)
	if err != nil {
		return CloudCredentialResponse{}, err
	}
	resp := CloudCredentialResponse{
		ID:          row.ID,
		ProjectID:   row.ProjectID,
		Name:        row.Name,
		Provider:    row.Provider,
		Description: row.Description,
		Data:        cloudcreds.RedactSecrets(row.Provider, blob),
		TargetRefs:  decodeStoredTargetRefs(row.TargetRefs),
		CreatedAt:   row.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if includeMaterializations {
		mats, err := h.queries.ListCloudCredentialMaterializations(ctx, row.ID)
		if err == nil {
			out := make([]MaterializationStatus, 0, len(mats))
			for _, m := range mats {
				ms := MaterializationStatus{
					ClusterID:  m.ClusterID,
					Namespace:  m.Namespace,
					SecretName: m.SecretName,
					Status:     m.Status,
					LastError:  m.LastError,
				}
				if m.LastAppliedAt.Valid {
					ms.LastAppliedAt = m.LastAppliedAt.Time.Format("2006-01-02T15:04:05Z07:00")
				}
				out = append(out, ms)
			}
			resp.Materializations = out
		}
	}
	return resp, nil
}

// rowToErrorResponse is the fallback shape when a single row can't be
// decoded (e.g. an encryption-key rotation that left an old row under a
// dropped key). We surface a clearly-failed row instead of failing the
// whole list call.
func (h *CloudCredentialHandler) rowToErrorResponse(row sqlc.CloudCredential, err error) CloudCredentialResponse {
	return CloudCredentialResponse{
		ID:          row.ID,
		ProjectID:   row.ProjectID,
		Name:        row.Name,
		Provider:    row.Provider,
		Description: fmt.Sprintf("(decode error: %s)", err.Error()),
		Data:        map[string]string{},
		TargetRefs:  decodeStoredTargetRefs(row.TargetRefs),
	}
}

// decryptToMap is the inverse of (encode → encrypt). Returns an empty
// map for an empty ciphertext so legacy / migrated rows decode cleanly.
func (h *CloudCredentialHandler) decryptToMap(ciphertext string) (map[string]string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return map[string]string{}, nil
	}
	if h.encryptor == nil {
		return nil, errors.New("encryptor not configured")
	}
	plain, err := h.encryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return cloudcreds.DecodeBlob([]byte(plain))
}

// canonicaliseTargetRefs validates the incoming target_refs slice and
// fills in a default secret_name for any entry that omitted one.
// Verifies each cluster_id exists; rejects bad UUIDs / empty namespaces.
func (h *CloudCredentialHandler) canonicaliseTargetRefs(ctx context.Context, in []TargetRef, credName string) ([]TargetRef, error) {
	if len(in) == 0 {
		return []TargetRef{}, nil
	}
	defaultName := defaultSecretName(credName)
	out := make([]TargetRef, 0, len(in))
	seen := map[string]struct{}{}
	for _, ref := range in {
		if ref.ClusterID == uuid.Nil {
			return nil, fmt.Errorf("target_ref.cluster_id must be set")
		}
		ns := strings.TrimSpace(ref.Namespace)
		if ns == "" {
			return nil, fmt.Errorf("target_ref.namespace must be set")
		}
		secret := strings.TrimSpace(ref.SecretName)
		if secret == "" {
			secret = defaultName
		} else {
			secret = sanitiseSecretName(secret)
			if secret == "" {
				return nil, fmt.Errorf("target_ref.secret_name produces an empty RFC 1123 name")
			}
		}
		// Verify the cluster exists. 404 here surfaces the operator-
		// recoverable failure cleanly instead of letting the worker
		// queue a doomed task.
		if _, err := h.queries.GetClusterByID(ctx, ref.ClusterID); err != nil {
			return nil, fmt.Errorf("target_ref.cluster_id %q not found", ref.ClusterID.String())
		}
		key := ref.ClusterID.String() + "|" + ns
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("duplicate target_ref for cluster %s namespace %q", ref.ClusterID.String(), ns)
		}
		seen[key] = struct{}{}
		out = append(out, TargetRef{
			ClusterID:  ref.ClusterID,
			Namespace:  ns,
			SecretName: secret,
		})
	}
	return out, nil
}

func (h *CloudCredentialHandler) materializeCredentialRefs(ctx context.Context, cred sqlc.CloudCredential, refs []TargetRef, op string) {
	for _, ref := range refs {
		if h.upsertMaterializationWithTaskOutbox(ctx, cred, ref, op) {
			continue
		}
		_, _ = h.queries.UpsertCloudCredentialMaterialization(ctx, sqlc.UpsertCloudCredentialMaterializationParams{
			CredentialID: cred.ID,
			ClusterID:    ref.ClusterID,
			Namespace:    ref.Namespace,
			SecretName:   ref.SecretName,
		})
		h.enqueueMaterialize(ctx, cred, []TargetRef{ref}, op)
	}
}

func (h *CloudCredentialHandler) deleteMaterializationRefs(ctx context.Context, credentialID uuid.UUID, refs []TargetRef) {
	for _, ref := range refs {
		if h.deleteMaterializationWithTaskOutbox(ctx, credentialID, ref) {
			continue
		}
		h.enqueueDelete(ctx, ref)
	}
}

func (h *CloudCredentialHandler) upsertMaterializationWithTaskOutbox(ctx context.Context, cred sqlc.CloudCredential, ref TargetRef, op string) bool {
	atomicQ, ok := h.queries.(cloudCredentialMaterializationTaskOutboxQuerier)
	if !ok || h.taskOutbox == nil {
		return false
	}
	task, err := tasks.NewCloudCredentialMaterializeTask(tasks.CloudCredentialMaterializePayload{
		CredentialID: cred.ID.String(),
		ClusterID:    ref.ClusterID.String(),
		Namespace:    ref.Namespace,
		SecretName:   ref.SecretName,
		Op:           op,
	})
	if err != nil {
		return false
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	_, err = atomicQ.UpsertCloudCredentialMaterializationWithTaskOutbox(ctx, sqlc.UpsertCloudCredentialMaterializationWithTaskOutboxParams{
		CredentialID:        cred.ID,
		ClusterID:           ref.ClusterID,
		Namespace:           ref.Namespace,
		SecretName:          ref.SecretName,
		DedupeKey:           pgtype.Text{String: cloudCredentialMaterializeDedupeKey(cred.ID, ref, op), Valid: true},
		TaskType:            task.Type(),
		Payload:             task.Payload(),
		QueueName:           "default",
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
		NextAttemptAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	return err == nil
}

func (h *CloudCredentialHandler) deleteMaterializationWithTaskOutbox(ctx context.Context, credentialID uuid.UUID, ref TargetRef) bool {
	atomicQ, ok := h.queries.(cloudCredentialMaterializationTaskOutboxQuerier)
	if !ok || h.taskOutbox == nil {
		return false
	}
	task, err := tasks.NewCloudCredentialMaterializeTask(tasks.CloudCredentialMaterializePayload{
		CredentialID: uuid.Nil.String(),
		ClusterID:    ref.ClusterID.String(),
		Namespace:    ref.Namespace,
		SecretName:   ref.SecretName,
		Op:           "delete",
	})
	if err != nil {
		return false
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	err = atomicQ.DeleteCloudCredentialMaterializationWithTaskOutbox(ctx, sqlc.DeleteCloudCredentialMaterializationWithTaskOutboxParams{
		CredentialID:        credentialID,
		ClusterID:           ref.ClusterID,
		Namespace:           ref.Namespace,
		DedupeKey:           pgtype.Text{String: cloudCredentialMaterializeDedupeKey(uuid.Nil, ref, "delete"), Valid: true},
		TaskType:            task.Type(),
		Payload:             task.Payload(),
		QueueName:           "default",
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
		NextAttemptAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	return err == nil
}

// enqueueMaterialize fires one task per target_ref. Best-effort —
// queue unavailability is logged as "skipped" by the periodic sweep.
func (h *CloudCredentialHandler) enqueueMaterialize(ctx context.Context, cred sqlc.CloudCredential, refs []TargetRef, op string) {
	if h.enqueuer == nil && h.taskOutbox == nil {
		return
	}
	for _, ref := range refs {
		task, err := tasks.NewCloudCredentialMaterializeTask(tasks.CloudCredentialMaterializePayload{
			CredentialID: cred.ID.String(),
			ClusterID:    ref.ClusterID.String(),
			Namespace:    ref.Namespace,
			SecretName:   ref.SecretName,
			Op:           op,
		})
		if err != nil {
			continue
		}
		payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
		task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
		if h.taskOutbox != nil {
			if _, err := tasks.EnqueueTaskOutbox(ctx, h.taskOutbox, task, tasks.TaskOutboxOptions{
				DedupeKey:           cloudCredentialMaterializeDedupeKey(cred.ID, ref, op),
				QueueName:           "default",
				MaxRetry:            3,
				MaxDeliveryAttempts: 20,
			}); err == nil {
				continue
			}
		}
		if h.enqueuer != nil {
			_, _ = h.enqueuer.Enqueue(task)
		}
	}
}

// enqueueDelete fires a single delete task for a dropped target_ref.
// Best-effort, same as the apply path.
func (h *CloudCredentialHandler) enqueueDelete(ctx context.Context, ref TargetRef) {
	if h.enqueuer == nil && h.taskOutbox == nil {
		return
	}
	task, err := tasks.NewCloudCredentialMaterializeTask(tasks.CloudCredentialMaterializePayload{
		CredentialID: uuid.Nil.String(),
		ClusterID:    ref.ClusterID.String(),
		Namespace:    ref.Namespace,
		SecretName:   ref.SecretName,
		Op:           "delete",
	})
	if err != nil {
		return
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	if h.taskOutbox != nil {
		if _, err := tasks.EnqueueTaskOutbox(ctx, h.taskOutbox, task, tasks.TaskOutboxOptions{
			DedupeKey:           cloudCredentialMaterializeDedupeKey(uuid.Nil, ref, "delete"),
			QueueName:           "default",
			MaxRetry:            3,
			MaxDeliveryAttempts: 20,
		}); err == nil {
			return
		}
	}
	if h.enqueuer != nil {
		_, _ = h.enqueuer.Enqueue(task)
	}
}

func cloudCredentialMaterializeDedupeKey(credentialID uuid.UUID, ref TargetRef, op string) string {
	return fmt.Sprintf("cloud_credential_materialize:%s:%s:%s:%s:%s",
		credentialID.String(),
		ref.ClusterID.String(),
		ref.Namespace,
		ref.SecretName,
		op,
	)
}

// audit writes a best-effort audit row using the optional auditor.
func (h *CloudCredentialHandler) audit(r *http.Request, action string, row sqlc.CloudCredential, projectName string, detail map[string]any) {
	if h == nil || h.auditor == nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["project_id"] = row.ProjectID.String()
	if projectName != "" {
		detail["project_name"] = projectName
	}
	recordAudit(r, h.auditor, action, "cloud_credential", row.ID.String(), row.Name, detail)
}

// --- Utility helpers used by tests + caller wiring --------------------

// decodeStoredTargetRefs parses the JSONB column into a stable Go slice.
// Malformed values default to "no refs" — the row is still usable for
// list/get, just doesn't enqueue any materializations until the
// operator fixes it via PUT.
func decodeStoredTargetRefs(raw json.RawMessage) []TargetRef {
	if len(raw) == 0 {
		return []TargetRef{}
	}
	var out []TargetRef
	if err := json.Unmarshal(raw, &out); err != nil {
		return []TargetRef{}
	}
	if out == nil {
		out = []TargetRef{}
	}
	return out
}

// diffTargetRefs returns refs present in `before` but not in `after`
// (cluster+namespace identity). The handler uses this on PUT to enqueue
// Secret deletion for dropped targets.
func diffTargetRefs(before, after []TargetRef) []TargetRef {
	keep := map[string]TargetRef{}
	for _, r := range after {
		keep[r.ClusterID.String()+"|"+r.Namespace] = r
	}
	out := make([]TargetRef, 0)
	for _, r := range before {
		key := r.ClusterID.String() + "|" + r.Namespace
		if _, present := keep[key]; !present {
			out = append(out, r)
		}
	}
	return out
}

// validatePatchData is the PUT-only relaxed validator. It enforces:
//   - Every value must be a string.
//   - For non-Generic providers, unknown keys are rejected.
//   - Empty string is accepted ONLY if the key is the SecretSentinel —
//     we let the merged-blob validator catch "blanked out a required
//     field" with the same error message the create path uses.
func validatePatchData(provider string, blob map[string]any) error {
	spec, ok := cloudcreds.LookupProvider(provider)
	if !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	allowed := map[string]struct{}{}
	for _, k := range spec.RequiredKeys {
		allowed[k] = struct{}{}
	}
	for _, k := range spec.OptionalKeys {
		allowed[k] = struct{}{}
	}
	for key, raw := range blob {
		if !spec.AllowUnknownKeys {
			if _, ok := allowed[key]; !ok {
				return fmt.Errorf("unknown key %q for provider %q", key, spec.Name)
			}
		}
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("key %q must be a string, got %T", key, raw)
		}
	}
	return nil
}

// userIDFromRequest extracts the authenticated user's UUID for the
// created_by stamp. Returns a NULL pgtype.UUID when the request isn't
// JWT-authenticated (admin scripts, internal jobs) — the column is
// nullable on purpose.
func userIDFromRequest(r *http.Request) pgtype.UUID {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(user.ID)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
