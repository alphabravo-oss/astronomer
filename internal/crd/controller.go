package crd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ClusterSync interface {
	EnsureFromCRD(context.Context, ClusterSpec) (ClusterStatus, error)
	DeleteByName(context.Context, string) error
}

type ProjectSync interface {
	EnsureFromCRD(context.Context, ProjectSpec) (ProjectStatus, error)
	DeleteByName(context.Context, string) error
}

type ObjectRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	Generation int64
}

type ClusterOwnershipSync interface {
	ValidateClusterOwnership(context.Context, ClusterSpec, ObjectRef) error
	RecordClusterOwnership(context.Context, ClusterSpec, ObjectRef) error
}

type ProjectOwnershipSync interface {
	ValidateProjectOwnership(context.Context, ProjectSpec, ObjectRef) error
	RecordProjectOwnership(context.Context, ProjectSpec, ObjectRef) error
}

var ErrInProgress = errors.New("crd: delete in progress")

type ControllerConfig struct {
	K8sConfig               *rest.Config
	WatchNamespace          string
	ClusterHandler          ClusterSync
	ProjectHandler          ProjectSync
	Log                     *slog.Logger
	PollPeriod              time.Duration
	LeaderElection          bool
	LeaderElectionNamespace string
}

const defaultPollPeriod = 60 * time.Second

func New(cfg ControllerConfig) (manager.Manager, error) {
	if cfg.K8sConfig == nil || cfg.ClusterHandler == nil || cfg.ProjectHandler == nil {
		return nil, errors.New("crd.New: Kubernetes config, cluster handler, and project handler are required")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	poll := cfg.PollPeriod
	if poll <= 0 {
		poll = defaultPollPeriod
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register core kinds: %w", err)
	}
	if err := AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register management kinds: %w", err)
	}
	opts := manager.Options{
		Scheme:                     scheme,
		Metrics:                    metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:     "0",
		LeaderElection:             cfg.LeaderElection,
		LeaderElectionNamespace:    cfg.LeaderElectionNamespace,
		LeaderElectionID:           "astronomer-management-crd-controller",
		LeaderElectionResourceLock: "leases",
	}
	if opts.LeaderElectionNamespace == "" {
		opts.LeaderElectionNamespace = cfg.WatchNamespace
	}
	if cfg.WatchNamespace != "" {
		opts.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{cfg.WatchNamespace: {}}}
	}
	mgr, err := ctrl.NewManager(cfg.K8sConfig, opts)
	if err != nil {
		return nil, fmt.Errorf("build CRD manager: %w", err)
	}
	controllers := []interface{ SetupWithManager(ctrl.Manager) error }{
		&ClusterReconciler{Client: mgr.GetClient(), Sync: cfg.ClusterHandler, Log: log.With("controller", "cluster"), Poll: poll},
		&ProjectReconciler{Client: mgr.GetClient(), Sync: cfg.ProjectHandler, Log: log.With("controller", "project"), Poll: poll},
		&AgentProfileReconciler{Client: mgr.GetClient(), Log: log.With("controller", "agentprofile"), Poll: poll},
	}
	for _, controller := range controllers {
		if err := controller.SetupWithManager(mgr); err != nil {
			return nil, err
		}
	}
	return mgr, nil
}

type ClusterReconciler struct {
	Client client.Client
	Sync   ClusterSync
	Log    *slog.Logger
	Poll   time.Duration
}

func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&Cluster{}).Complete(r)
}

func (r *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if r.Client == nil || r.Sync == nil {
		return reconcile.Result{}, errors.New("cluster reconciler is not configured")
	}
	var object Cluster
	if err := r.Client.Get(ctx, req.NamespacedName, &object); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if !object.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&object, FinalizerCluster) {
			return reconcile.Result{}, nil
		}
		name := firstNonBlank(object.Spec.Name, object.Name)
		if err := r.Sync.DeleteByName(ctx, name); err != nil {
			if errors.Is(err, ErrInProgress) {
				return reconcile.Result{RequeueAfter: pollOrDefault(r.Poll)}, nil
			}
			return reconcile.Result{}, err
		}
		controllerutil.RemoveFinalizer(&object, FinalizerCluster)
		return reconcile.Result{}, r.Client.Update(ctx, &object)
	}
	if !controllerutil.ContainsFinalizer(&object, FinalizerCluster) {
		controllerutil.AddFinalizer(&object, FinalizerCluster)
		if err := r.Client.Update(ctx, &object); err != nil {
			return reconcile.Result{}, err
		}
	}
	spec, err := r.clusterSpecForSync(ctx, object)
	if err != nil {
		return reconcile.Result{}, r.patchClusterFailure(ctx, &object, "InvalidAgentProfile", err)
	}
	ref := objectReference(object.APIVersion, "Cluster", object.Namespace, object.Name, object.Generation)
	if ownership, ok := r.Sync.(ClusterOwnershipSync); ok {
		if err := ownership.ValidateClusterOwnership(ctx, spec, ref); err != nil {
			return reconcile.Result{}, r.patchClusterFailure(ctx, &object, "OwnershipConflict", err)
		}
	}
	status, err := r.Sync.EnsureFromCRD(ctx, spec)
	if err != nil {
		return reconcile.Result{}, r.patchClusterFailure(ctx, &object, "SyncFailed", err)
	}
	if ownership, ok := r.Sync.(ClusterOwnershipSync); ok {
		if err := ownership.RecordClusterOwnership(ctx, spec, ref); err != nil {
			return reconcile.Result{}, r.patchClusterFailure(ctx, &object, "OwnershipWriteFailed", err)
		}
	}
	status.LastReconciled = metav1.Now()
	status.ObservedProjectRefs = copyStringSlice(spec.ProjectRefs)
	status.Conditions = readyConditions(object.Generation, "Cluster is synchronized")
	if err := patchClusterStatus(ctx, r.Client, &object, status); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: pollOrDefault(r.Poll)}, nil
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
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: object.Namespace, Name: ref}, &profile); err != nil {
		return spec, fmt.Errorf("resolve AgentProfile %s: %w", ref, err)
	}
	if problems := validateAgentProfileSpec(profile.Spec); len(problems) > 0 {
		return spec, fmt.Errorf("AgentProfile %s is invalid: %s", ref, strings.Join(problems, "; "))
	}
	spec.Agent.PrivilegeProfile = profile.Spec.PrivilegeProfile
	return spec, nil
}

func (r *ClusterReconciler) patchClusterFailure(ctx context.Context, object *Cluster, reason string, err error) error {
	status := object.Status
	status.LastReconciled = metav1.Now()
	status.Conditions = failedConditions(object.Generation, reason, err.Error())
	return patchClusterStatus(ctx, r.Client, object, status)
}

type ProjectReconciler struct {
	Client client.Client
	Sync   ProjectSync
	Log    *slog.Logger
	Poll   time.Duration
}

func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&Project{}).Complete(r)
}

func (r *ProjectReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if r.Client == nil || r.Sync == nil {
		return reconcile.Result{}, errors.New("project reconciler is not configured")
	}
	var object Project
	if err := r.Client.Get(ctx, req.NamespacedName, &object); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if !object.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&object, FinalizerProject) {
			return reconcile.Result{}, nil
		}
		if err := r.Sync.DeleteByName(ctx, firstNonBlank(object.Spec.Name, object.Name)); err != nil {
			return reconcile.Result{}, err
		}
		controllerutil.RemoveFinalizer(&object, FinalizerProject)
		return reconcile.Result{}, r.Client.Update(ctx, &object)
	}
	if !controllerutil.ContainsFinalizer(&object, FinalizerProject) {
		controllerutil.AddFinalizer(&object, FinalizerProject)
		if err := r.Client.Update(ctx, &object); err != nil {
			return reconcile.Result{}, err
		}
	}
	spec := object.Spec
	if strings.TrimSpace(spec.Name) == "" {
		spec.Name = object.Name
	}
	ref := objectReference(object.APIVersion, "Project", object.Namespace, object.Name, object.Generation)
	if ownership, ok := r.Sync.(ProjectOwnershipSync); ok {
		if err := ownership.ValidateProjectOwnership(ctx, spec, ref); err != nil {
			return reconcile.Result{}, r.patchFailure(ctx, &object, "OwnershipConflict", err)
		}
	}
	status, err := r.Sync.EnsureFromCRD(ctx, spec)
	if err != nil {
		return reconcile.Result{}, r.patchFailure(ctx, &object, "SyncFailed", err)
	}
	if ownership, ok := r.Sync.(ProjectOwnershipSync); ok {
		if err := ownership.RecordProjectOwnership(ctx, spec, ref); err != nil {
			return reconcile.Result{}, r.patchFailure(ctx, &object, "OwnershipWriteFailed", err)
		}
	}
	status.LastReconciled = metav1.Now()
	status.ObservedClusters = copyStringSlice(spec.Clusters)
	status.Conditions = readyConditions(object.Generation, "Project is synchronized")
	if err := patchProjectStatus(ctx, r.Client, &object, status); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: pollOrDefault(r.Poll)}, nil
}

func (r *ProjectReconciler) patchFailure(ctx context.Context, object *Project, reason string, err error) error {
	status := object.Status
	status.LastReconciled = metav1.Now()
	status.Conditions = failedConditions(object.Generation, reason, err.Error())
	return patchProjectStatus(ctx, r.Client, object, status)
}

type AgentProfileReconciler struct {
	Client client.Client
	Log    *slog.Logger
	Poll   time.Duration
}

func (r *AgentProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&AgentProfile{}).Complete(r)
}

func (r *AgentProfileReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if r.Client == nil {
		return reconcile.Result{}, errors.New("agent profile reconciler is not configured")
	}
	var object AgentProfile
	if err := r.Client.Get(ctx, req.NamespacedName, &object); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if !object.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&object, FinalizerAgentProfile) {
			controllerutil.RemoveFinalizer(&object, FinalizerAgentProfile)
			return reconcile.Result{}, r.Client.Update(ctx, &object)
		}
		return reconcile.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(&object, FinalizerAgentProfile) {
		controllerutil.AddFinalizer(&object, FinalizerAgentProfile)
		if err := r.Client.Update(ctx, &object); err != nil {
			return reconcile.Result{}, err
		}
	}
	before := object.DeepCopy()
	object.Status = buildAgentProfileStatus(object)
	if err := r.Client.Status().Patch(ctx, &object, client.MergeFrom(before)); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: pollOrDefault(r.Poll)}, nil
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
func pollOrDefault(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultPollPeriod
	}
	return value
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
