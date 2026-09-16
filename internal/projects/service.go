// Package projects owns transport-independent project policy, namespace
// lifecycle planning, and worker-runtime composition. HTTP handlers translate
// requests and audit outcomes; workers consume this package directly.
package projects

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/projectquota"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const DefaultPodSecurityProfile = "baseline"

var (
	ErrNamespaceAlreadyAssigned = errors.New("namespace already in project")
	ErrNamespaceNotAssigned     = errors.New("namespace not in project")
	ErrNamespaceOwnedElsewhere  = errors.New("namespace owned by another project")
	ErrResourceCapAllocation    = errors.New("project resource cap cannot be allocated")
)

type ValidationError struct{ Message string }

func (e ValidationError) Error() string { return e.Message }

func IsValidationError(err error) bool {
	var validation ValidationError
	return errors.As(err, &validation)
}

// Store is the persistence contract for project commands. It is intentionally
// narrower than generated sqlc.Queries so HTTP and worker composition depend
// on a stable domain boundary instead of a handler-owned mega interface.
type Store interface {
	CreateProject(context.Context, sqlc.CreateProjectParams) (sqlc.Project, error)
	UpdateProject(context.Context, sqlc.UpdateProjectParams) (sqlc.Project, error)
	UpdateProjectPolicy(context.Context, sqlc.UpdateProjectPolicyParams) (sqlc.Project, error)
	DeleteProject(context.Context, uuid.UUID) error
	NamespaceStore
}

type Service struct {
	store  Store
	outbox tasks.TaskOutboxWriter
}

func NewService(store Store, outbox tasks.TaskOutboxWriter) *Service {
	return &Service{store: store, outbox: outbox}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (sqlc.Project, error) {
	if s == nil || s.store == nil {
		return sqlc.Project{}, errors.New("project store is not configured")
	}
	params, err := PrepareCreate(input)
	if err != nil {
		return sqlc.Project{}, err
	}
	return s.store.CreateProject(ctx, params)
}

func (s *Service) Update(ctx context.Context, existing sqlc.Project, input UpdateInput) (sqlc.Project, UpdatePlan, error) {
	if s == nil || s.store == nil {
		return sqlc.Project{}, UpdatePlan{}, errors.New("project store is not configured")
	}
	plan, err := PlanUpdate(existing, input)
	if err != nil {
		return sqlc.Project{}, UpdatePlan{}, err
	}
	project, err := s.store.UpdateProject(ctx, plan.Params)
	return project, plan, err
}

func (s *Service) UpdatePolicy(ctx context.Context, existing sqlc.Project, input PolicyInput) (sqlc.Project, error) {
	if s == nil || s.store == nil {
		return sqlc.Project{}, errors.New("project store is not configured")
	}
	params, err := PlanPolicyUpdate(existing, input)
	if err != nil {
		return sqlc.Project{}, err
	}
	return s.store.UpdateProjectPolicy(ctx, params)
}

func (s *Service) Delete(ctx context.Context, projectID uuid.UUID) error {
	if s == nil || s.store == nil {
		return errors.New("project store is not configured")
	}
	return s.store.DeleteProject(ctx, projectID)
}

func (s *Service) EnsureAndEnqueue(ctx context.Context, projectID, clusterID uuid.UUID, namespace string) error {
	if s == nil {
		return errors.New("project service is not configured")
	}
	return EnsureAndEnqueue(ctx, s.store, s.outbox, projectID, clusterID, namespace)
}

func (s *Service) CleanupAndEnqueue(ctx context.Context, projectID, clusterID uuid.UUID, namespace string) error {
	if s == nil {
		return errors.New("project service is not configured")
	}
	return CleanupAndEnqueue(ctx, s.store, s.outbox, projectID, clusterID, namespace)
}

func (s *Service) EnqueueRemove(ctx context.Context, projectID, clusterID uuid.UUID, namespace string) error {
	if s == nil {
		return errors.New("project service is not configured")
	}
	return EnqueueRemove(ctx, s.outbox, projectID, clusterID, namespace)
}

func IsValidPodSecurityProfile(profile string) bool {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "privileged", "baseline", "restricted":
		return true
	default:
		return false
	}
}

type CreateInput struct {
	Name, DisplayName, Description string
	ClusterID                      uuid.UUID
	Namespaces, ResourceQuota      json.RawMessage
	LimitRange                     json.RawMessage
	NetworkPolicyMode              string
	CreatedByID                    pgtype.UUID
	PodSecurityProfile             *string
	ResourceQuotaCPULimit          *string
	ResourceQuotaMemoryLimit       *string
	ResourceQuotaPodCount          *int32
}

// PrepareCreate normalizes and validates the policy persisted for a new
// project. Namespace authorization remains transport policy, while namespace
// capacity belongs here with every other project lifecycle invariant.
func PrepareCreate(input CreateInput) (sqlc.CreateProjectParams, error) {
	namespaces := DefaultJSON(input.Namespaces, `[]`)
	resourceQuota := DefaultJSON(input.ResourceQuota, `{}`)
	limitRange := DefaultJSON(input.LimitRange, `{}`)
	profile, err := profileOrDefault(input.PodSecurityProfile, DefaultPodSecurityProfile)
	if err != nil {
		return sqlc.CreateProjectParams{}, err
	}
	cpu, memory, pods := optionalCap(input.ResourceQuotaCPULimit, input.ResourceQuotaMemoryLimit, input.ResourceQuotaPodCount)
	if err := validateCap(cpu, memory, pods, Namespaces(namespaces)); err != nil {
		return sqlc.CreateProjectParams{}, err
	}
	return sqlc.CreateProjectParams{
		Name: input.Name, DisplayName: input.DisplayName, Description: input.Description,
		ClusterID: input.ClusterID, Namespaces: namespaces, ResourceQuota: resourceQuota,
		LimitRange: limitRange, NetworkPolicyMode: DefaultNetworkPolicyMode(input.NetworkPolicyMode),
		CreatedByID: input.CreatedByID, PodSecurityProfile: profile,
		ResourceQuotaCpuLimit: cpu, ResourceQuotaMemoryLimit: memory, ResourceQuotaPodCount: pods,
	}, nil
}

type UpdateInput struct {
	DisplayName, Description, NetworkPolicyMode string
	Namespaces, ResourceQuota, LimitRange       json.RawMessage
	PodSecurityProfile                          *string
	ResourceQuotaCPULimit                       *string
	ResourceQuotaMemoryLimit                    *string
	ResourceQuotaPodCount                       *int32
}

type UpdatePlan struct {
	Params  sqlc.UpdateProjectParams
	Added   []string
	Removed []string
}

// PlanUpdate preserves omitted legacy fields, validates the target policy, and
// computes the namespace lifecycle delta used by both HTTP and asynchronous
// reconciliation paths.
func PlanUpdate(existing sqlc.Project, input UpdateInput) (UpdatePlan, error) {
	namespaces := input.Namespaces
	if namespaces == nil {
		encoded, err := json.Marshal(Namespaces(existing.Namespaces))
		if err != nil {
			return UpdatePlan{}, fmt.Errorf("encode existing namespaces: %w", err)
		}
		namespaces = encoded
	}
	fallbackProfile := existing.PodSecurityProfile
	if fallbackProfile == "" {
		// Rows created before the policy columns were introduced retain the
		// migration-era privileged posture until an operator changes them.
		fallbackProfile = "privileged"
	}
	profile, err := profileOrDefault(input.PodSecurityProfile, fallbackProfile)
	if err != nil {
		return UpdatePlan{}, err
	}
	if profile == "" {
		profile = "privileged"
	}
	cpu, memory, pods := existing.ResourceQuotaCpuLimit, existing.ResourceQuotaMemoryLimit, existing.ResourceQuotaPodCount
	if input.ResourceQuotaCPULimit != nil {
		cpu = strings.TrimSpace(*input.ResourceQuotaCPULimit)
	}
	if input.ResourceQuotaMemoryLimit != nil {
		memory = strings.TrimSpace(*input.ResourceQuotaMemoryLimit)
	}
	if input.ResourceQuotaPodCount != nil {
		pods = *input.ResourceQuotaPodCount
	}
	next := Namespaces(namespaces)
	if err := validateCap(cpu, memory, pods, next); err != nil {
		return UpdatePlan{}, err
	}
	mode := input.NetworkPolicyMode
	if mode == "" {
		mode = "none"
	}
	if !validNetworkPolicyMode(mode) {
		return UpdatePlan{}, ValidationError{Message: "Invalid network_policy_mode (must be isolated | allow-same-project | none)"}
	}
	added, removed := DiffNamespaces(Namespaces(existing.Namespaces), next)
	return UpdatePlan{Params: sqlc.UpdateProjectParams{
		ID: existing.ID, DisplayName: input.DisplayName, Description: input.Description,
		Namespaces: namespaces, ResourceQuota: DefaultJSON(input.ResourceQuota, `{}`),
		LimitRange: DefaultJSON(input.LimitRange, `{}`), NetworkPolicyMode: mode,
		PodSecurityProfile: profile, ResourceQuotaCpuLimit: cpu,
		ResourceQuotaMemoryLimit: memory, ResourceQuotaPodCount: pods,
	}, Added: added, Removed: removed}, nil
}

type PolicyInput struct {
	PodSecurityProfile       *string
	ResourceQuotaCPULimit    *string
	ResourceQuotaMemoryLimit *string
	ResourceQuotaPodCount    *int32
	NetworkPolicyMode        *string
}

func PlanPolicyUpdate(existing sqlc.Project, input PolicyInput) (sqlc.UpdateProjectPolicyParams, error) {
	fallbackProfile := existing.PodSecurityProfile
	if fallbackProfile == "" {
		fallbackProfile = "privileged"
	}
	profile, err := profileOrDefault(input.PodSecurityProfile, fallbackProfile)
	if err != nil {
		return sqlc.UpdateProjectPolicyParams{}, err
	}
	cpu, memory, pods := existing.ResourceQuotaCpuLimit, existing.ResourceQuotaMemoryLimit, existing.ResourceQuotaPodCount
	if input.ResourceQuotaCPULimit != nil {
		cpu = strings.TrimSpace(*input.ResourceQuotaCPULimit)
	}
	if input.ResourceQuotaMemoryLimit != nil {
		memory = strings.TrimSpace(*input.ResourceQuotaMemoryLimit)
	}
	if input.ResourceQuotaPodCount != nil {
		pods = *input.ResourceQuotaPodCount
	}
	if err := validateCap(cpu, memory, pods, Namespaces(existing.Namespaces)); err != nil {
		return sqlc.UpdateProjectPolicyParams{}, err
	}
	mode := existing.NetworkPolicyMode
	if input.NetworkPolicyMode != nil {
		mode = strings.TrimSpace(*input.NetworkPolicyMode)
		if !validNetworkPolicyMode(mode) {
			return sqlc.UpdateProjectPolicyParams{}, ValidationError{Message: "Invalid network_policy_mode (must be isolated | allow-same-project | none)"}
		}
	}
	return sqlc.UpdateProjectPolicyParams{ID: existing.ID, PodSecurityProfile: profile,
		ResourceQuotaCpuLimit: cpu, ResourceQuotaMemoryLimit: memory,
		ResourceQuotaPodCount: pods, NetworkPolicyMode: mode}, nil
}

func DefaultJSON(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}

func DefaultNetworkPolicyMode(mode string) string {
	if mode == "" {
		return "none"
	}
	return mode
}

func Namespaces(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var items []string
	if json.Unmarshal(raw, &items) == nil {
		return items
	}
	var unknown []any
	if json.Unmarshal(raw, &unknown) != nil {
		return []string{}
	}
	out := make([]string, 0, len(unknown))
	for _, item := range unknown {
		if value, ok := item.(string); ok {
			out = append(out, value)
		}
	}
	return out
}

func DiffNamespaces(previous, next []string) (added, removed []string) {
	previousSet := make(map[string]struct{}, len(previous))
	for _, namespace := range previous {
		previousSet[namespace] = struct{}{}
	}
	nextSet := make(map[string]struct{}, len(next))
	for _, namespace := range next {
		nextSet[namespace] = struct{}{}
	}
	seen := make(map[string]struct{}, len(next))
	for _, namespace := range next {
		if _, duplicate := seen[namespace]; duplicate {
			continue
		}
		seen[namespace] = struct{}{}
		if _, exists := previousSet[namespace]; !exists {
			added = append(added, namespace)
		}
	}
	seen = make(map[string]struct{}, len(previous))
	for _, namespace := range previous {
		if _, duplicate := seen[namespace]; duplicate {
			continue
		}
		seen[namespace] = struct{}{}
		if _, exists := nextSet[namespace]; !exists {
			removed = append(removed, namespace)
		}
	}
	return added, removed
}

func IsReservedNamespace(namespace string) bool {
	name := strings.ToLower(strings.TrimSpace(namespace))
	switch name {
	case "kube-system", "kube-public", "kube-node-lease", "default":
		return true
	}
	for _, owned := range agenttemplate.AstronomerOwnedNamespaces {
		if name == strings.ToLower(owned) {
			return true
		}
	}
	return false
}

func ValidateNamespaceAddition(project sqlc.Project, namespace string) error {
	if IsReservedNamespace(namespace) {
		return ValidationError{Message: "namespace is reserved by the platform"}
	}
	for _, current := range Namespaces(project.Namespaces) {
		if current == namespace {
			return ValidationError{Message: "namespace is already in this project"}
		}
	}
	return validateCap(project.ResourceQuotaCpuLimit, project.ResourceQuotaMemoryLimit, project.ResourceQuotaPodCount,
		append(append([]string(nil), Namespaces(project.Namespaces)...), namespace))
}

func UpdateNamespaces(existing sqlc.Project, namespaces []string) (sqlc.UpdateProjectParams, error) {
	encoded, err := json.Marshal(namespaces)
	if err != nil {
		return sqlc.UpdateProjectParams{}, fmt.Errorf("encode namespaces: %w", err)
	}
	return sqlc.UpdateProjectParams{ID: existing.ID, DisplayName: existing.DisplayName,
		Description: existing.Description, Namespaces: encoded,
		ResourceQuota: DefaultJSON(existing.ResourceQuota, `{}`), LimitRange: DefaultJSON(existing.LimitRange, `{}`),
		NetworkPolicyMode:  DefaultNetworkPolicyMode(existing.NetworkPolicyMode),
		PodSecurityProfile: existing.PodSecurityProfile, ResourceQuotaCpuLimit: existing.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: existing.ResourceQuotaMemoryLimit, ResourceQuotaPodCount: existing.ResourceQuotaPodCount}, nil
}

// NamespaceTx is the minimal transactional write surface for namespace
// membership. The HTTP layer adds its audit event through executeMutation;
// this domain service owns the state, sidecar, and durable reconciliation
// intent that must all commit together.
type NamespaceTx interface {
	tasks.TaskOutboxWriter
	GetProjectByIDForUpdate(context.Context, uuid.UUID) (sqlc.Project, error)
	UpdateProject(context.Context, sqlc.UpdateProjectParams) (sqlc.Project, error)
	UpsertProjectNamespace(context.Context, sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error)
	DeleteProjectNamespace(context.Context, sqlc.DeleteProjectNamespaceParams) error
}

type NamespaceMutation struct {
	ProjectID      uuid.UUID
	ClusterID      uuid.UUID
	Namespace      string
	IdempotencyKey string
}

func AddNamespace(ctx context.Context, tx NamespaceTx, mutation NamespaceMutation) (sqlc.Project, error) {
	if tx == nil {
		return sqlc.Project{}, audit.ErrOutboxUnavailable
	}
	project, err := tx.GetProjectByIDForUpdate(ctx, mutation.ProjectID)
	if err != nil {
		return sqlc.Project{}, err
	}
	namespace := strings.TrimSpace(mutation.Namespace)
	for _, current := range Namespaces(project.Namespaces) {
		if current == namespace {
			return sqlc.Project{}, ErrNamespaceAlreadyAssigned
		}
	}
	if err := ValidateNamespaceAddition(project, namespace); err != nil {
		return sqlc.Project{}, ErrResourceCapAllocation
	}
	namespaces := append(append([]string(nil), Namespaces(project.Namespaces)...), namespace)
	params, err := UpdateNamespaces(project, namespaces)
	if err != nil {
		return sqlc.Project{}, err
	}
	updated, err := tx.UpdateProject(ctx, params)
	if err != nil {
		return sqlc.Project{}, err
	}
	if _, err := tx.UpsertProjectNamespace(ctx, sqlc.UpsertProjectNamespaceParams{
		ProjectID: project.ID, ClusterID: project.ClusterID, Namespace: namespace,
	}); err != nil {
		if isUniqueViolation(err) {
			return sqlc.Project{}, ErrNamespaceOwnedElsewhere
		}
		return sqlc.Project{}, err
	}
	for _, name := range namespaces {
		if err := enqueueReconcile(ctx, tx, updated.ID, updated.ClusterID, name, "apply", mutation.IdempotencyKey); err != nil {
			return sqlc.Project{}, err
		}
	}
	return updated, nil
}

func RemoveNamespace(ctx context.Context, tx NamespaceTx, mutation NamespaceMutation) (sqlc.Project, error) {
	if tx == nil {
		return sqlc.Project{}, audit.ErrOutboxUnavailable
	}
	project, err := tx.GetProjectByIDForUpdate(ctx, mutation.ProjectID)
	if err != nil {
		return sqlc.Project{}, err
	}
	namespace := strings.TrimSpace(mutation.Namespace)
	current := Namespaces(project.Namespaces)
	kept := make([]string, 0, len(current))
	found := false
	for _, name := range current {
		if name == namespace {
			found = true
			continue
		}
		kept = append(kept, name)
	}
	if !found {
		return sqlc.Project{}, ErrNamespaceNotAssigned
	}
	params, err := UpdateNamespaces(project, kept)
	if err != nil {
		return sqlc.Project{}, err
	}
	updated, err := tx.UpdateProject(ctx, params)
	if err != nil {
		return sqlc.Project{}, err
	}
	if err := tx.DeleteProjectNamespace(ctx, sqlc.DeleteProjectNamespaceParams{
		ProjectID: project.ID, ClusterID: project.ClusterID, Namespace: namespace,
	}); err != nil {
		return sqlc.Project{}, err
	}
	if err := enqueueReconcile(ctx, tx, project.ID, project.ClusterID, namespace, "remove", mutation.IdempotencyKey); err != nil {
		return sqlc.Project{}, err
	}
	for _, name := range kept {
		if err := enqueueReconcile(ctx, tx, project.ID, project.ClusterID, name, "apply", mutation.IdempotencyKey); err != nil {
			return sqlc.Project{}, err
		}
	}
	return updated, nil
}

// EnqueueApply and EnqueueRemove are the non-membership lifecycle hooks used
// after project policy and CRUD updates. They retain the same outbox contract
// as transactional membership commands without importing HTTP details.
func EnqueueApply(ctx context.Context, outbox tasks.TaskOutboxWriter, projectID, clusterID uuid.UUID, namespace string) error {
	return enqueueReconcile(ctx, outbox, projectID, clusterID, namespace, "apply", "")
}

func EnqueueRemove(ctx context.Context, outbox tasks.TaskOutboxWriter, projectID, clusterID uuid.UUID, namespace string) error {
	return enqueueReconcile(ctx, outbox, projectID, clusterID, namespace, "remove", "")
}

type NamespaceStore interface {
	UpsertProjectNamespace(context.Context, sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error)
	DeleteProjectNamespace(context.Context, sqlc.DeleteProjectNamespaceParams) error
}

func EnsureAndEnqueue(ctx context.Context, store NamespaceStore, outbox tasks.TaskOutboxWriter, projectID, clusterID uuid.UUID, namespace string) error {
	if store == nil {
		return errors.New("project namespace store is not configured")
	}
	if _, err := store.UpsertProjectNamespace(ctx, sqlc.UpsertProjectNamespaceParams{ProjectID: projectID, ClusterID: clusterID, Namespace: namespace}); err != nil {
		return err
	}
	return EnqueueApply(ctx, outbox, projectID, clusterID, namespace)
}

func CleanupAndEnqueue(ctx context.Context, store NamespaceStore, outbox tasks.TaskOutboxWriter, projectID, clusterID uuid.UUID, namespace string) error {
	enqueueErr := EnqueueRemove(ctx, outbox, projectID, clusterID, namespace)
	if store == nil {
		return errors.New("project namespace store is not configured")
	}
	if err := store.DeleteProjectNamespace(ctx, sqlc.DeleteProjectNamespaceParams{ProjectID: projectID, ClusterID: clusterID, Namespace: namespace}); err != nil {
		return err
	}
	return enqueueErr
}

func enqueueReconcile(ctx context.Context, outbox tasks.TaskOutboxWriter, projectID, clusterID uuid.UUID, namespace, operation, idempotencyKey string) error {
	if outbox == nil {
		return audit.ErrOutboxUnavailable
	}
	task, err := tasks.NewProjectReconcileTask(tasks.ProjectReconcilePayload{
		ProjectID: projectID.String(), ClusterID: clusterID.String(), Namespace: namespace, Op: operation,
	})
	if err != nil {
		return err
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), reqctx.CorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload)
	requestKey := strings.TrimSpace(idempotencyKey)
	if requestKey == "" {
		requestKey = reqctx.RequestID(ctx)
	}
	if requestKey == "" {
		requestKey = uuid.NewString()
	}
	resourceID := projectID.String() + ":" + clusterID.String() + ":" + namespace
	options := tasks.TaskOutboxOptions{QueueName: tasks.ClusterTemplateApplyQueueName}
	if strings.TrimSpace(idempotencyKey) == "" && reqctx.RequestID(ctx) == "" {
		options.DedupeKey = fmt.Sprintf("project_reconcile:%s:%s:%s:%s", operation, projectID, clusterID, namespace)
		options.MaxDeliveryAttempts = 20
	} else {
		options.DedupeKey = "project_reconcile:" + audit.MutationDedupeKey(requestKey, operation, "project_namespace", resourceID)
		options.MaxRetry = 3
	}
	_, err = tasks.EnqueueTaskOutbox(ctx, outbox, task, options)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func profileOrDefault(candidate *string, fallback string) (string, error) {
	profile := fallback
	if candidate != nil {
		profile = strings.TrimSpace(*candidate)
		if profile == "" {
			profile = DefaultPodSecurityProfile
		}
	}
	profile = strings.ToLower(profile)
	if !IsValidPodSecurityProfile(profile) {
		return "", ValidationError{Message: "Invalid pod_security_profile (must be privileged | baseline | restricted)"}
	}
	return profile, nil
}

func optionalCap(cpu, memory *string, pods *int32) (string, string, int32) {
	var cpuValue, memoryValue string
	var podValue int32
	if cpu != nil {
		cpuValue = strings.TrimSpace(*cpu)
	}
	if memory != nil {
		memoryValue = strings.TrimSpace(*memory)
	}
	if pods != nil {
		podValue = *pods
	}
	return cpuValue, memoryValue, podValue
}

func validateCap(cpu, memory string, pods int32, namespaces []string) error {
	if _, err := projectquota.Allocate(projectquota.Cap{CPU: cpu, Memory: memory, Pods: pods}, namespaces); err != nil {
		return ValidationError{Message: "Project resource cap cannot be allocated: " + err.Error()}
	}
	return nil
}

func validNetworkPolicyMode(mode string) bool {
	switch mode {
	case "isolated", "allow-same-project", "none":
		return true
	}
	return false
}

// ReconcileStore is the worker-facing persistence contract. It deliberately
// mirrors only the project reconciler's needs so server composition can pass
// sqlc.Queries directly and workers never need an HTTP handler adapter.
type ReconcileStore interface {
	GetProjectByID(context.Context, uuid.UUID) (sqlc.Project, error)
	GetClusterRegistryConfig(context.Context, uuid.UUID) (sqlc.ClusterRegistryConfig, error)
	GetDefaultPodSecurityTemplate(context.Context) (sqlc.PodSecurityTemplate, error)
	ListProjectNamespaces(context.Context, uuid.UUID) ([]sqlc.ProjectNamespace, error)
	UpsertProjectResourceQuotaAllocation(context.Context, sqlc.UpsertProjectResourceQuotaAllocationParams) (sqlc.ProjectResourceQuotaAllocation, error)
	DeleteProjectResourceQuotaAllocation(context.Context, sqlc.DeleteProjectResourceQuotaAllocationParams) error
	ListAllProjectNamespaces(context.Context) ([]sqlc.ProjectNamespace, error)
	UpsertProjectNamespace(context.Context, sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error)
	DeleteProjectNamespace(context.Context, sqlc.DeleteProjectNamespaceParams) error
	ClaimProjectNamespaceReconcile(context.Context, sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error)
	MarkProjectNamespaceReconciled(context.Context, sqlc.MarkProjectNamespaceReconciledParams) (int64, error)
}

type K8sRequester interface {
	Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error)
}

func NewReconcileRuntime(store ReconcileStore, requester K8sRequester, encryptor *auth.Encryptor) tasks.ProjectRuntime {
	if store == nil || requester == nil || encryptor == nil {
		return tasks.ProjectRuntime{}
	}
	return tasks.ProjectRuntime{Deps: tasks.ProjectReconcileDeps{Queries: reconcileStoreAdapter{store}, Requester: requesterAdapter{requester}, Encryptor: encryptor}}
}

func TaskRequester(requester K8sRequester) tasks.ProjectK8sRequester {
	if requester == nil {
		return nil
	}
	return requesterAdapter{requester}
}

type reconcileStoreAdapter struct{ store ReconcileStore }

func (a reconcileStoreAdapter) GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error) {
	return a.store.GetProjectByID(ctx, id)
}
func (a reconcileStoreAdapter) GetClusterRegistryConfig(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return a.store.GetClusterRegistryConfig(ctx, id)
}
func (a reconcileStoreAdapter) GetDefaultPodSecurityTemplate(ctx context.Context) (sqlc.PodSecurityTemplate, error) {
	return a.store.GetDefaultPodSecurityTemplate(ctx)
}
func (a reconcileStoreAdapter) ListProjectNamespaces(ctx context.Context, id uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	return a.store.ListProjectNamespaces(ctx, id)
}
func (a reconcileStoreAdapter) UpsertProjectResourceQuotaAllocation(ctx context.Context, arg sqlc.UpsertProjectResourceQuotaAllocationParams) (sqlc.ProjectResourceQuotaAllocation, error) {
	return a.store.UpsertProjectResourceQuotaAllocation(ctx, arg)
}
func (a reconcileStoreAdapter) DeleteProjectResourceQuotaAllocation(ctx context.Context, arg sqlc.DeleteProjectResourceQuotaAllocationParams) error {
	return a.store.DeleteProjectResourceQuotaAllocation(ctx, arg)
}
func (a reconcileStoreAdapter) ListAllProjectNamespaces(ctx context.Context) ([]sqlc.ProjectNamespace, error) {
	return a.store.ListAllProjectNamespaces(ctx)
}
func (a reconcileStoreAdapter) UpsertProjectNamespace(ctx context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error) {
	return a.store.UpsertProjectNamespace(ctx, arg)
}
func (a reconcileStoreAdapter) DeleteProjectNamespace(ctx context.Context, arg sqlc.DeleteProjectNamespaceParams) error {
	return a.store.DeleteProjectNamespace(ctx, arg)
}
func (a reconcileStoreAdapter) ClaimProjectNamespaceReconcile(ctx context.Context, arg sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error) {
	return a.store.ClaimProjectNamespaceReconcile(ctx, arg)
}
func (a reconcileStoreAdapter) MarkProjectNamespaceReconciled(ctx context.Context, arg sqlc.MarkProjectNamespaceReconciledParams) (int64, error) {
	return a.store.MarkProjectNamespaceReconciled(ctx, arg)
}

type requesterAdapter struct{ requester K8sRequester }

func (a requesterAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*tasks.ProjectK8sResponse, error) {
	response, err := a.requester.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("Kubernetes requester returned an empty response")
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Body)
	if err != nil {
		return nil, fmt.Errorf("decode Kubernetes response body: %w", err)
	}
	return &tasks.ProjectK8sResponse{StatusCode: response.StatusCode, Body: decoded}, nil
}
