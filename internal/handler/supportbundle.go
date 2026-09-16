// Package handler — support bundle.
//
// Generates a downloadable .zip of platform diagnostics suitable for sharing
// with whoever is debugging an Astronomer install. Two design rules:
//
//  1. The bundle MUST NOT contain plaintext credentials. Anything that could
//     wrap a secret (password hashes, API tokens, Argo auth tokens, private
//     keys, kubeconfigs, credential-shaped strings) is replaced with a
//     redaction marker.
//
//  2. Collection runs only as a durable tunnel-worker operation. Section
//     writers stream into a hard-bounded artifact buffer; HTTP handlers only
//     accept, poll, or download an already completed artifact.
package handler

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SupportBundleQuerier is the slice of sqlc Queries that the bundle reads.
type SupportBundleQuerier interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	ListClusterLivenessForClusters(ctx context.Context, clusterIds []uuid.UUID) ([]sqlc.ClusterLiveness, error)
	ListUsers(ctx context.Context, arg sqlc.ListUsersParams) ([]sqlc.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	ListAuditLogV1(ctx context.Context, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error)
	// ListActiveConnections drives the agent-connections bundle section —
	// last-seen + cluster_id are exactly what an
	// L3 engineer needs when triaging a "why are these clusters offline?"
	// question.
	ListActiveConnections(ctx context.Context, limit int32) ([]sqlc.AgentConnection, error)
}

// supportBundleCharlieQuerier is optional so Charlie remains entirely absent
// from support-bundle work on installations that have never enabled the
// feature. It reads only local Astronomer metadata; it never constructs a
// Product Bridge or Charlie Central client.
type supportBundleCharlieQuerier interface {
	GetLatestCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	ListCharlieTriggerRules(context.Context, uuid.UUID) ([]sqlc.CharlieTriggerRule, error)
	ListCharlieFindings(context.Context, sqlc.ListCharlieFindingsParams) ([]sqlc.CharlieFinding, error)
}

// supportBundleAuditOutboxQuerier is optional for compatibility with narrow
// test and extension stores. Production *sqlc.Queries implements it and the
// resulting section contains lifecycle counters only—never audit detail.
type supportBundleAuditOutboxQuerier interface {
	GetAuditOutboxHealth(context.Context) (sqlc.GetAuditOutboxHealthRow, error)
}

// SupportBundleAsynqInspector is the slice of asynq.Inspector the bundle
// needs to capture queue + DLQ state. *asynq.Inspector satisfies this. Kept
// behind an interface so tests can inject a fake; nil means "skip the
// asynq sections cleanly".
type SupportBundleAsynqInspector interface {
	Queues() ([]string, error)
	GetQueueInfo(qname string) (*asynq.QueueInfo, error)
	ListArchivedTasks(qname string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
}

// SupportBundleDBPooler exposes the minimum surface needed to run the
// raw SELECT against schema_migrations. *pgxpool.Pool satisfies this.
type SupportBundleDBPooler interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type SupportBundleOperationStore interface {
	GetSupportBundleOperation(context.Context, uuid.UUID) (sqlc.GetSupportBundleOperationRow, error)
	GetSupportBundleArtifact(context.Context, uuid.UUID) (sqlc.GetSupportBundleArtifactRow, error)
}

// SupportBundleMutationTx is the transaction-bound surface for accepting a
// generation request. The operation, tunnel-queue task intent, and audit intent
// are one PostgreSQL commit decision.
type SupportBundleMutationTx interface {
	CreateSupportBundleOperation(context.Context, sqlc.CreateSupportBundleOperationParams) (sqlc.CreateSupportBundleOperationRow, error)
	audit.OutboxQuerier
}

type supportBundleRunTxFunc func(context.Context, func(SupportBundleMutationTx) error) error

// SupportBundleHandler exposes durable support-bundle operations and owns the
// reusable generator executed by the tunnel worker.
type SupportBundleHandler struct {
	queries    SupportBundleQuerier
	operations SupportBundleOperationStore
	k8s        kubernetes.Interface
	namespace  string
	// Optional, wired via setters; nil-safe section writers degrade
	// gracefully when these are absent.
	inspector SupportBundleAsynqInspector
	db        SupportBundleDBPooler
	runTx     supportBundleRunTxFunc
}

func (h *SupportBundleHandler) SetRunTx(runTx supportBundleRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *SupportBundleHandler) TransactionalMutationWired() bool {
	return h != nil && h.operations != nil && h.runTx != nil
}

// SetAsynqInspector wires the asynq queue inspector. Enables the
// asynq-queues.json section. nil disables it.
func (h *SupportBundleHandler) SetAsynqInspector(insp SupportBundleAsynqInspector) {
	if h == nil {
		return
	}
	h.inspector = insp
}

// SetDBPool wires the raw DB pool. Enables the schema-migrations.json
// section (which reads a non-sqlc table). nil disables it.
func (h *SupportBundleHandler) SetDBPool(pool SupportBundleDBPooler) {
	if h == nil {
		return
	}
	h.db = pool
}

// NewSupportBundleHandler returns a handler. k8sClient and namespace are
// optional: when nil/empty, pod-state and pod-logs sections are skipped
// and the bundle just contains DB-derived sections.
func NewSupportBundleHandler(queries SupportBundleQuerier, operations SupportBundleOperationStore, k8sClient kubernetes.Interface, namespace string) *SupportBundleHandler {
	return &SupportBundleHandler{
		queries:    queries,
		operations: operations,
		k8s:        k8sClient,
		namespace:  namespace,
	}
}

type SupportBundleOperationResponse struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	AttemptCount int32      `json:"attempt_count"`
	ErrorCode    string     `json:"error_code,omitempty"`
	Filename     string     `json:"filename,omitempty"`
	SHA256       string     `json:"sha256,omitempty"`
	Size         int64      `json:"size"`
	ExpiresAt    time.Time  `json:"expires_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	StatusURL    string     `json:"status_url"`
	DownloadURL  string     `json:"download_url,omitempty"`
}

// Create accepts a durable generation request. There is deliberately no
// synchronous fallback: collection may enumerate pods, logs, events, and Helm
// state and therefore only runs in the bounded tunnel worker.
