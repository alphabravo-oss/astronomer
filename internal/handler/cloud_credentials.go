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
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// CloudCredentialQuerier is the slice of *sqlc.Queries the handler needs.
// Defined as an interface so tests can pass narrow fakes; the production
// wiring passes *sqlc.Queries (which satisfies this surface).
type CloudCredentialQuerier interface {
	// Projects + clusters for FK existence checks.
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListProjectNamespaces(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error)
	// Cloud-credentials CRUD.
	ListCloudCredentialsForProject(ctx context.Context, projectID uuid.UUID) ([]sqlc.CloudCredential, error)
	GetCloudCredentialByID(ctx context.Context, id uuid.UUID) (sqlc.CloudCredential, error)
	GetCloudCredentialByProjectAndName(ctx context.Context, arg sqlc.GetCloudCredentialByProjectAndNameParams) (sqlc.CloudCredential, error)
	CreateCloudCredential(ctx context.Context, arg sqlc.CreateCloudCredentialParams) (sqlc.CloudCredential, error)
	UpdateCloudCredential(ctx context.Context, arg sqlc.UpdateCloudCredentialParams) (sqlc.CloudCredential, error)
	DeleteCloudCredential(ctx context.Context, id uuid.UUID) error
	// Materializations.
	ListCloudCredentialMaterializations(ctx context.Context, credentialID uuid.UUID) ([]sqlc.CloudCredentialMaterialization, error)
	DeleteOrphanCloudCredentialMaterializations(ctx context.Context, arg sqlc.DeleteOrphanCloudCredentialMaterializationsParams) error
}

type cloudCredentialMaterializationTaskOutboxQuerier interface {
	UpsertCloudCredentialMaterializationWithTaskOutbox(ctx context.Context, arg sqlc.UpsertCloudCredentialMaterializationWithTaskOutboxParams) (sqlc.CloudCredentialMaterialization, error)
	DeleteCloudCredentialMaterializationWithTaskOutbox(ctx context.Context, arg sqlc.DeleteCloudCredentialMaterializationWithTaskOutboxParams) error
}

// CloudCredentialMutationTx is the complete transaction-bound surface for a
// credential write. Production always commits the encrypted credential row,
// every materialization task intent, and the audit envelope together.
type CloudCredentialMutationTx interface {
	audit.OutboxQuerier
	cloudCredentialMaterializationTaskOutboxQuerier
	CreateCloudCredential(context.Context, sqlc.CreateCloudCredentialParams) (sqlc.CloudCredential, error)
	UpdateCloudCredential(context.Context, sqlc.UpdateCloudCredentialParams) (sqlc.CloudCredential, error)
	DeleteCloudCredential(context.Context, uuid.UUID) error
	DeleteOrphanCloudCredentialMaterializations(context.Context, sqlc.DeleteOrphanCloudCredentialMaterializationsParams) error
}

type cloudCredentialRunTxFunc func(context.Context, func(CloudCredentialMutationTx) error) error

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

type digitalOceanCloudTester interface {
	TestDigitalOcean(ctx context.Context, blob map[string]string) (CloudTestResult, error)
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
	queries   CloudCredentialQuerier
	auditor   any // auditWriterV1 surface — recordAudit type-asserts internally
	encryptor *auth.Encryptor
	tester    CloudTester
	runTx     cloudCredentialRunTxFunc
}

// NewCloudCredentialHandler wires the handler. The encryptor, transaction
// runner, and audit writer are required by write and provider-probe paths.
func NewCloudCredentialHandler(queries CloudCredentialQuerier) *CloudCredentialHandler {
	return &CloudCredentialHandler{queries: queries}
}

// SetAuditor wires mandatory audit persistence for provider probes. CRUD uses
// the transaction-bound audit outbox configured through SetRunTx.
func (h *CloudCredentialHandler) SetAuditor(a any) {
	if h == nil {
		return
	}
	h.auditor = a
}

// SetRunTx wires the production database transaction used for encrypted
// credential state, reconciliation task intents, and audit evidence.
func (h *CloudCredentialHandler) SetRunTx(runTx cloudCredentialRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *CloudCredentialHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetEncryptor wires the Fernet encryptor. The handler 503s on POST/PUT
// when encryptor is nil so an operator can't accidentally store creds
// in plaintext.
func (h *CloudCredentialHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
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
// openapi:request CloudCredentialRequest
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
