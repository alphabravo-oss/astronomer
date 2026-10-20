package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
)

// ClusterSync is the management-plane side of the Cluster CR.
type ClusterSync interface {
	EnsureFromCRD(context.Context, ClusterSpec) (ClusterStatus, error)
	DeleteByName(context.Context, string) error
}

// ProjectSync is the management-plane side of the Project CR.
type ProjectSync interface {
	EnsureFromCRD(context.Context, ProjectSpec) (ProjectStatus, error)
	DeleteByName(context.Context, string) error
}

// ObjectRef identifies the CR that owns a management-plane row.
type ObjectRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	Generation int64
}

// ClusterOwnershipSync is optionally implemented by a ClusterSync that
// records which CR owns a cluster row.
type ClusterOwnershipSync interface {
	ValidateClusterOwnership(context.Context, ClusterSpec, ObjectRef) error
	RecordClusterOwnership(context.Context, ClusterSpec, ObjectRef) error
}

// ProjectOwnershipSync is optionally implemented by a ProjectSync that
// records which CR owns a project row.
type ProjectOwnershipSync interface {
	ValidateProjectOwnership(context.Context, ProjectSpec, ObjectRef) error
	RecordProjectOwnership(context.Context, ProjectSpec, ObjectRef) error
}

// ErrInProgress is returned by ClusterSync.DeleteByName while a decommission
// is still running; the finalizer stays and the request is polled.
var ErrInProgress = errors.New("operator: delete in progress")

// DefaultPollPeriod is the steady-state resync interval.
const DefaultPollPeriod = 60 * time.Second

// Options are shared by every reconciler constructor.
type Options struct {
	Log      *slog.Logger
	Poll     time.Duration
	Observer Observer
}

func (o Options) poll() time.Duration {
	if o.Poll <= 0 {
		return DefaultPollPeriod
	}
	return o.Poll
}

func (o Options) log() *slog.Logger {
	if o.Log == nil {
		return slog.Default()
	}
	return o.Log
}

// Stage names, shared by the three pipelines where they mean the same thing.
const (
	StageFetch             = "fetch"
	StageFinalize          = "finalize"
	StageEnsureFinalizer   = "ensure-finalizer"
	StageResolveSpec       = "resolve-spec"
	StageValidateOwnership = "validate-ownership"
	StageSync              = "sync"
	StageRecordOwnership   = "record-ownership"
	StagePublishStatus     = "publish-status"
)

// fetchObject loads the request's object; a missing object ends the reconcile.
func fetchObject(ctx context.Context, c client.Client, req reconcile.Request, into client.Object) (Step, error) {
	if err := c.Get(ctx, req.NamespacedName, into); err != nil {
		if apierrors.IsNotFound(err) {
			return Done(reconcile.Result{}), nil
		}
		return Step{}, err
	}
	return Next(), nil
}

// ensureFinalizer adds finalizer when missing.
func ensureFinalizer(ctx context.Context, c client.Client, object client.Object, finalizer string) (Step, error) {
	if controllerutil.ContainsFinalizer(object, finalizer) {
		return Next(), nil
	}
	controllerutil.AddFinalizer(object, finalizer)
	if err := c.Update(ctx, object); err != nil {
		return Step{}, err
	}
	return Next(), nil
}

// removeFinalizer drops finalizer and ends the reconcile.
func removeFinalizer(ctx context.Context, c client.Client, object client.Object, finalizer string) (Step, error) {
	controllerutil.RemoveFinalizer(object, finalizer)
	if err := c.Update(ctx, object); err != nil {
		return Step{}, err
	}
	return Done(reconcile.Result{}), nil
}

// ---------------------------------------------------------------- Cluster

type clusterRun struct {
	req    reconcile.Request
	object Cluster
	spec   ClusterSpec
	ref    ObjectRef
	status ClusterStatus
}

// ClusterReconciler reconciles Cluster CRs into management-plane clusters.
type ClusterReconciler struct {
	*Pipeline[clusterRun]
	client client.Client
	sync   ClusterSync
	poll   time.Duration
}

// NewClusterReconciler builds the Cluster pipeline:
// fetch → finalize → ensure-finalizer → resolve-spec → validate-ownership →
// sync → record-ownership → publish-status.
func NewClusterReconciler(c client.Client, sync ClusterSync, opts Options) (*ClusterReconciler, error) {
	if c == nil || sync == nil {
		return nil, errors.New("cluster reconciler: client and sync are required")
	}
	r := &ClusterReconciler{client: c, sync: sync, poll: opts.poll()}
	pipeline, err := NewPipeline(PipelineConfig[clusterRun]{
		Controller: "cluster",
		NewState:   func(req reconcile.Request) *clusterRun { return &clusterRun{req: req} },
		Stages: []Stage[clusterRun]{
			{Name: StageFetch, Run: r.fetch},
			{Name: StageFinalize, Run: r.finalize},
			{Name: StageEnsureFinalizer, Run: r.ensureFinalizer},
			{Name: StageResolveSpec, FailureReason: "InvalidAgentProfile", Run: r.resolveSpec},
			{Name: StageValidateOwnership, FailureReason: "OwnershipConflict", Run: r.validateOwnership},
			{Name: StageSync, FailureReason: "SyncFailed", Run: r.syncSpec},
			{Name: StageRecordOwnership, FailureReason: "OwnershipWriteFailed", Run: r.recordOwnership},
			{Name: StagePublishStatus, Run: r.publishStatus},
		},
		OnFailure: r.recordFailure,
		Log:       opts.log(),
		Observer:  opts.Observer,
	})
	if err != nil {
		return nil, err
	}
	r.Pipeline = pipeline
	return r, nil
}

func (r *ClusterReconciler) fetch(ctx context.Context, s *clusterRun) (Step, error) {
	return fetchObject(ctx, r.client, s.req, &s.object)
}

func (r *ClusterReconciler) finalize(ctx context.Context, s *clusterRun) (Step, error) {
	if s.object.DeletionTimestamp.IsZero() {
		return Next(), nil
	}
	if !controllerutil.ContainsFinalizer(&s.object, FinalizerCluster) {
		return Done(reconcile.Result{}), nil
	}
	if err := r.sync.DeleteByName(ctx, firstNonBlank(s.object.Spec.Name, s.object.Name)); err != nil {
		if errors.Is(err, ErrInProgress) {
			return Done(reconcile.Result{RequeueAfter: r.poll}), nil
		}
		return Step{}, err
	}
	return removeFinalizer(ctx, r.client, &s.object, FinalizerCluster)
}

func (r *ClusterReconciler) ensureFinalizer(ctx context.Context, s *clusterRun) (Step, error) {
	return ensureFinalizer(ctx, r.client, &s.object, FinalizerCluster)
}

func (r *ClusterReconciler) resolveSpec(ctx context.Context, s *clusterRun) (Step, error) {
	spec, err := r.clusterSpecForSync(ctx, s.object)
	if err != nil {
		return Step{}, err
	}
	s.spec = spec
	s.ref = objectReference(s.object.APIVersion, "Cluster", s.object.Namespace, s.object.Name, s.object.Generation)
	return Next(), nil
}

func (r *ClusterReconciler) validateOwnership(ctx context.Context, s *clusterRun) (Step, error) {
	if ownership, ok := r.sync.(ClusterOwnershipSync); ok {
		if err := ownership.ValidateClusterOwnership(ctx, s.spec, s.ref); err != nil {
			return Step{}, err
		}
	}
	return Next(), nil
}

func (r *ClusterReconciler) syncSpec(ctx context.Context, s *clusterRun) (Step, error) {
	status, err := r.sync.EnsureFromCRD(ctx, s.spec)
	if err != nil {
		return Step{}, err
	}
	s.status = status
	return Next(), nil
}

func (r *ClusterReconciler) recordOwnership(ctx context.Context, s *clusterRun) (Step, error) {
	if ownership, ok := r.sync.(ClusterOwnershipSync); ok {
		if err := ownership.RecordClusterOwnership(ctx, s.spec, s.ref); err != nil {
			return Step{}, err
		}
	}
	return Next(), nil
}

func (r *ClusterReconciler) publishStatus(ctx context.Context, s *clusterRun) (Step, error) {
	s.status.LastReconciled = metav1.Now()
	s.status.ObservedProjectRefs = copyStringSlice(s.spec.ProjectRefs)
	s.status.Conditions = readyConditions(s.object.Generation, "Cluster is synchronized")
	if err := patchClusterStatus(ctx, r.client, &s.object, s.status); err != nil {
		return Step{}, err
	}
	return Done(reconcile.Result{RequeueAfter: r.poll}), nil
}

func (r *ClusterReconciler) recordFailure(ctx context.Context, s *clusterRun, reason string, err error) error {
	status := s.object.Status
	status.LastReconciled = metav1.Now()
	status.Conditions = failedConditions(s.object.Generation, reason, err.Error())
	return patchClusterStatus(ctx, r.client, &s.object, status)
}

func (r *ClusterReconciler) clusterSpecForSync(ctx context.Context, object Cluster) (ClusterSpec, error) {
	spec := object.Spec
	if strings.TrimSpace(spec.Name) == "" {
		spec.Name = object.Name
	}
	ref := strings.TrimSpace(spec.Agent.ProfileRef)
	if ref == "" {
		return spec, nil
	}
	if strings.Contains(ref, "/") {
		return spec, errors.New("agent.profileRef must be a same-namespace name")
	}
	var profile AgentProfile
	if err := r.client.Get(ctx, client.ObjectKey{Namespace: object.Namespace, Name: ref}, &profile); err != nil {
		return spec, fmt.Errorf("resolve AgentProfile %s: %w", ref, err)
	}
	if problems := validateAgentProfileSpec(profile.Spec); len(problems) > 0 {
		return spec, fmt.Errorf("AgentProfile %s is invalid: %s", ref, strings.Join(problems, "; "))
	}
	spec.Agent.PrivilegeProfile = profile.Spec.PrivilegeProfile
	return spec, nil
}

// ---------------------------------------------------------------- Project

type projectRun struct {
	req    reconcile.Request
	object Project
	spec   ProjectSpec
	ref    ObjectRef
	status ProjectStatus
}

// ProjectReconciler reconciles Project CRs into management-plane projects.
type ProjectReconciler struct {
	*Pipeline[projectRun]
	client client.Client
	sync   ProjectSync
	poll   time.Duration
}

// NewProjectReconciler builds the Project pipeline:
// fetch → finalize → ensure-finalizer → resolve-spec → validate-ownership →
// sync → record-ownership → publish-status.
func NewProjectReconciler(c client.Client, sync ProjectSync, opts Options) (*ProjectReconciler, error) {
	if c == nil || sync == nil {
		return nil, errors.New("project reconciler: client and sync are required")
	}
	r := &ProjectReconciler{client: c, sync: sync, poll: opts.poll()}
	pipeline, err := NewPipeline(PipelineConfig[projectRun]{
		Controller: "project",
		NewState:   func(req reconcile.Request) *projectRun { return &projectRun{req: req} },
		Stages: []Stage[projectRun]{
			{Name: StageFetch, Run: r.fetch},
			{Name: StageFinalize, Run: r.finalize},
			{Name: StageEnsureFinalizer, Run: r.ensureFinalizer},
			{Name: StageResolveSpec, Run: r.resolveSpec},
			{Name: StageValidateOwnership, FailureReason: "OwnershipConflict", Run: r.validateOwnership},
			{Name: StageSync, FailureReason: "SyncFailed", Run: r.syncSpec},
			{Name: StageRecordOwnership, FailureReason: "OwnershipWriteFailed", Run: r.recordOwnership},
			{Name: StagePublishStatus, Run: r.publishStatus},
		},
		OnFailure: r.recordFailure,
		Log:       opts.log(),
		Observer:  opts.Observer,
	})
	if err != nil {
		return nil, err
	}
	r.Pipeline = pipeline
	return r, nil
}

func (r *ProjectReconciler) fetch(ctx context.Context, s *projectRun) (Step, error) {
	return fetchObject(ctx, r.client, s.req, &s.object)
}

func (r *ProjectReconciler) finalize(ctx context.Context, s *projectRun) (Step, error) {
	if s.object.DeletionTimestamp.IsZero() {
		return Next(), nil
	}
	if !controllerutil.ContainsFinalizer(&s.object, FinalizerProject) {
		return Done(reconcile.Result{}), nil
	}
	if err := r.sync.DeleteByName(ctx, firstNonBlank(s.object.Spec.Name, s.object.Name)); err != nil {
		return Step{}, err
	}
	return removeFinalizer(ctx, r.client, &s.object, FinalizerProject)
}

func (r *ProjectReconciler) ensureFinalizer(ctx context.Context, s *projectRun) (Step, error) {
	return ensureFinalizer(ctx, r.client, &s.object, FinalizerProject)
}

func (r *ProjectReconciler) resolveSpec(_ context.Context, s *projectRun) (Step, error) {
	s.spec = s.object.Spec
	if strings.TrimSpace(s.spec.Name) == "" {
		s.spec.Name = s.object.Name
	}
	s.ref = objectReference(s.object.APIVersion, "Project", s.object.Namespace, s.object.Name, s.object.Generation)
	return Next(), nil
}

func (r *ProjectReconciler) validateOwnership(ctx context.Context, s *projectRun) (Step, error) {
	if ownership, ok := r.sync.(ProjectOwnershipSync); ok {
		if err := ownership.ValidateProjectOwnership(ctx, s.spec, s.ref); err != nil {
			return Step{}, err
		}
	}
	return Next(), nil
}

func (r *ProjectReconciler) syncSpec(ctx context.Context, s *projectRun) (Step, error) {
	status, err := r.sync.EnsureFromCRD(ctx, s.spec)
	if err != nil {
		return Step{}, err
	}
	s.status = status
	return Next(), nil
}

func (r *ProjectReconciler) recordOwnership(ctx context.Context, s *projectRun) (Step, error) {
	if ownership, ok := r.sync.(ProjectOwnershipSync); ok {
		if err := ownership.RecordProjectOwnership(ctx, s.spec, s.ref); err != nil {
			return Step{}, err
		}
	}
	return Next(), nil
}

func (r *ProjectReconciler) publishStatus(ctx context.Context, s *projectRun) (Step, error) {
	s.status.LastReconciled = metav1.Now()
	s.status.ObservedClusters = copyStringSlice(s.spec.Clusters)
	s.status.Conditions = readyConditions(s.object.Generation, "Project is synchronized")
	if err := patchProjectStatus(ctx, r.client, &s.object, s.status); err != nil {
		return Step{}, err
	}
	return Done(reconcile.Result{RequeueAfter: r.poll}), nil
}

func (r *ProjectReconciler) recordFailure(ctx context.Context, s *projectRun, reason string, err error) error {
	status := s.object.Status
	status.LastReconciled = metav1.Now()
	status.Conditions = failedConditions(s.object.Generation, reason, err.Error())
	return patchProjectStatus(ctx, r.client, &s.object, status)
}

// ----------------------------------------------------------- AgentProfile

type agentProfileRun struct {
	req    reconcile.Request
	object AgentProfile
}

// AgentProfileReconciler validates AgentProfile CRs and publishes their
// effective RBAC summary.
type AgentProfileReconciler struct {
	*Pipeline[agentProfileRun]
	client client.Client
	poll   time.Duration
}

// NewAgentProfileReconciler builds the AgentProfile pipeline:
// fetch → finalize → ensure-finalizer → publish-status.
func NewAgentProfileReconciler(c client.Client, opts Options) (*AgentProfileReconciler, error) {
	if c == nil {
		return nil, errors.New("agent profile reconciler: client is required")
	}
	r := &AgentProfileReconciler{client: c, poll: opts.poll()}
	pipeline, err := NewPipeline(PipelineConfig[agentProfileRun]{
		Controller: "agentprofile",
		NewState:   func(req reconcile.Request) *agentProfileRun { return &agentProfileRun{req: req} },
		Stages: []Stage[agentProfileRun]{
			{Name: StageFetch, Run: r.fetch},
			{Name: StageFinalize, Run: r.finalize},
			{Name: StageEnsureFinalizer, Run: r.ensureFinalizer},
			{Name: StagePublishStatus, Run: r.publishStatus},
		},
		Log:      opts.log(),
		Observer: opts.Observer,
	})
	if err != nil {
		return nil, err
	}
	r.Pipeline = pipeline
	return r, nil
}

func (r *AgentProfileReconciler) fetch(ctx context.Context, s *agentProfileRun) (Step, error) {
	return fetchObject(ctx, r.client, s.req, &s.object)
}

func (r *AgentProfileReconciler) finalize(ctx context.Context, s *agentProfileRun) (Step, error) {
	if s.object.DeletionTimestamp.IsZero() {
		return Next(), nil
	}
	if !controllerutil.ContainsFinalizer(&s.object, FinalizerAgentProfile) {
		return Done(reconcile.Result{}), nil
	}
	return removeFinalizer(ctx, r.client, &s.object, FinalizerAgentProfile)
}

func (r *AgentProfileReconciler) ensureFinalizer(ctx context.Context, s *agentProfileRun) (Step, error) {
	return ensureFinalizer(ctx, r.client, &s.object, FinalizerAgentProfile)
}

func (r *AgentProfileReconciler) publishStatus(ctx context.Context, s *agentProfileRun) (Step, error) {
	before := s.object.DeepCopy()
	s.object.Status = buildAgentProfileStatus(s.object)
	if err := r.client.Status().Patch(ctx, &s.object, client.MergeFrom(before)); err != nil {
		return Step{}, err
	}
	return Done(reconcile.Result{RequeueAfter: r.poll}), nil
}

func buildAgentProfileStatus(object AgentProfile) AgentProfileStatus {
	status := AgentProfileStatus{ObservedGeneration: object.Generation, LastReconciled: metav1.Now(), EffectiveRBAC: effectiveRBACForAgentProfile(object.Spec)}
	if problems := validateAgentProfileSpec(object.Spec); len(problems) > 0 {
		status.Phase = "Invalid"
		status.Conditions = failedConditions(object.Generation, "ValidationFailed", strings.Join(problems, "; "))
		return status
	}
	status.Phase = "Ready"
	status.Conditions = readyConditions(object.Generation, "AgentProfile is valid")
	return status
}

func validateAgentProfileSpec(spec AgentProfileSpec) []string {
	profile := agenttemplate.NormalizePrivilegeProfile(spec.PrivilegeProfile)
	if profile != strings.TrimSpace(spec.PrivilegeProfile) && strings.TrimSpace(spec.PrivilegeProfile) != "" {
		known := map[string]bool{"viewer": true, "operator": true, "namespace-viewer": true, "namespace-operator": true, "custom": true, "admin": true}
		if !known[strings.TrimSpace(spec.PrivilegeProfile)] {
			return []string{"privilegeProfile is unsupported"}
		}
	}
	var problems []string
	if (profile == agenttemplate.PrivilegeProfileNamespaceViewer || profile == agenttemplate.PrivilegeProfileNamespaceOperator) && len(spec.NamespaceScope) == 0 {
		problems = append(problems, "namespaceScope is required")
	}
	if profile != agenttemplate.PrivilegeProfileAdmin && (spec.HostAccess.HostNetwork || spec.HostAccess.HostPID || len(spec.HostAccess.HostPathPrefixes) > 0) {
		problems = append(problems, "hostAccess requires admin")
	}
	for i, rule := range spec.AllowedRules {
		if len(rule.Resources) == 0 || len(rule.Verbs) == 0 {
			problems = append(problems, fmt.Sprintf("allowedRules[%d] requires resources and verbs", i))
		}
	}
	if name := strings.TrimSpace(spec.Install.ServiceAccountName); name != "" {
		for _, issue := range k8svalidation.IsDNS1123Label(name) {
			problems = append(problems, "install.serviceAccountName "+issue)
		}
	}
	return problems
}

func effectiveRBACForAgentProfile(spec AgentProfileSpec) []string {
	out := []string{"profile:" + agenttemplate.NormalizePrivilegeProfile(spec.PrivilegeProfile)}
	if len(spec.NamespaceScope) > 0 {
		scopes := copyStringSlice(spec.NamespaceScope)
		sort.Strings(scopes)
		out = append(out, "namespaces:"+strings.Join(scopes, ","))
	}
	return out
}

// ---------------------------------------------------------------- helpers

func patchClusterStatus(ctx context.Context, c client.Client, object *Cluster, status ClusterStatus) error {
	before := object.DeepCopy()
	object.Status = status
	return c.Status().Patch(ctx, object, client.MergeFrom(before))
}

func patchProjectStatus(ctx context.Context, c client.Client, object *Project, status ProjectStatus) error {
	before := object.DeepCopy()
	object.Status = status
	return c.Status().Patch(ctx, object, client.MergeFrom(before))
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func objectReference(apiVersion, kind, namespace, name string, generation int64) ObjectRef {
	if apiVersion == "" {
		apiVersion = GroupVersion.String()
	}
	return ObjectRef{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name, Generation: generation}
}

func readyConditions(generation int64, message string) []metav1.Condition {
	now := metav1.Now()
	return []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: generation, LastTransitionTime: now, Reason: "Synchronized", Message: message}}
}

func failedConditions(generation int64, reason, message string) []metav1.Condition {
	now := metav1.Now()
	return []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, ObservedGeneration: generation, LastTransitionTime: now, Reason: reason, Message: message}}
}
