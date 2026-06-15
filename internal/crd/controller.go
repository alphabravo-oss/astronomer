package crd

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
	"github.com/santhosh-tekuri/jsonschema/v6"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

// ClusterSync is the narrow handler-facing interface the cluster reconciler
// depends on. Defined here (not in handler) so the controller stays trivially
// fakeable and so handler does not pick up a transitive dependency on
// controller-runtime.
//
// The server-side wiring (internal/server/crd_wiring.go) ships an adapter that
// translates between this shape and the existing ClusterQuerier /
// ProjectQuerier surfaces — the handlers themselves do not need to change.
type ClusterSync interface {
	// EnsureFromCRD upserts a cluster row to match the supplied spec and
	// returns the DB-side status the controller should reflect back onto the
	// CR's .status subresource. The implementation is expected to be
	// idempotent: calling it twice with the same spec is a no-op.
	EnsureFromCRD(ctx context.Context, spec ClusterSpec) (ClusterStatus, error)

	// DeleteByName starts the decommission flow for the named cluster.
	// Returns nil when the cluster is gone from the DB (the controller can
	// then drop the finalizer); returns an error when the work is still in
	// progress so the controller requeues without removing the finalizer.
	//
	// Implementations may also return a non-nil "in progress" sentinel —
	// see ErrInProgress — to distinguish a transient retry from a permanent
	// failure that should bubble to the user.
	DeleteByName(ctx context.Context, name string) error
}

// ProjectSync is the project-side equivalent of ClusterSync.
type ProjectSync interface {
	// EnsureFromCRD upserts a project row from the spec. The spec's first
	// Clusters[] entry is resolved to a cluster.id by the implementation;
	// the resolved ID is reflected back on the returned status.
	EnsureFromCRD(ctx context.Context, spec ProjectSpec) (ProjectStatus, error)

	// DeleteByName drops the named project. Project deletion is synchronous
	// today (no decommission CR), so a nil return means the row is gone.
	DeleteByName(ctx context.Context, name string) error
}

// ObjectRef identifies the Kubernetes object currently driving a DB row.
type ObjectRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	Generation int64
}

// ClusterOwnershipSync is an optional extension implemented by production
// sync adapters that can stamp DB ownership metadata after a successful sync.
type ClusterOwnershipSync interface {
	ValidateClusterOwnership(ctx context.Context, spec ClusterSpec, ref ObjectRef) error
	RecordClusterOwnership(ctx context.Context, spec ClusterSpec, ref ObjectRef) error
}

// ProjectOwnershipSync is the project-side ownership metadata hook.
type ProjectOwnershipSync interface {
	ValidateProjectOwnership(ctx context.Context, spec ProjectSpec, ref ObjectRef) error
	RecordProjectOwnership(ctx context.Context, spec ProjectSpec, ref ObjectRef) error
}

// ErrInProgress is the sentinel a Sync implementation can return when the
// delete work has been enqueued but is not yet complete. The controller
// requeues without removing the finalizer when it sees this error.
var ErrInProgress = errors.New("crd: delete in progress")

// ControllerConfig bundles the dependencies the manager needs.
//
// Lifecycle: the manager runs until ctx is cancelled (passed to
// manager.Start). On cancellation it waits for in-flight reconciles to drain
// before returning.
type ControllerConfig struct {
	// K8sConfig is the rest.Config used to build the manager's client. Always
	// produced from rest.InClusterConfig at server boot — the controller
	// expects to run inside a cluster.
	K8sConfig *rest.Config

	// WatchNamespace scopes the cache to a single namespace. Empty string
	// means cluster-scope watch (not recommended; the chart sets the value
	// to the management namespace).
	WatchNamespace string

	// ClusterHandler is the cluster-side sync entrypoint.
	ClusterHandler ClusterSync

	// ProjectHandler is the project-side sync entrypoint.
	ProjectHandler ProjectSync

	// Log is the slog logger used for controller-emitted lifecycle messages.
	// Required (nil-safe: defaults to slog.Default at New time).
	Log *slog.Logger

	// PollPeriod is the RequeueAfter cadence used for steady-state status
	// reconciles, i.e. the rate at which a Cluster/Project CR is re-examined
	// to pick up DB-side drift. Defaults to 60s when zero — see the spec's
	// "polling reconcile every 60s" guidance.
	PollPeriod time.Duration

	// LeaderElection enables controller-runtime lease coordination. Enable this
	// whenever the controller runs embedded in a Deployment with more than one
	// server replica, otherwise every server pod reconciles the same CRs.
	LeaderElection bool

	// LeaderElectionNamespace is where the Lease object is stored. Defaults to
	// WatchNamespace when empty. Required by controller-runtime for in-cluster
	// workloads that cannot infer a namespace.
	LeaderElectionNamespace string

	// ArgoNamespace is where Astronomer writes Argo CD ApplicationSets from
	// GitOpsTarget/ClusterBaseline CRDs. Defaults to "argocd".
	ArgoNamespace string
}

// defaultPollPeriod is the fallback for ControllerConfig.PollPeriod.
const defaultPollPeriod = 60 * time.Second

const finalizerTimeout = 15 * time.Minute

const defaultArgoNamespace = "argocd"

const applicationSetSpecHashAnnotation = "management.astronomer.io/desired-spec-sha1"

const componentBundleValuesSchemaDefaultKey = "values.schema.json"

const maxArgoApplicationResourceDetails = 50

var applicationSetGVK = kubeutil.ArgoApplicationSetGVK

var applicationGVK = kubeutil.ArgoApplicationGVK

var configMapGVK = kubeutil.ConfigMapGVK

const (
	gitOpsTemplateLabelKey   = "management.astronomer.io/gitops-template"
	gitOpsTemplateLabelValue = "true"
	gitOpsTemplateDataKey    = "template.json"
)

// New constructs a controller-runtime Manager wired with both reconcilers.
// The caller starts it via manager.Start(ctx); cancelling ctx drains in-flight
// reconciles and returns from Start.
func New(cfg ControllerConfig) (manager.Manager, error) {
	if cfg.K8sConfig == nil {
		return nil, errors.New("crd.New: K8sConfig is required")
	}
	if cfg.ClusterHandler == nil {
		return nil, errors.New("crd.New: ClusterHandler is required")
	}
	if cfg.ProjectHandler == nil {
		return nil, errors.New("crd.New: ProjectHandler is required")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	poll := cfg.PollPeriod
	if poll <= 0 {
		poll = defaultPollPeriod
	}
	argoNamespace := strings.TrimSpace(cfg.ArgoNamespace)
	if argoNamespace == "" {
		argoNamespace = defaultArgoNamespace
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("crd.New: register core kinds: %w", err)
	}
	if err := AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("crd.New: register CRD kinds: %w", err)
	}

	mgrOpts := manager.Options{
		Scheme: scheme,
		// We don't run the metrics endpoint inside the server pod — the
		// existing /metrics on the server already exposes prometheus via the
		// observability stack, and binding a second :8080 would conflict.
		// BindAddress "0" tells controller-runtime to skip starting the
		// metrics listener entirely.
		Metrics: metricsserver.Options{BindAddress: "0"},
		// Health-probe HTTP server is also unused: the server pod has its
		// own /health endpoint handled by chi. Empty BindAddress = disabled.
		HealthProbeBindAddress: "0",
		LeaderElection:         cfg.LeaderElection,
	}
	if cfg.LeaderElection {
		leaderNamespace := cfg.LeaderElectionNamespace
		if leaderNamespace == "" {
			leaderNamespace = cfg.WatchNamespace
		}
		mgrOpts.LeaderElectionNamespace = leaderNamespace
		mgrOpts.LeaderElectionID = "astronomer-management-crd-controller"
		mgrOpts.LeaderElectionResourceLock = "leases"
	}
	if ns := cfg.WatchNamespace; ns != "" {
		mgrOpts.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}}
	}

	mgr, err := ctrl.NewManager(cfg.K8sConfig, mgrOpts)
	if err != nil {
		return nil, fmt.Errorf("crd.New: build manager: %w", err)
	}

	cr := &ClusterReconciler{
		Client: mgr.GetClient(),
		Sync:   cfg.ClusterHandler,
		Log:    log.With("controller", "cluster"),
		Poll:   poll,
	}
	if err := cr.SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: cluster reconciler: %w", err)
	}

	pr := &ProjectReconciler{
		Client: mgr.GetClient(),
		Sync:   cfg.ProjectHandler,
		Log:    log.With("controller", "project"),
		Poll:   poll,
	}
	if err := pr.SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: project reconciler: %w", err)
	}

	if err := (&ClusterBaselineReconciler{Client: mgr.GetClient(), Reader: mgr.GetAPIReader(), Log: log.With("controller", "clusterbaseline"), Poll: poll, ArgoNamespace: argoNamespace}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: clusterbaseline reconciler: %w", err)
	}
	if err := (&ComponentBundleReconciler{Client: mgr.GetClient(), Log: log.With("controller", "componentbundle"), Poll: poll}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: componentbundle reconciler: %w", err)
	}
	if err := (&AgentProfileReconciler{Client: mgr.GetClient(), Log: log.With("controller", "agentprofile"), Poll: poll}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: agentprofile reconciler: %w", err)
	}
	if err := (&GitOpsTargetReconciler{Client: mgr.GetClient(), Reader: mgr.GetAPIReader(), Log: log.With("controller", "gitopstarget"), Poll: poll, ArgoNamespace: argoNamespace}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("crd.New: gitopstarget reconciler: %w", err)
	}

	return mgr, nil
}

// ClusterReconciler watches Cluster CRs and reflects their spec into the DB.
//
// Exported for the test package — the tests construct a Reconciler directly
// against a fake client rather than spinning a full manager.
type ClusterReconciler struct {
	Client client.Client
	Sync   ClusterSync
	Log    *slog.Logger
	// Poll is the steady-state RequeueAfter; zero falls back to defaultPollPeriod.
	Poll time.Duration
}

// SetupWithManager registers the reconciler with the manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&Cluster{}).
		Complete(r)
}

// Reconcile applies the CRD spec to the DB and writes status back.
//
// Steps:
//  1. Get the Cluster CR. On NotFound (CR already gone) return without error
//     — the finalizer path drops the finalizer on delete, so by the time we
//     see a NotFound the row is also gone.
//  2. If DeletionTimestamp is non-zero, run the decommission path. The
//     finalizer comes off only when the Sync.DeleteByName completes.
//  3. Otherwise install the finalizer (idempotent) and call Sync.EnsureFromCRD.
//  4. Patch .status with the DB-side outcome.
//  5. Requeue at the poll period to refresh status against DB-side drift.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.Log.With("name", req.Name, "namespace", req.Namespace)
	poll := r.Poll
	if poll <= 0 {
		poll = defaultPollPeriod
	}

	var obj Cluster
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Deletion path.
	if !obj.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&obj, FinalizerCluster) {
			// Finalizer already removed — nothing to do.
			return reconcile.Result{}, nil
		}
		name := obj.Spec.Name
		if name == "" {
			name = obj.Name
		}
		log.Info("crd_cluster_delete_starting", "cluster_name", name)
		if err := r.Sync.DeleteByName(ctx, name); err != nil {
			if errors.Is(err, ErrInProgress) {
				if finalizerTimeoutExceeded(&obj) {
					if patchErr := r.patchClusterFinalizerTimeoutStatus(ctx, &obj, name); patchErr != nil {
						return reconcile.Result{}, patchErr
					}
				}
				return reconcile.Result{RequeueAfter: poll}, nil
			}
			if finalizerTimeoutExceeded(&obj) {
				if patchErr := r.patchClusterFinalizerTimeoutStatus(ctx, &obj, name); patchErr != nil {
					return reconcile.Result{}, patchErr
				}
			}
			log.Warn("crd_cluster_delete_failed", "error", err)
			return reconcile.Result{}, err
		}
		// DB row is gone; drop the finalizer so the API server can complete
		// object deletion.
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.RemoveFinalizer(&obj, FinalizerCluster)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("remove finalizer: %w", err)
		}
		return reconcile.Result{}, nil
	}

	// Steady-state path: install finalizer + sync.
	if !controllerutil.ContainsFinalizer(&obj, FinalizerCluster) {
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.AddFinalizer(&obj, FinalizerCluster)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
	}

	ref := ObjectRef{
		APIVersion: GroupVersion.String(),
		Kind:       "Cluster",
		Namespace:  obj.Namespace,
		Name:       obj.Name,
		Generation: obj.Generation,
	}
	specForSync, err := r.clusterSpecForSync(ctx, obj)
	if err != nil {
		log.Warn("crd_cluster_agent_profile_invalid", "error", err)
		return reconcile.Result{}, err
	}
	if ownershipSync, ok := r.Sync.(ClusterOwnershipSync); ok {
		if err := ownershipSync.ValidateClusterOwnership(ctx, specForSync, ref); err != nil {
			log.Warn("crd_cluster_ownership_invalid", "error", err)
			return reconcile.Result{}, err
		}
	}
	status, err := r.Sync.EnsureFromCRD(ctx, specForSync)
	if err != nil {
		log.Warn("crd_cluster_sync_failed", "error", err)
		// Don't burn the queue on hard sync failures — exponential backoff.
		return reconcile.Result{}, err
	}
	if ownershipSync, ok := r.Sync.(ClusterOwnershipSync); ok {
		if err := ownershipSync.RecordClusterOwnership(ctx, specForSync, ref); err != nil {
			log.Warn("crd_cluster_ownership_failed", "error", err)
			return reconcile.Result{}, err
		}
	}
	if status.LastReconciled.IsZero() {
		status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	}

	// Echo spec.projectRefs onto status.observedProjectRefs so kubectl describe
	// shows what the controller picked up; the spec stays authoritative.
	if len(obj.Spec.ProjectRefs) > 0 {
		status.ObservedProjectRefs = append([]string(nil), obj.Spec.ProjectRefs...)
	}

	// Patch status if it actually changed. Avoids hot-looping the API server
	// on every poll when the DB state is steady.
	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

func (r *ClusterReconciler) patchClusterFinalizerTimeoutStatus(ctx context.Context, obj *Cluster, name string) error {
	patch := client.MergeFrom(obj.DeepCopy())
	obj.Status.Phase = "DeletingTimedOut"
	obj.Status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	obj.Status.Conditions = finalizerTimeoutConditions(obj.Generation, fmt.Sprintf("Cluster %s deletion has exceeded %s; finalizer remains until decommission completes.", name, finalizerTimeout))
	if err := r.Client.Status().Patch(ctx, obj, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("patch Cluster finalizer timeout status: %w", err)
	}
	return nil
}

func (r *ClusterReconciler) clusterSpecForSync(ctx context.Context, obj Cluster) (ClusterSpec, error) {
	var spec ClusterSpec
	obj.Spec.DeepCopyInto(&spec)
	ref := strings.TrimSpace(spec.Agent.ProfileRef)
	if ref == "" {
		return spec, nil
	}
	var profile AgentProfile
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: obj.Namespace, Name: ref}, &profile); err != nil {
		return spec, fmt.Errorf("resolve AgentProfile %s/%s: %w", obj.Namespace, ref, err)
	}
	if problems := validateAgentProfileSpec(profile.Spec); len(problems) > 0 {
		return spec, fmt.Errorf("AgentProfile %s/%s is invalid: %s", obj.Namespace, ref, strings.Join(problems, "; "))
	}
	spec.Agent.PrivilegeProfile = profile.Spec.PrivilegeProfile
	if spec.Annotations == nil {
		spec.Annotations = map[string]string{}
	}
	spec.Annotations["management.astronomer.io/agent-profile-ref"] = ref
	spec.Annotations["management.astronomer.io/agent-profile-api-version"] = GroupVersion.String()
	if image := strings.TrimSpace(profile.Spec.Install.Image); image != "" {
		spec.Annotations[agenttemplate.AgentImageAnnotation] = image
	}
	if serviceAccountName := strings.TrimSpace(profile.Spec.Install.ServiceAccountName); serviceAccountName != "" {
		spec.Annotations[agenttemplate.AgentServiceAccountNameAnnotation] = serviceAccountName
	}
	if len(profile.Spec.Install.PodLabels) > 0 {
		payload, err := json.Marshal(profile.Spec.Install.PodLabels)
		if err != nil {
			return spec, fmt.Errorf("marshal AgentProfile %s/%s install.podLabels: %w", obj.Namespace, ref, err)
		}
		spec.Annotations[agenttemplate.AgentPodLabelsAnnotation] = string(payload)
	}
	return spec, nil
}

// ProjectReconciler is the project-side equivalent of ClusterReconciler.
type ProjectReconciler struct {
	Client client.Client
	Sync   ProjectSync
	Log    *slog.Logger
	Poll   time.Duration
}

// SetupWithManager registers the reconciler with the manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&Project{}).
		Complete(r)
}

// Reconcile mirrors the Cluster reconciler shape against the Project CR.
func (r *ProjectReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.Log.With("name", req.Name, "namespace", req.Namespace)
	poll := r.Poll
	if poll <= 0 {
		poll = defaultPollPeriod
	}

	var obj Project
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !obj.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&obj, FinalizerProject) {
			return reconcile.Result{}, nil
		}
		name := obj.Spec.Name
		if name == "" {
			name = obj.Name
		}
		log.Info("crd_project_delete_starting", "project_name", name)
		if err := r.Sync.DeleteByName(ctx, name); err != nil {
			if errors.Is(err, ErrInProgress) {
				if finalizerTimeoutExceeded(&obj) {
					if patchErr := r.patchProjectFinalizerTimeoutStatus(ctx, &obj, name); patchErr != nil {
						return reconcile.Result{}, patchErr
					}
				}
				return reconcile.Result{RequeueAfter: poll}, nil
			}
			if finalizerTimeoutExceeded(&obj) {
				if patchErr := r.patchProjectFinalizerTimeoutStatus(ctx, &obj, name); patchErr != nil {
					return reconcile.Result{}, patchErr
				}
			}
			log.Warn("crd_project_delete_failed", "error", err)
			return reconcile.Result{}, err
		}
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.RemoveFinalizer(&obj, FinalizerProject)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("remove finalizer: %w", err)
		}
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&obj, FinalizerProject) {
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.AddFinalizer(&obj, FinalizerProject)
		if err := r.Client.Patch(ctx, &obj, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
	}

	ref := ObjectRef{
		APIVersion: GroupVersion.String(),
		Kind:       "Project",
		Namespace:  obj.Namespace,
		Name:       obj.Name,
		Generation: obj.Generation,
	}
	if ownershipSync, ok := r.Sync.(ProjectOwnershipSync); ok {
		if err := ownershipSync.ValidateProjectOwnership(ctx, obj.Spec, ref); err != nil {
			log.Warn("crd_project_ownership_invalid", "error", err)
			return reconcile.Result{}, err
		}
	}
	status, err := r.Sync.EnsureFromCRD(ctx, obj.Spec)
	if err != nil {
		log.Warn("crd_project_sync_failed", "error", err)
		return reconcile.Result{}, err
	}
	if ownershipSync, ok := r.Sync.(ProjectOwnershipSync); ok {
		if err := ownershipSync.RecordProjectOwnership(ctx, obj.Spec, ref); err != nil {
			log.Warn("crd_project_ownership_failed", "error", err)
			return reconcile.Result{}, err
		}
	}
	if status.LastReconciled.IsZero() {
		status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	}
	if len(obj.Spec.Clusters) > 0 {
		status.ObservedClusters = append([]string(nil), obj.Spec.Clusters...)
	}

	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

func (r *ProjectReconciler) patchProjectFinalizerTimeoutStatus(ctx context.Context, obj *Project, name string) error {
	patch := client.MergeFrom(obj.DeepCopy())
	obj.Status.Phase = "DeletingTimedOut"
	obj.Status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	obj.Status.Conditions = finalizerTimeoutConditions(obj.Generation, fmt.Sprintf("Project %s deletion has exceeded %s; finalizer remains until cleanup completes.", name, finalizerTimeout))
	if err := r.Client.Status().Patch(ctx, obj, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("patch Project finalizer timeout status: %w", err)
	}
	return nil
}

// NamespacedNameFromMeta is a small helper for callers building a
// reconcile.Request from a *metav1.ObjectMeta — saves importing types in the
// integration test glue.
func NamespacedNameFromMeta(meta metav1.Object) types.NamespacedName {
	return kubeutil.NamespacedNameFromMeta(meta)
}

// ClusterBaselineReconciler validates ClusterBaseline specs and materializes
// supported per-bundle Argo ApplicationSets.
type ClusterBaselineReconciler struct {
	Client        client.Client
	Reader        client.Reader
	Log           *slog.Logger
	Poll          time.Duration
	ArgoNamespace string
}

func (r *ClusterBaselineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ClusterBaseline{}).
		Complete(r)
}

func (r *ClusterBaselineReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	poll := pollOrDefault(r.Poll)
	var obj ClusterBaseline
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if done, err := r.reconcileClusterBaselineFinalizer(ctx, &obj); done || err != nil {
		return reconcile.Result{}, err
	}
	status := buildClusterBaselineStatus(obj)
	if shouldApplyClusterBaseline(obj, status) {
		applications, driftedApplicationSets, err := r.ensureClusterBaselineApplicationSets(ctx, obj)
		status.Applications = applications
		if err != nil {
			status.Phase = "Degraded"
			status.Conditions = standardCRDConditions(obj.Generation, "ApplicationSetApplyFailed", false, "ApplicationSetApplyFailed", err.Error())
		} else {
			status.Phase = "Ready"
			reason := "ApplicationSetsApplied"
			message := fmt.Sprintf("ClusterBaseline ApplicationSets have been applied; observed %d generated Applications.", clusterBaselineApplicationCount(applications))
			if driftedApplicationSets > 0 {
				reason = "ApplicationSetDriftRepaired"
				message = fmt.Sprintf("ClusterBaseline repaired %d drifted ApplicationSet specs; observed %d generated Applications.", driftedApplicationSets, clusterBaselineApplicationCount(applications))
			}
			status.Conditions = standardCRDConditions(obj.Generation, "Accepted", true, reason, message)
		}
	}
	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch ClusterBaseline status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

// ComponentBundleReconciler validates bundle source shape and required
// capability declarations so invalid catalog entries fail fast in status.
type ComponentBundleReconciler struct {
	Client client.Client
	Log    *slog.Logger
	Poll   time.Duration
}

func (r *ComponentBundleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ComponentBundle{}).
		Complete(r)
}

func (r *ComponentBundleReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	poll := pollOrDefault(r.Poll)
	var obj ComponentBundle
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if done, err := reconcileSimpleFinalizer(ctx, r.Client, &obj, FinalizerComponentBundle); done || err != nil {
		return reconcile.Result{}, err
	}
	status := buildComponentBundleStatus(obj)
	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch ComponentBundle status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

// AgentProfileReconciler validates agent security profiles and reports the
// implied RBAC/capability surface for UI and GitOps consumers.
type AgentProfileReconciler struct {
	Client client.Client
	Log    *slog.Logger
	Poll   time.Duration
}

func (r *AgentProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&AgentProfile{}).
		Complete(r)
}

func (r *AgentProfileReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	poll := pollOrDefault(r.Poll)
	var obj AgentProfile
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if done, err := reconcileSimpleFinalizer(ctx, r.Client, &obj, FinalizerAgentProfile); done || err != nil {
		return reconcile.Result{}, err
	}
	status := buildAgentProfileStatus(obj)
	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch AgentProfile status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

// GitOpsTargetReconciler validates Argo ApplicationSet targeting boundaries and
// materializes supported ApplicationSet specs.
type GitOpsTargetReconciler struct {
	Client        client.Client
	Reader        client.Reader
	Log           *slog.Logger
	Poll          time.Duration
	ArgoNamespace string
}

func (r *GitOpsTargetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&GitOpsTarget{}).
		Complete(r)
}

func (r *GitOpsTargetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	poll := pollOrDefault(r.Poll)
	var obj GitOpsTarget
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if done, err := r.reconcileGitOpsTargetFinalizer(ctx, &obj); done || err != nil {
		return reconcile.Result{}, err
	}
	status := buildGitOpsTargetStatus(obj)
	if shouldApplyGitOpsTarget(obj, status) {
		appSetName, driftedApplicationSet, err := r.ensureGitOpsTargetApplicationSet(ctx, obj)
		status.ApplicationSetName = appSetName
		if err != nil {
			status.Phase = "Degraded"
			status.Conditions = standardCRDConditions(obj.Generation, "ApplicationSetApplyFailed", false, "ApplicationSetApplyFailed", err.Error())
		} else {
			rollup, err := r.rollupGitOpsTargetApplications(ctx, obj)
			if err != nil {
				status.Phase = "Degraded"
				status.Conditions = standardCRDConditions(obj.Generation, "ApplicationStatusReadFailed", false, "ApplicationStatusReadFailed", err.Error())
			} else {
				status.SyncStatus = rollup.SyncStatus
				status.Health = rollup.Health
				status.ApplicationCount = int32(rollup.Count)
				status.Applications = rollup.Applications
				status.Phase = "Ready"
				reason := "ApplicationSetApplied"
				message := fmt.Sprintf("GitOpsTarget ApplicationSet has been applied; observed %d generated Applications.", rollup.Count)
				if driftedApplicationSet {
					reason = "ApplicationSetDriftRepaired"
					message = fmt.Sprintf("GitOpsTarget repaired drifted ApplicationSet spec; observed %d generated Applications.", rollup.Count)
				}
				status.Conditions = standardCRDConditions(obj.Generation, "Accepted", true, reason, message)
			}
		}
	}
	if !reflect.DeepEqual(obj.Status, status) {
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Status = status
		if err := r.Client.Status().Patch(ctx, &obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patch GitOpsTarget status: %w", err)
		}
	}
	return reconcile.Result{RequeueAfter: poll}, nil
}

func (r *GitOpsTargetReconciler) rollupGitOpsTargetApplications(ctx context.Context, obj GitOpsTarget) (argoApplicationRollup, error) {
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	return rollupArgoApplications(ctx, reader, argoNamespaceOrDefault(r.ArgoNamespace), crdSourceLabels("GitOpsTarget", obj.Namespace, obj.Name))
}

func (r *GitOpsTargetReconciler) reconcileGitOpsTargetFinalizer(ctx context.Context, obj *GitOpsTarget) (bool, error) {
	if !obj.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(obj, FinalizerGitOpsTarget) {
			return true, nil
		}
		if err := r.deleteGitOpsTargetApplicationSet(ctx, *obj); err != nil {
			if finalizerTimeoutExceeded(obj) {
				if patchErr := r.patchGitOpsTargetFinalizerTimeoutStatus(ctx, obj); patchErr != nil {
					return true, patchErr
				}
			}
			return true, err
		}
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.RemoveFinalizer(obj, FinalizerGitOpsTarget)
		if err := r.Client.Patch(ctx, obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return true, fmt.Errorf("remove GitOpsTarget finalizer: %w", err)
		}
		return true, nil
	}
	if controllerutil.ContainsFinalizer(obj, FinalizerGitOpsTarget) {
		return false, nil
	}
	patch := client.MergeFrom(obj.DeepCopy())
	controllerutil.AddFinalizer(obj, FinalizerGitOpsTarget)
	if err := r.Client.Patch(ctx, obj, patch); err != nil {
		return true, fmt.Errorf("add GitOpsTarget finalizer: %w", err)
	}
	return false, nil
}

func (r *GitOpsTargetReconciler) patchGitOpsTargetFinalizerTimeoutStatus(ctx context.Context, obj *GitOpsTarget) error {
	patch := client.MergeFrom(obj.DeepCopy())
	obj.Status.Phase = "DeletingTimedOut"
	obj.Status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	obj.Status.ApplicationSetName = gitOpsTargetApplicationSetName(*obj)
	obj.Status.Conditions = finalizerTimeoutConditions(obj.Generation, fmt.Sprintf("GitOpsTarget %s/%s cleanup has exceeded %s; finalizer remains until generated ApplicationSets are removed.", obj.Namespace, obj.Name, finalizerTimeout))
	if err := r.Client.Status().Patch(ctx, obj, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("patch GitOpsTarget finalizer timeout status: %w", err)
	}
	return nil
}

func (r *GitOpsTargetReconciler) deleteGitOpsTargetApplicationSet(ctx context.Context, obj GitOpsTarget) error {
	argoNamespace := strings.TrimSpace(r.ArgoNamespace)
	if argoNamespace == "" {
		argoNamespace = defaultArgoNamespace
	}
	appSet := kubeutil.NewUnstructured(applicationSetGVK, argoNamespace, gitOpsTargetApplicationSetName(obj))
	if err := r.Client.Delete(ctx, appSet); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete ApplicationSet %s/%s: %w", argoNamespace, appSet.GetName(), err)
	}
	return nil
}

func pollOrDefault(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return defaultPollPeriod
}

func reconcileSimpleFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) (bool, error) {
	if !obj.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(obj, finalizer) {
			return true, nil
		}
		patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
		controllerutil.RemoveFinalizer(obj, finalizer)
		if err := c.Patch(ctx, obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return true, fmt.Errorf("remove finalizer: %w", err)
		}
		return true, nil
	}
	if controllerutil.ContainsFinalizer(obj, finalizer) {
		return false, nil
	}
	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	controllerutil.AddFinalizer(obj, finalizer)
	if err := c.Patch(ctx, obj, patch); err != nil {
		return true, fmt.Errorf("add finalizer: %w", err)
	}
	return false, nil
}

func buildClusterBaselineStatus(obj ClusterBaseline) ClusterBaselineStatus {
	now := metav1.Time{Time: time.Now().UTC()}
	status := ClusterBaselineStatus{
		ObservedGeneration: obj.Generation,
		LastReconciled:     now,
		TargetedClusters:   append([]string(nil), obj.Spec.ClusterSelector.ClusterRefs...),
	}
	problems := validateClusterBaselineSpec(obj.Spec)
	if obj.Spec.Suspended {
		status.Phase = "Suspended"
		status.Conditions = suspendedCRDConditions(obj.Generation, "ClusterBaseline reconciliation is suspended.")
		return status
	}
	if len(problems) > 0 {
		status.Phase = "Degraded"
		status.Conditions = standardCRDConditions(obj.Generation, "ValidationFailed", false, "ValidationFailed", strings.Join(problems, "; "))
		return status
	}
	status.Phase = "Ready"
	status.Conditions = standardCRDConditions(obj.Generation, "Accepted", true, "ValidationSucceeded", "ClusterBaseline spec is accepted; supported bundle ApplicationSets will be applied.")
	return status
}

func shouldApplyClusterBaseline(obj ClusterBaseline, status ClusterBaselineStatus) bool {
	return !obj.Spec.Suspended && status.Phase == "Ready"
}

func (r *ClusterBaselineReconciler) reconcileClusterBaselineFinalizer(ctx context.Context, obj *ClusterBaseline) (bool, error) {
	if !obj.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(obj, FinalizerClusterBaseline) {
			return true, nil
		}
		if err := r.deleteClusterBaselineApplicationSets(ctx, *obj); err != nil {
			if finalizerTimeoutExceeded(obj) {
				if patchErr := r.patchClusterBaselineFinalizerTimeoutStatus(ctx, obj); patchErr != nil {
					return true, patchErr
				}
			}
			return true, err
		}
		patch := client.MergeFrom(obj.DeepCopy())
		controllerutil.RemoveFinalizer(obj, FinalizerClusterBaseline)
		if err := r.Client.Patch(ctx, obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return true, fmt.Errorf("remove ClusterBaseline finalizer: %w", err)
		}
		return true, nil
	}
	if controllerutil.ContainsFinalizer(obj, FinalizerClusterBaseline) {
		return false, nil
	}
	patch := client.MergeFrom(obj.DeepCopy())
	controllerutil.AddFinalizer(obj, FinalizerClusterBaseline)
	if err := r.Client.Patch(ctx, obj, patch); err != nil {
		return true, fmt.Errorf("add ClusterBaseline finalizer: %w", err)
	}
	return false, nil
}

func (r *ClusterBaselineReconciler) patchClusterBaselineFinalizerTimeoutStatus(ctx context.Context, obj *ClusterBaseline) error {
	patch := client.MergeFrom(obj.DeepCopy())
	obj.Status.Phase = "DeletingTimedOut"
	obj.Status.LastReconciled = metav1.Time{Time: time.Now().UTC()}
	obj.Status.Conditions = finalizerTimeoutConditions(obj.Generation, fmt.Sprintf("ClusterBaseline %s/%s cleanup has exceeded %s; finalizer remains until generated ApplicationSets are removed.", obj.Namespace, obj.Name, finalizerTimeout))
	if err := r.Client.Status().Patch(ctx, obj, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("patch ClusterBaseline finalizer timeout status: %w", err)
	}
	return nil
}

func (r *ClusterBaselineReconciler) ensureClusterBaselineApplicationSets(ctx context.Context, obj ClusterBaseline) ([]ClusterBaselineApplicationStatus, int, error) {
	argoNamespace := argoNamespaceOrDefault(r.ArgoNamespace)
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	desired := map[string]struct{}{}
	applications := []ClusterBaselineApplicationStatus{}
	driftedApplicationSets := 0
	for _, ref := range enabledClusterBaselineBundles(obj.Spec.Bundles) {
		appSet, err := clusterBaselineApplicationSetObject(ctx, reader, obj, ref, argoNamespace)
		if err != nil {
			return applications, driftedApplicationSets, err
		}
		desired[appSet.GetName()] = struct{}{}
		drifted, err := upsertApplicationSet(ctx, r.Client, reader, appSet)
		if err != nil {
			return applications, driftedApplicationSets, err
		}
		if drifted {
			driftedApplicationSets++
		}
		rollup, err := rollupArgoApplications(ctx, reader, appSet.GetNamespace(), appSet.GetLabels())
		if err != nil {
			return applications, driftedApplicationSets, err
		}
		applications = append(applications, ClusterBaselineApplicationStatus{
			Name:              appSet.GetName(),
			Namespace:         appSet.GetNamespace(),
			SyncStatus:        rollup.SyncStatus,
			Health:            rollup.Health,
			ApplicationCount:  int32(rollup.Count),
			ChildApplications: rollup.Applications,
		})
	}
	if err := r.deleteStaleClusterBaselineApplicationSets(ctx, obj, argoNamespace, desired); err != nil {
		return applications, driftedApplicationSets, err
	}
	return applications, driftedApplicationSets, nil
}

func (r *ClusterBaselineReconciler) deleteClusterBaselineApplicationSets(ctx context.Context, obj ClusterBaseline) error {
	return r.deleteStaleClusterBaselineApplicationSets(ctx, obj, argoNamespaceOrDefault(r.ArgoNamespace), nil)
}

func (r *ClusterBaselineReconciler) deleteStaleClusterBaselineApplicationSets(ctx context.Context, obj ClusterBaseline, argoNamespace string, desired map[string]struct{}) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(kubeutil.ListGVK(applicationSetGVK))
	if err := r.Client.List(ctx, list, client.InNamespace(argoNamespace), client.MatchingLabels(crdSourceLabels("ClusterBaseline", obj.Namespace, obj.Name))); err != nil {
		return fmt.Errorf("list ClusterBaseline ApplicationSets: %w", err)
	}
	for i := range list.Items {
		item := &list.Items[i]
		if desired != nil {
			if _, keep := desired[item.GetName()]; keep {
				continue
			}
		}
		item.SetGroupVersionKind(applicationSetGVK)
		if err := r.Client.Delete(ctx, item); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale ApplicationSet %s/%s: %w", item.GetNamespace(), item.GetName(), err)
		}
	}
	return nil
}

func clusterBaselineApplicationSetObject(ctx context.Context, reader client.Reader, obj ClusterBaseline, ref ClusterBaselineBundleRef, argoNamespace string) (*unstructured.Unstructured, error) {
	var bundle ComponentBundle
	if err := reader.Get(ctx, kubeutil.NamespacedName(obj.Namespace, ref.Name), &bundle); err != nil {
		return nil, fmt.Errorf("get ComponentBundle %s/%s: %w", obj.Namespace, ref.Name, err)
	}
	if problems := validateComponentBundleSpec(bundle.Spec); len(problems) > 0 {
		return nil, fmt.Errorf("ComponentBundle %s/%s is invalid: %s", obj.Namespace, ref.Name, strings.Join(problems, "; "))
	}
	resolvedSpec, err := resolveComponentBundleVersion(bundle, ref.Version)
	if err != nil {
		return nil, err
	}
	if err := validateClusterBaselineBundleValues(ctx, reader, bundle, resolvedSpec, ref.Values); err != nil {
		return nil, err
	}
	source, err := componentBundleApplicationSource(resolvedSpec)
	if err != nil {
		return nil, err
	}
	source, err = sourceWithBundleValues(source, ref.Values, ref.ValuesFrom)
	if err != nil {
		return nil, err
	}
	destinationNamespace := strutil.FirstNonBlankTrimmed(resolvedSpec.DefaultNamespace, "default")
	name := clusterBaselineApplicationSetName(obj, ref)
	labels := crdSourceLabels("ClusterBaseline", obj.Namespace, obj.Name)
	labels["astronomer.io/bundle-name"] = dnsLabel(ref.Name)
	templateSpec := map[string]any{
		"project": "default",
		"source":  source,
		"destination": map[string]any{
			"server":    "{{server}}",
			"namespace": destinationNamespace,
		},
		"syncPolicy": clusterBaselineApplicationSyncPolicy(obj.Spec.SyncPolicy, destinationNamespace),
	}
	object := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": applicationSetGVK.GroupVersion().String(),
			"kind":       applicationSetGVK.Kind,
			"metadata": map[string]any{
				"name":        name,
				"namespace":   argoNamespace,
				"labels":      stringMapToAny(labels),
				"annotations": crdSourceAnnotations("ClusterBaseline", obj.Namespace, obj.Name),
			},
			"spec": map[string]any{
				"generators": []any{
					map[string]any{
						"clusters": map[string]any{
							"selector": clusterBaselineClusterSelector(obj.Spec.ClusterSelector),
						},
					},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"name":   dnsLabel("astro", obj.Name, ref.Name) + "-{{nameNormalized}}",
						"labels": stringMapToAny(labels),
					},
					"spec": templateSpec,
				},
			},
		},
	}
	object.SetGroupVersionKind(applicationSetGVK)
	object.SetLabels(labels)
	object.SetAnnotations(mapStringAnyToString(crdSourceAnnotations("ClusterBaseline", obj.Namespace, obj.Name)))
	return object, nil
}

func enabledClusterBaselineBundles(refs []ClusterBaselineBundleRef) []ClusterBaselineBundleRef {
	out := make([]ClusterBaselineBundleRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Enabled != nil && !*ref.Enabled {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func clusterBaselineApplicationSetName(obj ClusterBaseline, ref ClusterBaselineBundleRef) string {
	return dnsLabel("astronomer-baseline", obj.Namespace, obj.Name, ref.Name)
}

func sourceWithBundleValues(source map[string]any, values map[string]string, valuesFrom []ClusterBaselineValuesSource) (map[string]any, error) {
	if len(values) == 0 && len(valuesFrom) == 0 {
		return source, nil
	}
	hasGitValueFiles := false
	for _, valuesSource := range valuesFrom {
		if strings.TrimSpace(valuesSource.Type) == "git" {
			hasGitValueFiles = true
			break
		}
	}
	if (len(values) > 0 || hasGitValueFiles) && strings.TrimSpace(fmt.Sprint(source["chart"])) == "" {
		return nil, errors.New("bundle values overrides are supported only for Helm chart sources")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parameters := make([]any, 0, len(keys))
	for _, key := range keys {
		parameters = append(parameters, map[string]any{"name": key, "value": values[key]})
	}
	helm, _ := source["helm"].(map[string]any)
	if helm == nil {
		helm = map[string]any{}
	}
	if len(parameters) > 0 {
		helm["parameters"] = parameters
	}
	valueFiles := existingHelmValueFiles(helm["valueFiles"])
	for _, valuesSource := range valuesFrom {
		if strings.TrimSpace(valuesSource.Type) == "git" {
			valueFiles = append(valueFiles, strings.TrimSpace(valuesSource.Path))
		}
	}
	if len(valueFiles) > 0 {
		helm["valueFiles"] = stringSliceToAny(valueFiles)
	}
	source["helm"] = helm
	return source, nil
}

func existingHelmValueFiles(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func clusterBaselineApplicationSyncPolicy(policy ClusterBaselineSyncPolicy, destinationNamespace string) map[string]any {
	out := map[string]any{}
	if policy.Automated || policy.Prune || policy.SelfHeal {
		out["automated"] = map[string]any{
			"prune":    policy.Prune,
			"selfHeal": policy.SelfHeal,
		}
	}
	if strings.TrimSpace(destinationNamespace) != "" {
		out["syncOptions"] = []any{"CreateNamespace=true"}
	}
	return out
}

func clusterBaselineClusterSelector(selector LabelSelectorSpec) map[string]any {
	matchLabels := map[string]any{
		"astronomer.io/managed-by": "astronomer",
	}
	for key, value := range selector.MatchLabels {
		matchLabels[key] = value
	}
	out := map[string]any{"matchLabels": matchLabels}
	if len(selector.ClusterRefs) > 0 {
		out["matchExpressions"] = []any{
			map[string]any{
				"key":      "astronomer.io/cluster-name",
				"operator": "In",
				"values":   stringSliceToAny(selector.ClusterRefs),
			},
		}
	}
	return out
}

func upsertApplicationSet(ctx context.Context, writer client.Client, reader client.Reader, appSet *unstructured.Unstructured) (bool, error) {
	desiredHash, err := annotateApplicationSetSpecHash(appSet)
	if err != nil {
		return false, err
	}
	current := kubeutil.NewUnstructured(applicationSetGVK, appSet.GetNamespace(), appSet.GetName())
	key := kubeutil.NamespacedNameFromMeta(appSet)
	if err := reader.Get(ctx, key, current); err != nil {
		if apierrors.IsNotFound(err) {
			if err := writer.Create(ctx, appSet); err != nil {
				return false, fmt.Errorf("create ApplicationSet %s/%s: %w", appSet.GetNamespace(), appSet.GetName(), err)
			}
			return false, nil
		}
		return false, fmt.Errorf("get ApplicationSet %s/%s: %w", appSet.GetNamespace(), appSet.GetName(), err)
	}
	if err := validateApplicationSetOwnership(current, appSet); err != nil {
		return false, err
	}
	currentHash, err := applicationSetSpecHash(current)
	if err != nil {
		return false, err
	}
	drifted := currentHash != desiredHash
	appSet.SetResourceVersion(current.GetResourceVersion())
	if err := writer.Update(ctx, appSet); err != nil {
		return false, fmt.Errorf("update ApplicationSet %s/%s: %w", appSet.GetNamespace(), appSet.GetName(), err)
	}
	return drifted, nil
}

func annotateApplicationSetSpecHash(appSet *unstructured.Unstructured) (string, error) {
	return kubeutil.AnnotateSpecHash(appSet, applicationSetSpecHashAnnotation)
}

func applicationSetSpecHash(appSet *unstructured.Unstructured) (string, error) {
	hash, err := kubeutil.SpecHash(appSet)
	if err != nil {
		return "", fmt.Errorf("hash ApplicationSet %s/%s spec: %w", appSet.GetNamespace(), appSet.GetName(), err)
	}
	return hash, nil
}

func validateApplicationSetOwnership(current, desired *unstructured.Unstructured) error {
	for _, key := range []string{
		"app.kubernetes.io/managed-by",
		"astronomer.io/crd-kind",
		"astronomer.io/crd-name",
		"astronomer.io/crd-namespace",
	} {
		if current.GetLabels()[key] != desired.GetLabels()[key] {
			return fmt.Errorf("refusing to update ApplicationSet %s/%s: existing resource is not owned by %s/%s %s",
				current.GetNamespace(), current.GetName(),
				desired.GetAnnotations()["management.astronomer.io/source-namespace"],
				desired.GetAnnotations()["management.astronomer.io/source-kind"],
				desired.GetAnnotations()["management.astronomer.io/source-name"],
			)
		}
	}
	for _, key := range []string{
		"management.astronomer.io/source-api-version",
		"management.astronomer.io/source-kind",
		"management.astronomer.io/source-namespace",
		"management.astronomer.io/source-name",
	} {
		if current.GetAnnotations()[key] != desired.GetAnnotations()[key] {
			return fmt.Errorf("refusing to update ApplicationSet %s/%s: existing resource has conflicting source annotation %s",
				current.GetNamespace(), current.GetName(), key)
		}
	}
	return nil
}

func argoNamespaceOrDefault(value string) string {
	if strings.TrimSpace(value) == "" {
		return defaultArgoNamespace
	}
	return strings.TrimSpace(value)
}

func crdSourceLabels(kind, namespace, name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "astronomer",
		"astronomer.io/crd-kind":       kind,
		"astronomer.io/crd-name":       dnsLabel(name),
		"astronomer.io/crd-namespace":  dnsLabel(namespace),
	}
}

func crdSourceAnnotations(kind, namespace, name string) map[string]any {
	return map[string]any{
		"management.astronomer.io/source-api-version": GroupVersion.String(),
		"management.astronomer.io/source-kind":        kind,
		"management.astronomer.io/source-namespace":   namespace,
		"management.astronomer.io/source-name":        name,
	}
}

func stringMapToAny(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mapStringAnyToString(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = fmt.Sprint(value)
	}
	return out
}

func stringSliceToAny(in []string) []any {
	out := make([]any, len(in))
	for i, value := range in {
		out[i] = value
	}
	return out
}

type argoApplicationRollup struct {
	SyncStatus   string
	Health       string
	Count        int
	Applications []ArgoApplicationStatus
}

func rollupArgoApplications(ctx context.Context, reader client.Reader, namespace string, labels map[string]string) (argoApplicationRollup, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(kubeutil.ListGVK(applicationGVK))
	opts := []client.ListOption{client.InNamespace(namespace)}
	if len(labels) > 0 {
		opts = append(opts, client.MatchingLabels(labels))
	}
	if err := reader.List(ctx, list, opts...); err != nil {
		return argoApplicationRollup{}, fmt.Errorf("list Argo Applications in namespace %s: %w", namespace, err)
	}
	if len(list.Items) == 0 {
		return argoApplicationRollup{SyncStatus: "Unknown", Health: "Unknown"}, nil
	}
	rollup := argoApplicationRollup{
		SyncStatus: "Synced",
		Health:     "Healthy",
		Count:      len(list.Items),
	}
	sort.Slice(list.Items, func(i, j int) bool {
		left := list.Items[i].GetNamespace() + "/" + list.Items[i].GetName()
		right := list.Items[j].GetNamespace() + "/" + list.Items[j].GetName()
		return left < right
	})
	for i := range list.Items {
		syncStatus, _, _ := unstructured.NestedString(list.Items[i].Object, "status", "sync", "status")
		healthStatus, _, _ := unstructured.NestedString(list.Items[i].Object, "status", "health", "status")
		rollup.SyncStatus = worseArgoSyncStatus(rollup.SyncStatus, syncStatus)
		rollup.Health = worseArgoHealthStatus(rollup.Health, healthStatus)
		rollup.Applications = append(rollup.Applications, argoApplicationStatusFromObject(list.Items[i]))
	}
	return rollup, nil
}

func argoApplicationStatusFromObject(obj unstructured.Unstructured) ArgoApplicationStatus {
	syncStatus, _, _ := unstructured.NestedString(obj.Object, "status", "sync", "status")
	revision, _, _ := unstructured.NestedString(obj.Object, "status", "sync", "revision")
	healthStatus, _, _ := unstructured.NestedString(obj.Object, "status", "health", "status")
	operationPhase, _, _ := unstructured.NestedString(obj.Object, "status", "operationState", "phase")
	message, _, _ := unstructured.NestedString(obj.Object, "status", "operationState", "message")
	return ArgoApplicationStatus{
		Name:           obj.GetName(),
		Namespace:      obj.GetNamespace(),
		SyncStatus:     syncStatus,
		Health:         healthStatus,
		Revision:       revision,
		OperationPhase: operationPhase,
		Message:        message,
		Resources:      argoApplicationResourceStatuses(obj),
	}
}

func argoApplicationResourceStatuses(obj unstructured.Unstructured) []ArgoApplicationResourceStatus {
	resources, found, err := unstructured.NestedSlice(obj.Object, "status", "resources")
	if err != nil || !found || len(resources) == 0 {
		return nil
	}
	if len(resources) > maxArgoApplicationResourceDetails {
		resources = resources[:maxArgoApplicationResourceDetails]
	}
	out := make([]ArgoApplicationResourceStatus, 0, len(resources))
	for _, resource := range resources {
		item, ok := resource.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, ArgoApplicationResourceStatus{
			Group:     stringFromAny(item["group"]),
			Kind:      stringFromAny(item["kind"]),
			Namespace: stringFromAny(item["namespace"]),
			Name:      stringFromAny(item["name"]),
			Status:    stringFromAny(item["status"]),
			Health:    stringFromAny(item["health"]),
		})
	}
	return out
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func worseArgoSyncStatus(current, next string) string {
	return worseByRank(current, next, map[string]int{
		"Synced":    1,
		"Unknown":   2,
		"OutOfSync": 3,
	})
}

func worseArgoHealthStatus(current, next string) string {
	return worseByRank(current, next, map[string]int{
		"Healthy":     1,
		"Unknown":     2,
		"Suspended":   3,
		"Progressing": 4,
		"Missing":     5,
		"Degraded":    6,
	})
}

func worseByRank(current, next string, ranks map[string]int) string {
	current = strutil.FirstNonBlankTrimmed(current, "Unknown")
	next = strutil.FirstNonBlankTrimmed(next, "Unknown")
	currentRank, currentKnown := ranks[current]
	nextRank, nextKnown := ranks[next]
	if !currentKnown {
		currentRank = ranks["Unknown"]
	}
	if !nextKnown {
		nextRank = ranks["Unknown"]
		next = "Unknown"
	}
	if nextRank > currentRank {
		return next
	}
	return current
}

func clusterBaselineApplicationCount(applications []ClusterBaselineApplicationStatus) int {
	total := 0
	for _, app := range applications {
		total += int(app.ApplicationCount)
	}
	return total
}

func validateClusterBaselineSpec(spec ClusterBaselineSpec) []string {
	var problems []string
	if len(spec.ClusterSelector.MatchLabels) == 0 && len(spec.ClusterSelector.ClusterRefs) == 0 {
		problems = append(problems, "clusterSelector.matchLabels or clusterSelector.clusterRefs is required")
	}
	if len(spec.ClusterSelector.MatchLabels) > 0 && spec.ClusterSelector.MatchLabels["astronomer.io/managed-by"] != "astronomer" {
		problems = append(problems, "clusterSelector.matchLabels must include astronomer.io/managed-by=astronomer")
	}
	if len(spec.Bundles) == 0 {
		problems = append(problems, "at least one bundle is required")
	}
	for i, bundle := range spec.Bundles {
		if strings.TrimSpace(bundle.Name) == "" {
			problems = append(problems, fmt.Sprintf("bundles[%d].name is required", i))
		}
		for j, source := range bundle.ValuesFrom {
			problems = append(problems, validateClusterBaselineValuesSource(source, fmt.Sprintf("bundles[%d].valuesFrom[%d]", i, j))...)
		}
	}
	return problems
}

func validateClusterBaselineValuesSource(source ClusterBaselineValuesSource, path string) []string {
	var problems []string
	switch strings.TrimSpace(source.Type) {
	case "git":
		if strings.TrimSpace(source.Path) == "" {
			problems = append(problems, path+".path is required for git values sources")
		}
		if unsafeRelativeValuesPath(source.Path) {
			problems = append(problems, path+".path must be a relative value-file path without traversal")
		}
		if strings.TrimSpace(source.Name) != "" || strings.TrimSpace(source.Key) != "" {
			problems = append(problems, path+".name and .key must be omitted for git values sources")
		}
	case "secret", "configMap":
		if strings.TrimSpace(source.Name) == "" {
			problems = append(problems, path+".name is required for "+source.Type+" values sources")
		} else if errs := k8svalidation.IsDNS1123Subdomain(strings.TrimSpace(source.Name)); len(errs) > 0 {
			problems = append(problems, path+".name must be a valid Kubernetes resource name")
		}
		if strings.TrimSpace(source.Path) != "" {
			problems = append(problems, path+".path must be omitted for "+source.Type+" values sources")
		}
		if strings.TrimSpace(source.Key) != "" {
			if errs := k8svalidation.IsConfigMapKey(strings.TrimSpace(source.Key)); len(errs) > 0 {
				problems = append(problems, path+".key must be a valid Kubernetes Secret/ConfigMap key")
			}
		}
	default:
		problems = append(problems, path+".type must be one of git, secret, or configMap")
	}
	return problems
}

func unsafeRelativeValuesPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return true
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == "" || part == "." || part == ".." {
			return true
		}
	}
	return false
}

func buildComponentBundleStatus(obj ComponentBundle) ComponentBundleStatus {
	now := metav1.Time{Time: time.Now().UTC()}
	status := ComponentBundleStatus{
		ObservedGeneration: obj.Generation,
		LastReconciled:     now,
		ResolvedRevision:   obj.Spec.Source.TargetRevision,
		AvailableVersions:  componentBundleAvailableVersions(obj.Spec),
	}
	problems := validateComponentBundleSpec(obj.Spec)
	if len(problems) > 0 {
		status.Phase = "Invalid"
		status.Conditions = standardCRDConditions(obj.Generation, "ValidationFailed", false, "ValidationFailed", strings.Join(problems, "; "))
		return status
	}
	status.Phase = "Valid"
	status.Conditions = standardCRDConditions(obj.Generation, "Accepted", true, "ValidationSucceeded", "ComponentBundle source and reference shapes are valid; resolvedRevision reflects source.targetRevision.")
	return status
}

func validateComponentBundleSpec(spec ComponentBundleSpec) []string {
	var problems []string
	problems = append(problems, validateComponentBundleSingleVersionSpec(spec, "")...)
	seenVersions := map[string]string{}
	if version := strings.TrimSpace(spec.Version); version != "" {
		seenVersions[version] = "spec.version"
	}
	for i, version := range spec.Versions {
		path := fmt.Sprintf("versions[%d]", i)
		resolved := componentBundleSpecFromVersion(spec, version)
		problems = append(problems, validateComponentBundleSingleVersionSpec(resolved, path)...)
		versionName := strings.TrimSpace(version.Version)
		if versionName == "" {
			continue
		}
		if previousPath, ok := seenVersions[versionName]; ok {
			problems = append(problems, fmt.Sprintf("%s.version duplicates %s", path, previousPath))
			continue
		}
		seenVersions[versionName] = path + ".version"
	}
	return problems
}

func validateComponentBundleSingleVersionSpec(spec ComponentBundleSpec, path string) []string {
	var problems []string
	if strings.TrimSpace(spec.Version) == "" {
		problems = append(problems, fieldPath(path, "version")+" is required")
	}
	problems = append(problems, validateComponentBundleSourceSpec(spec.Source, fieldPath(path, "source"))...)
	for i, req := range spec.CapabilityRequirements {
		if strings.TrimSpace(req.Feature) == "" {
			problems = append(problems, fmt.Sprintf("%s[%d].feature is required", fieldPath(path, "capabilityRequirements"), i))
		}
	}
	for i, check := range spec.HealthChecks {
		switch strings.TrimSpace(check.Type) {
		case "argocd", "http", "kubernetes", "prometheus":
		default:
			problems = append(problems, fmt.Sprintf("%s[%d].type must be one of argocd, http, kubernetes, prometheus", fieldPath(path, "healthChecks"), i))
		}
	}
	return problems
}

func validateComponentBundleSourceSpec(source ComponentBundleSourceSpec, path string) []string {
	var problems []string
	sourceType := strings.TrimSpace(source.Type)
	switch sourceType {
	case "helm":
		if strings.TrimSpace(source.RepoURL) == "" {
			problems = append(problems, fieldPath(path, "repoURL")+" is required for helm bundles")
		}
		if strings.TrimSpace(source.Chart) == "" {
			problems = append(problems, fieldPath(path, "chart")+" is required for helm bundles")
		}
	case "kustomize", "git-path", "raw":
		if strings.TrimSpace(source.RepoURL) == "" {
			problems = append(problems, fieldPath(path, "repoURL")+" is required for git-backed bundles")
		}
		if strings.TrimSpace(source.Path) == "" {
			problems = append(problems, fieldPath(path, "path")+" is required for git-backed bundles")
		}
	default:
		problems = append(problems, fieldPath(path, "type")+" must be one of helm, kustomize, git-path, raw")
	}
	problems = append(problems, validateComponentBundleValuesSchemaRefForPath(source.ValuesSchemaRef, fieldPath(path, "valuesSchemaRef"))...)
	for i, ref := range source.SecretRefs {
		refPath := fmt.Sprintf("%s[%d]", fieldPath(path, "secretRefs"), i)
		if strings.TrimSpace(ref.Name) == "" {
			problems = append(problems, refPath+".name is required")
		} else {
			for _, msg := range k8svalidation.IsDNS1123Subdomain(strings.TrimSpace(ref.Name)) {
				problems = append(problems, refPath+".name "+msg)
			}
		}
		if strings.TrimSpace(ref.Namespace) != "" {
			problems = append(problems, refPath+".namespace must be omitted; ComponentBundle secretRefs are same-namespace only")
		}
		if strings.TrimSpace(ref.Key) != "" {
			for _, msg := range k8svalidation.IsConfigMapKey(strings.TrimSpace(ref.Key)) {
				problems = append(problems, refPath+".key "+msg)
			}
		}
	}
	return problems
}

func fieldPath(parent, field string) string {
	if strings.TrimSpace(parent) == "" {
		return field
	}
	return parent + "." + field
}

func validateComponentBundleValuesSchemaRefForPath(ref, path string) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	var problems []string
	if strings.Contains(ref, "://") {
		problems = append(problems, path+" must reference a same-namespace ConfigMap as name or name/key, not a URL")
	}
	if strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "..") {
		problems = append(problems, path+" must be a relative name or name/key reference")
	}
	parts := strings.Split(ref, "/")
	if len(parts) > 2 {
		problems = append(problems, path+" must have at most one slash")
	}
	if len(parts) >= 1 {
		for _, msg := range k8svalidation.IsDNS1123Subdomain(parts[0]) {
			problems = append(problems, path+" name "+msg)
		}
	}
	if len(parts) == 2 {
		for _, msg := range k8svalidation.IsConfigMapKey(parts[1]) {
			problems = append(problems, path+" key "+msg)
		}
	}
	return problems
}

func validateClusterBaselineBundleValues(ctx context.Context, reader client.Reader, bundle ComponentBundle, spec ComponentBundleSpec, values map[string]string) error {
	ref := strings.TrimSpace(spec.Source.ValuesSchemaRef)
	if ref == "" {
		return nil
	}
	schemaDoc, err := resolveComponentBundleValuesSchema(ctx, reader, bundle.Namespace, ref)
	if err != nil {
		return err
	}
	compiled, err := compileComponentBundleValuesSchema(bundle, schemaDoc)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	if err := compiled.Validate(helmParameterValuesObject(values)); err != nil {
		return fmt.Errorf("ComponentBundle %s/%s values do not match source.valuesSchemaRef %q: %w", bundle.Namespace, bundle.Name, ref, err)
	}
	return nil
}

func resolveComponentBundleValuesSchema(ctx context.Context, reader client.Reader, namespace, ref string) (string, error) {
	name, key := componentBundleValuesSchemaRefParts(ref)
	if name == "" {
		return "", fmt.Errorf("source.valuesSchemaRef %q is invalid", ref)
	}
	obj := kubeutil.NewUnstructured(configMapGVK, namespace, name)
	if err := reader.Get(ctx, kubeutil.NamespacedName(namespace, name), obj); err != nil {
		return "", fmt.Errorf("get values schema ConfigMap %s/%s: %w", namespace, name, err)
	}
	data, found, err := unstructured.NestedStringMap(obj.Object, "data")
	if err != nil {
		return "", fmt.Errorf("read values schema ConfigMap %s/%s data: %w", namespace, name, err)
	}
	if !found {
		return "", fmt.Errorf("values schema ConfigMap %s/%s must include data.%s", namespace, name, key)
	}
	schemaDoc := strings.TrimSpace(data[key])
	if schemaDoc == "" {
		return "", fmt.Errorf("values schema ConfigMap %s/%s must include data.%s", namespace, name, key)
	}
	return schemaDoc, nil
}

func compileComponentBundleValuesSchema(bundle ComponentBundle, schemaDoc string) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaDoc))
	if err != nil {
		return nil, fmt.Errorf("parse ComponentBundle %s/%s values schema: %w", bundle.Namespace, bundle.Name, err)
	}
	compiler := jsonschema.NewCompiler()
	schemaURL := "componentbundle-values-schema.json"
	if err := compiler.AddResource(schemaURL, doc); err != nil {
		return nil, fmt.Errorf("load ComponentBundle %s/%s values schema: %w", bundle.Namespace, bundle.Name, err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile ComponentBundle %s/%s values schema: %w", bundle.Namespace, bundle.Name, err)
	}
	return compiled, nil
}

func componentBundleValuesSchemaRefParts(ref string) (string, string) {
	parts := strings.Split(strings.TrimSpace(ref), "/")
	if len(parts) == 0 || len(parts) > 2 {
		return "", ""
	}
	key := componentBundleValuesSchemaDefaultKey
	if len(parts) == 2 {
		key = parts[1]
	}
	return parts[0], key
}

func helmParameterValuesObject(values map[string]string) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		setNestedHelmParameterValue(out, key, parseHelmParameterValue(values[key]))
	}
	return out
}

func setNestedHelmParameterValue(root map[string]any, key string, value any) {
	parts := strings.Split(strings.TrimSpace(key), ".")
	current := root
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		next, _ := current[part].(map[string]any)
		if next == nil {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	leaf := strings.TrimSpace(parts[len(parts)-1])
	if leaf == "" {
		return
	}
	current[leaf] = value
}

func parseHelmParameterValue(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "null" {
		return nil
	}
	if parsed, err := strconv.ParseBool(trimmed); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return parsed
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return parsed
		}
	}
	return value
}

func buildAgentProfileStatus(obj AgentProfile) AgentProfileStatus {
	now := metav1.Time{Time: time.Now().UTC()}
	status := AgentProfileStatus{
		ObservedGeneration: obj.Generation,
		LastReconciled:     now,
		EffectiveRBAC:      effectiveRBACForAgentProfile(obj.Spec),
	}
	problems := validateAgentProfileSpec(obj.Spec)
	if len(problems) > 0 {
		status.Phase = "Invalid"
		status.Conditions = standardCRDConditions(obj.Generation, "ValidationFailed", false, "ValidationFailed", strings.Join(problems, "; "))
		return status
	}
	status.Phase = "Ready"
	status.Conditions = standardCRDConditions(obj.Generation, "Accepted", true, "ValidationSucceeded", "AgentProfile spec is accepted; Cluster profileRef projection and install metadata projection are supported.")
	return status
}

func validateAgentProfileSpec(spec AgentProfileSpec) []string {
	profile := strings.TrimSpace(spec.PrivilegeProfile)
	switch profile {
	case "viewer", "operator", "namespace-viewer", "namespace-operator", "custom", "admin":
	default:
		return []string{"privilegeProfile must be one of viewer, operator, namespace-viewer, namespace-operator, custom, admin"}
	}
	if (profile == "namespace-viewer" || profile == "namespace-operator") && len(spec.NamespaceScope) == 0 {
		return []string{"namespaceScope is required for namespace-scoped profiles"}
	}
	var problems []string
	if profile != "admin" && hasAgentProfileHostAccess(spec.HostAccess) {
		problems = append(problems, "hostAccess is only allowed for admin profiles")
	}
	for i, rule := range spec.AllowedRules {
		if len(rule.Resources) == 0 {
			problems = append(problems, fmt.Sprintf("allowedRules[%d].resources is required", i))
		}
		if len(rule.Verbs) == 0 {
			problems = append(problems, fmt.Sprintf("allowedRules[%d].verbs is required", i))
		}
	}
	problems = append(problems, validateAgentProfileAllowedRules(profile, spec.AllowedRules)...)
	problems = append(problems, validateAgentProfileCapabilities(spec)...)
	switch strings.TrimSpace(spec.NetworkEgress.Mode) {
	case "", "default", "restricted", "blocked":
	case "custom":
		if len(spec.NetworkEgress.AllowedCIDRs) == 0 && len(spec.NetworkEgress.AllowedHosts) == 0 {
			problems = append(problems, "networkEgress.allowedCIDRs or networkEgress.allowedHosts is required when mode is custom")
		}
	default:
		problems = append(problems, "networkEgress.mode must be one of default, restricted, blocked, custom")
	}
	if strings.ContainsAny(spec.Install.Image, "\r\n\t") {
		problems = append(problems, "install.image must not contain control whitespace")
	}
	if serviceAccountName := strings.TrimSpace(spec.Install.ServiceAccountName); serviceAccountName != "" {
		for _, msg := range k8svalidation.IsDNS1123Label(serviceAccountName) {
			problems = append(problems, "install.serviceAccountName "+msg)
		}
	}
	for key, value := range spec.Install.PodLabels {
		for _, msg := range k8svalidation.IsQualifiedName(key) {
			problems = append(problems, fmt.Sprintf("install.podLabels[%q] key %s", key, msg))
		}
		for _, msg := range k8svalidation.IsValidLabelValue(value) {
			problems = append(problems, fmt.Sprintf("install.podLabels[%q] value %s", key, msg))
		}
	}
	return problems
}

func validateAgentProfileCapabilities(spec AgentProfileSpec) []string {
	if len(spec.Capabilities) == 0 {
		return nil
	}
	allowed := agentProfileAllowedCapabilities(spec)
	var problems []string
	for raw, enabled := range spec.Capabilities {
		capability := normalizeAgentCapability(raw)
		if capability == "" {
			problems = append(problems, "capabilities keys must not be empty")
			continue
		}
		if !knownAgentCapability(capability) {
			problems = append(problems, fmt.Sprintf("capabilities[%q] is not a supported capability", raw))
			continue
		}
		if enabled && !allowed[capability] {
			problems = append(problems, fmt.Sprintf("capabilities[%q] is not permitted by privilegeProfile %s", raw, agenttemplate.NormalizePrivilegeProfile(spec.PrivilegeProfile)))
		}
	}
	return problems
}

func agentProfileAllowedCapabilities(spec AgentProfileSpec) map[string]bool {
	allowed := map[string]bool{}
	enable := func(names ...string) {
		for _, name := range names {
			allowed[name] = true
		}
	}
	switch agenttemplate.NormalizePrivilegeProfile(spec.PrivilegeProfile) {
	case agenttemplate.PrivilegeProfileViewer:
		enable("watch", "logs", "cluster_scope", "capability_inference")
	case agenttemplate.PrivilegeProfileOperator:
		enable("watch", "logs", "exec", "helm", "service_proxy", "mutate", "secrets", "cluster_scope", "capability_inference")
	case agenttemplate.PrivilegeProfileNamespaceViewer:
		enable("watch", "logs", "namespace_scoped", "capability_inference")
	case agenttemplate.PrivilegeProfileNamespaceOperator:
		enable("watch", "logs", "exec", "service_proxy", "mutate", "namespace_scoped", "capability_inference")
	case agenttemplate.PrivilegeProfileCustom:
		enable("custom_rbac")
		for name := range inferCapabilitiesFromAllowedRules(spec.AllowedRules) {
			allowed[name] = true
		}
	default:
		enable("cluster_admin", "watch", "logs", "exec", "helm", "service_proxy", "mutate", "secrets", "rbac", "cluster_scope", "namespace_scoped", "capability_inference")
	}
	return allowed
}

func validateAgentProfileAllowedRules(profile string, rules []AgentProfilePolicyRule) []string {
	normalized := agenttemplate.NormalizePrivilegeProfile(profile)
	if normalized == agenttemplate.PrivilegeProfileAdmin || normalized == agenttemplate.PrivilegeProfileCustom {
		return nil
	}
	var problems []string
	for i, rule := range rules {
		if containsAgentRuleValue(rule.APIGroups, "*") {
			problems = append(problems, fmt.Sprintf("allowedRules[%d].apiGroups must not use wildcard unless privilegeProfile is admin or custom", i))
		}
		for _, verb := range rule.Verbs {
			if containsAgentRuleValue([]string{verb}, "*") {
				problems = append(problems, fmt.Sprintf("allowedRules[%d].verbs must not use wildcard unless privilegeProfile is admin or custom", i))
				continue
			}
			if !agentProfileAllowsVerb(normalized, verb) {
				problems = append(problems, fmt.Sprintf("allowedRules[%d].verbs contains %q which is not permitted by privilegeProfile %s", i, verb, normalized))
			}
		}
		for _, resource := range rule.Resources {
			if containsAgentRuleValue([]string{resource}, "*") {
				problems = append(problems, fmt.Sprintf("allowedRules[%d].resources must not use wildcard unless privilegeProfile is admin or custom", i))
				continue
			}
			if !agentProfileAllowsResource(normalized, resource) {
				problems = append(problems, fmt.Sprintf("allowedRules[%d].resources contains %q which is not permitted by privilegeProfile %s", i, resource, normalized))
			}
		}
	}
	return problems
}

func agentProfileAllowsVerb(profile, verb string) bool {
	normalizedVerb := strings.ToLower(strings.TrimSpace(verb))
	switch profile {
	case agenttemplate.PrivilegeProfileViewer, agenttemplate.PrivilegeProfileNamespaceViewer:
		switch normalizedVerb {
		case "get", "list", "watch":
			return true
		default:
			return false
		}
	default:
		switch normalizedVerb {
		case "get", "list", "watch", "create", "update", "patch", "delete":
			return true
		default:
			return false
		}
	}
}

func agentProfileAllowsResource(profile, resource string) bool {
	normalizedResource := strings.ToLower(strings.TrimSpace(resource))
	if normalizedResource == "" {
		return false
	}
	if agentProfileIsClusterAdminResource(normalizedResource) {
		return false
	}
	switch profile {
	case agenttemplate.PrivilegeProfileViewer:
		return !agentProfileIsPrivilegedResource(normalizedResource)
	case agenttemplate.PrivilegeProfileOperator:
		return true
	case agenttemplate.PrivilegeProfileNamespaceViewer:
		return !agentProfileIsPrivilegedResource(normalizedResource) && !agentProfileIsClusterScopedResource(normalizedResource)
	case agenttemplate.PrivilegeProfileNamespaceOperator:
		return normalizedResource != "secrets" && !agentProfileIsClusterScopedResource(normalizedResource)
	default:
		return false
	}
}

func agentProfileIsPrivilegedResource(resource string) bool {
	switch resource {
	case "secrets", "pods/exec", "pods/attach", "pods/portforward", "services/proxy":
		return true
	default:
		return false
	}
}

func agentProfileIsClusterAdminResource(resource string) bool {
	switch resource {
	case "customresourcedefinitions", "clusterroles", "clusterrolebindings", "validatingwebhookconfigurations", "mutatingwebhookconfigurations", "storageclasses":
		return true
	default:
		return false
	}
}

func agentProfileIsClusterScopedResource(resource string) bool {
	if agentProfileIsClusterAdminResource(resource) {
		return true
	}
	switch resource {
	case "namespaces", "nodes", "persistentvolumes", "componentstatuses", "apiservices":
		return true
	default:
		return false
	}
}

func inferCapabilitiesFromAllowedRules(rules []AgentProfilePolicyRule) map[string]bool {
	out := map[string]bool{}
	for _, rule := range rules {
		for _, resource := range rule.Resources {
			normalizedResource := strings.ToLower(strings.TrimSpace(resource))
			if normalizedResource == "" {
				continue
			}
			if normalizedResource == "*" {
				out["cluster_scope"] = true
				out["rbac"] = true
				out["secrets"] = true
				if agentRuleHasReadVerb(rule.Verbs) {
					out["watch"] = true
					out["logs"] = true
				}
				if agentRuleHasMutatingVerb(rule.Verbs) {
					out["exec"] = true
					out["helm"] = true
					out["service_proxy"] = true
					out["mutate"] = true
				}
				if containsAgentRuleValue(rule.Verbs, "*") {
					out["cluster_admin"] = true
				}
				continue
			}
			if agentProfileIsClusterScopedResource(normalizedResource) {
				out["cluster_scope"] = true
			}
			if agentProfileIsRBACResource(normalizedResource) {
				out["rbac"] = true
			}
			if normalizedResource == "secrets" {
				out["secrets"] = true
			}
			if normalizedResource == "pods/log" {
				out["logs"] = true
			}
			if normalizedResource == "pods/exec" || normalizedResource == "pods/attach" {
				out["exec"] = true
			}
			if normalizedResource == "pods/portforward" || normalizedResource == "services/proxy" {
				out["service_proxy"] = true
			}
			for _, verb := range rule.Verbs {
				normalizedVerb := strings.ToLower(strings.TrimSpace(verb))
				if normalizedVerb == "get" || normalizedVerb == "list" || normalizedVerb == "watch" || normalizedVerb == "*" {
					out["watch"] = true
				}
				if normalizedVerb == "create" || normalizedVerb == "update" || normalizedVerb == "patch" || normalizedVerb == "delete" || normalizedVerb == "*" {
					out["mutate"] = true
				}
			}
		}
	}
	return out
}

func agentRuleHasReadVerb(verbs []string) bool {
	for _, verb := range verbs {
		switch strings.ToLower(strings.TrimSpace(verb)) {
		case "get", "list", "watch", "*":
			return true
		}
	}
	return false
}

func agentRuleHasMutatingVerb(verbs []string) bool {
	for _, verb := range verbs {
		switch strings.ToLower(strings.TrimSpace(verb)) {
		case "create", "update", "patch", "delete", "*":
			return true
		}
	}
	return false
}

func agentProfileIsRBACResource(resource string) bool {
	switch resource {
	case "roles", "rolebindings", "clusterroles", "clusterrolebindings":
		return true
	default:
		return false
	}
}

func containsAgentRuleValue(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func knownAgentCapability(capability string) bool {
	switch capability {
	case "watch", "logs", "exec", "shell", "helm", "service_proxy", "mutate", "secrets", "rbac", "cluster_scope", "namespace_scoped", "cluster_admin", "custom_rbac", "capability_inference":
		return true
	default:
		return false
	}
}

func normalizeAgentCapability(capability string) string {
	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(capability)))
	if normalized == "shell" {
		return "exec"
	}
	return normalized
}

func hasAgentProfileHostAccess(spec AgentProfileHostAccessSpec) bool {
	return spec.HostNetwork || spec.HostPID || len(spec.HostPathPrefixes) > 0
}

func effectiveRBACForAgentProfile(spec AgentProfileSpec) []string {
	profile := strings.TrimSpace(spec.PrivilegeProfile)
	out := []string{}
	switch profile {
	case "viewer":
		out = append(out, "cluster:get/list/watch core workload resources", "cluster:get/list/watch logs", "deny:secrets", "deny:mutations", "deny:exec")
	case "operator":
		out = append(out, "cluster:get/list/watch core workload resources", "cluster:mutate workloads", "cluster:exec/logs/service-proxy", "deny:cluster-admin")
	case "namespace-viewer":
		out = append(out, "namespace:get/list/watch core workload resources", "namespace:get/list/watch logs", "deny:cluster-scope", "deny:mutations", "deny:exec")
	case "namespace-operator":
		out = append(out, "namespace:get/list/watch core workload resources", "namespace:mutate workloads", "namespace:exec/logs/service-proxy", "deny:cluster-scope")
	case "custom":
		out = append(out, "custom:operator-provided-rbac", "deny:capability-inference")
	case "admin":
		out = append(out, "cluster-admin:*")
	default:
		out = append(out, "invalid")
	}
	if len(spec.NamespaceScope) > 0 {
		scopes := append([]string(nil), spec.NamespaceScope...)
		sort.Strings(scopes)
		out = append(out, "namespaces:"+strings.Join(scopes, ","))
	}
	if len(spec.Capabilities) > 0 {
		keys := make([]string, 0, len(spec.Capabilities))
		for key := range spec.Capabilities {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out, fmt.Sprintf("capability:%s=%t", key, spec.Capabilities[key]))
		}
	}
	if len(spec.AllowedRules) > 0 {
		out = append(out, fmt.Sprintf("custom-rules:%d", len(spec.AllowedRules)))
	}
	if hasAgentProfileHostAccess(spec.HostAccess) {
		out = append(out, "host-access:requested")
	}
	if mode := strings.TrimSpace(spec.NetworkEgress.Mode); mode != "" {
		out = append(out, "egress:"+mode)
	}
	return out
}

func buildGitOpsTargetStatus(obj GitOpsTarget) GitOpsTargetStatus {
	now := metav1.Time{Time: time.Now().UTC()}
	status := GitOpsTargetStatus{
		ObservedGeneration: obj.Generation,
		LastReconciled:     now,
		MatchedClusters:    append([]string(nil), obj.Spec.Selector.ClusterRefs...),
		ApplicationSetName: gitOpsTargetApplicationSetName(obj),
	}
	problems := validateGitOpsTargetSpec(obj.Spec)
	if obj.Spec.Suspended {
		status.Phase = "Suspended"
		status.Conditions = suspendedCRDConditions(obj.Generation, "GitOpsTarget reconciliation is suspended.")
		return status
	}
	if len(problems) > 0 {
		status.Phase = "Degraded"
		status.Conditions = standardCRDConditions(obj.Generation, "ValidationFailed", false, "ValidationFailed", strings.Join(problems, "; "))
		return status
	}
	status.Phase = "Ready"
	status.Conditions = standardCRDConditions(obj.Generation, "Accepted", true, "ValidationSucceeded", "GitOpsTarget spec is accepted; Argo ApplicationSet generation will run when a supported source, bundleRef, or templateRef is present.")
	return status
}

func shouldApplyGitOpsTarget(obj GitOpsTarget, status GitOpsTargetStatus) bool {
	return !obj.Spec.Suspended && status.Phase == "Ready"
}

func (r *GitOpsTargetReconciler) ensureGitOpsTargetApplicationSet(ctx context.Context, obj GitOpsTarget) (string, bool, error) {
	argoNamespace := argoNamespaceOrDefault(r.ArgoNamespace)
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	appSet, err := gitOpsTargetApplicationSetObject(ctx, reader, obj, argoNamespace)
	if err != nil {
		return gitOpsTargetApplicationSetName(obj), false, err
	}
	drifted, err := upsertApplicationSet(ctx, r.Client, reader, appSet)
	if err != nil {
		return appSet.GetName(), false, err
	}
	return appSet.GetName(), drifted, nil
}

func gitOpsTargetApplicationSetObject(ctx context.Context, reader client.Reader, obj GitOpsTarget, argoNamespace string) (*unstructured.Unstructured, error) {
	if err := enforceGitOpsTargetProjectSelector(ctx, reader, obj); err != nil {
		return nil, err
	}
	materialized, err := gitOpsTargetApplicationMaterialization(ctx, reader, obj)
	if err != nil {
		return nil, err
	}
	name := gitOpsTargetApplicationSetName(obj)
	sourceNameLabel := dnsLabel(obj.Name)
	sourceNamespaceLabel := dnsLabel(obj.Namespace)
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "astronomer",
		"astronomer.io/crd-kind":       "GitOpsTarget",
		"astronomer.io/crd-name":       sourceNameLabel,
		"astronomer.io/crd-namespace":  sourceNamespaceLabel,
	}
	selector := gitOpsTargetClusterSelector(obj.Spec.Selector)
	syncPolicy := gitOpsTargetApplicationSyncPolicy(obj.Spec.SyncPolicy, materialized.DestinationNamespace)
	templateSpec := map[string]any{
		"project": materialized.Project,
		"source":  materialized.Source,
		"destination": map[string]any{
			"server": "{{server}}",
		},
	}
	if strings.TrimSpace(materialized.DestinationNamespace) != "" {
		templateSpec["destination"].(map[string]any)["namespace"] = materialized.DestinationNamespace
	}
	if len(syncPolicy) > 0 {
		templateSpec["syncPolicy"] = syncPolicy
	}
	object := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": applicationSetGVK.GroupVersion().String(),
			"kind":       applicationSetGVK.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": argoNamespace,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "astronomer",
					"astronomer.io/crd-kind":       "GitOpsTarget",
					"astronomer.io/crd-name":       sourceNameLabel,
					"astronomer.io/crd-namespace":  sourceNamespaceLabel,
				},
				"annotations": map[string]any{
					"management.astronomer.io/source-api-version": GroupVersion.String(),
					"management.astronomer.io/source-kind":        "GitOpsTarget",
					"management.astronomer.io/source-namespace":   obj.Namespace,
					"management.astronomer.io/source-name":        obj.Name,
				},
			},
			"spec": map[string]any{
				"generators": []any{
					map[string]any{
						"clusters": map[string]any{
							"selector": selector,
						},
					},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"name": dnsLabel("astro", obj.Name) + "-{{nameNormalized}}",
						"labels": map[string]any{
							"app.kubernetes.io/managed-by": "astronomer",
							"astronomer.io/crd-kind":       "GitOpsTarget",
							"astronomer.io/crd-name":       sourceNameLabel,
							"astronomer.io/crd-namespace":  sourceNamespaceLabel,
						},
					},
					"spec": templateSpec,
				},
			},
		},
	}
	object.SetGroupVersionKind(applicationSetGVK)
	object.SetLabels(labels)
	object.SetAnnotations(mapStringAnyToString(crdSourceAnnotations("GitOpsTarget", obj.Namespace, obj.Name)))
	return object, nil
}

func enforceGitOpsTargetProjectSelector(ctx context.Context, reader client.Reader, obj GitOpsTarget) error {
	if len(obj.Spec.ProjectSelector.MatchLabels) == 0 && len(obj.Spec.ProjectSelector.ClusterRefs) == 0 {
		return nil
	}
	projects, err := matchingGitOpsTargetProjects(ctx, reader, obj.Namespace, obj.Spec.ProjectSelector)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return fmt.Errorf("projectSelector matched no Project resources in namespace %s", obj.Namespace)
	}
	if len(obj.Spec.Selector.ClusterRefs) > 0 {
		allowedClusters := map[string]struct{}{}
		for _, project := range projects {
			for _, cluster := range project.Spec.Clusters {
				allowedClusters[strings.TrimSpace(cluster)] = struct{}{}
			}
		}
		for _, cluster := range obj.Spec.Selector.ClusterRefs {
			cluster = strings.TrimSpace(cluster)
			if cluster == "" {
				continue
			}
			if _, ok := allowedClusters[cluster]; !ok {
				return fmt.Errorf("projectSelector does not allow target cluster %q", cluster)
			}
		}
		return nil
	}
	if projectName := strings.TrimSpace(obj.Spec.Selector.MatchLabels[argolabels.ProjectLabelKey]); projectName != "" {
		if gitOpsTargetProjectsIncludeName(projects, projectName) {
			return nil
		}
		return fmt.Errorf("selector.matchLabels[%s]=%q is not allowed by projectSelector", argolabels.ProjectLabelKey, projectName)
	}
	if projectID := strings.TrimSpace(obj.Spec.Selector.MatchLabels[argolabels.ProjectIDLabelKey]); projectID != "" {
		if gitOpsTargetProjectsIncludeID(projects, projectID) {
			return nil
		}
		return fmt.Errorf("selector.matchLabels[%s]=%q is not allowed by projectSelector", argolabels.ProjectIDLabelKey, projectID)
	}
	if label, ok := gitOpsTargetAllowedProjectMembershipLabel(projects, obj.Spec.Selector.MatchLabels); ok {
		if strings.TrimSpace(obj.Spec.Selector.MatchLabels[label]) == argolabels.ProjectMembershipLabelValue {
			return nil
		}
		return fmt.Errorf("selector.matchLabels[%s] must be %q for projectSelector", label, argolabels.ProjectMembershipLabelValue)
	}
	return fmt.Errorf("projectSelector requires selector.clusterRefs or a durable project label selector (%s, %s, %s*, or %s*)", argolabels.ProjectLabelKey, argolabels.ProjectIDLabelKey, argolabels.ProjectMembershipPrefix, argolabels.ProjectIDMembershipPrefix)
}

func matchingGitOpsTargetProjects(ctx context.Context, reader client.Reader, namespace string, selector LabelSelectorSpec) ([]Project, error) {
	list := &ProjectList{}
	if err := reader.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list Projects for projectSelector: %w", err)
	}
	out := make([]Project, 0, len(list.Items))
	for _, project := range list.Items {
		if !labelsMatch(project.GetLabels(), selector.MatchLabels) {
			continue
		}
		if len(selector.ClusterRefs) > 0 && !projectContainsAllClusters(project, selector.ClusterRefs) {
			continue
		}
		out = append(out, project)
	}
	return out, nil
}

func labelsMatch(labels map[string]string, selector map[string]string) bool {
	for key, want := range selector {
		if labels[key] != want {
			return false
		}
	}
	return true
}

func projectContainsAllClusters(project Project, clusters []string) bool {
	available := map[string]struct{}{}
	for _, cluster := range project.Spec.Clusters {
		available[strings.TrimSpace(cluster)] = struct{}{}
	}
	for _, cluster := range clusters {
		cluster = strings.TrimSpace(cluster)
		if cluster == "" {
			continue
		}
		if _, ok := available[cluster]; !ok {
			return false
		}
	}
	return true
}

func gitOpsTargetProjectsIncludeName(projects []Project, name string) bool {
	for _, project := range projects {
		if project.Name == name || strings.TrimSpace(project.Spec.Name) == name {
			return true
		}
	}
	return false
}

func gitOpsTargetProjectsIncludeID(projects []Project, id string) bool {
	for _, project := range projects {
		if strings.TrimSpace(project.Status.ProjectID) == id {
			return true
		}
	}
	return false
}

func gitOpsTargetAllowedProjectMembershipLabel(projects []Project, matchLabels map[string]string) (string, bool) {
	for _, project := range projects {
		if label := argolabels.ProjectNameMembershipLabel(project.Name); label != "" {
			if _, ok := matchLabels[label]; ok {
				return label, true
			}
		}
		if specName := strings.TrimSpace(project.Spec.Name); specName != "" {
			if label := argolabels.ProjectNameMembershipLabel(specName); label != "" {
				if _, ok := matchLabels[label]; ok {
					return label, true
				}
			}
		}
		if projectID := strings.TrimSpace(project.Status.ProjectID); projectID != "" {
			label := argolabels.ProjectIDMembershipPrefix + projectID
			if _, ok := matchLabels[label]; ok {
				return label, true
			}
		}
	}
	return "", false
}

type gitOpsTargetMaterialization struct {
	Source               map[string]any
	DestinationNamespace string
	Project              string
}

func gitOpsTargetApplicationMaterialization(ctx context.Context, reader client.Reader, obj GitOpsTarget) (gitOpsTargetMaterialization, error) {
	params := obj.Spec.ApplicationSet.Parameters
	destinationNamespace := strings.TrimSpace(params["namespace"])
	if strings.TrimSpace(obj.Spec.ApplicationSet.SourceRepo) != "" || strings.TrimSpace(obj.Spec.ApplicationSet.Path) != "" {
		sourceRepo := strings.TrimSpace(obj.Spec.ApplicationSet.SourceRepo)
		path := strings.TrimSpace(obj.Spec.ApplicationSet.Path)
		if sourceRepo == "" || path == "" {
			return gitOpsTargetMaterialization{}, errors.New("applicationSet.sourceRepo and applicationSet.path must be set together")
		}
		revision := strutil.FirstNonBlankTrimmed(obj.Spec.ApplicationSet.Revision, "HEAD")
		return gitOpsTargetMaterialization{
			Source: map[string]any{
				"repoURL":        sourceRepo,
				"path":           path,
				"targetRevision": revision,
			},
			DestinationNamespace: strutil.FirstNonBlankTrimmed(destinationNamespace, "default"),
			Project:              "default",
		}, nil
	}
	if strings.TrimSpace(obj.Spec.BundleRef.Name) != "" {
		if reader == nil {
			return gitOpsTargetMaterialization{}, errors.New("component bundle reader is not configured")
		}
		var bundle ComponentBundle
		if err := reader.Get(ctx, kubeutil.NamespacedName(obj.Namespace, obj.Spec.BundleRef.Name), &bundle); err != nil {
			return gitOpsTargetMaterialization{}, fmt.Errorf("get ComponentBundle %s/%s: %w", obj.Namespace, obj.Spec.BundleRef.Name, err)
		}
		if problems := validateComponentBundleSpec(bundle.Spec); len(problems) > 0 {
			return gitOpsTargetMaterialization{}, fmt.Errorf("ComponentBundle %s/%s is invalid: %s", obj.Namespace, obj.Spec.BundleRef.Name, strings.Join(problems, "; "))
		}
		resolvedSpec, err := resolveComponentBundleVersion(bundle, obj.Spec.BundleRef.Version)
		if err != nil {
			return gitOpsTargetMaterialization{}, err
		}
		source, err := componentBundleApplicationSource(resolvedSpec)
		if err != nil {
			return gitOpsTargetMaterialization{}, err
		}
		return gitOpsTargetMaterialization{
			Source:               source,
			DestinationNamespace: strutil.FirstNonBlankTrimmed(destinationNamespace, resolvedSpec.DefaultNamespace, "default"),
			Project:              "default",
		}, nil
	}
	if strings.TrimSpace(obj.Spec.ApplicationSet.TemplateRef) != "" {
		return resolveGitOpsTemplateRef(ctx, reader, obj)
	}
	return gitOpsTargetMaterialization{}, errors.New("applicationSet source, bundleRef, or templateRef is required")
}

type gitOpsTemplateSpec struct {
	Project              string                    `json:"project,omitempty"`
	DestinationNamespace string                    `json:"destinationNamespace,omitempty"`
	Source               ComponentBundleSourceSpec `json:"source"`
}

func resolveGitOpsTemplateRef(ctx context.Context, reader client.Reader, obj GitOpsTarget) (gitOpsTargetMaterialization, error) {
	if reader == nil {
		return gitOpsTargetMaterialization{}, errors.New("gitops template reader is not configured")
	}
	name := strings.TrimSpace(obj.Spec.ApplicationSet.TemplateRef)
	if strings.Contains(name, "/") {
		return gitOpsTargetMaterialization{}, errors.New("applicationSet.templateRef must reference a same-namespace template name")
	}
	cm := kubeutil.NewUnstructured(configMapGVK, obj.Namespace, name)
	if err := reader.Get(ctx, kubeutil.NamespacedName(obj.Namespace, name), cm); err != nil {
		return gitOpsTargetMaterialization{}, fmt.Errorf("get GitOps template ConfigMap %s/%s: %w", obj.Namespace, name, err)
	}
	if cm.GetLabels()[gitOpsTemplateLabelKey] != gitOpsTemplateLabelValue {
		return gitOpsTargetMaterialization{}, fmt.Errorf("GitOps template ConfigMap %s/%s is missing %s=%s", obj.Namespace, name, gitOpsTemplateLabelKey, gitOpsTemplateLabelValue)
	}
	data, found, err := unstructured.NestedStringMap(cm.Object, "data")
	if err != nil {
		return gitOpsTargetMaterialization{}, fmt.Errorf("read GitOps template ConfigMap %s/%s data: %w", obj.Namespace, name, err)
	}
	if !found || strings.TrimSpace(data[gitOpsTemplateDataKey]) == "" {
		return gitOpsTargetMaterialization{}, fmt.Errorf("GitOps template ConfigMap %s/%s must include data.%s", obj.Namespace, name, gitOpsTemplateDataKey)
	}
	var spec gitOpsTemplateSpec
	if err := json.Unmarshal([]byte(data[gitOpsTemplateDataKey]), &spec); err != nil {
		return gitOpsTargetMaterialization{}, fmt.Errorf("parse GitOps template ConfigMap %s/%s data.%s: %w", obj.Namespace, name, gitOpsTemplateDataKey, err)
	}
	if problems := validateGitOpsTemplateSpec(spec); len(problems) > 0 {
		return gitOpsTargetMaterialization{}, fmt.Errorf("GitOps template ConfigMap %s/%s is invalid: %s", obj.Namespace, name, strings.Join(problems, "; "))
	}
	source, err := applicationSourceFromComponentBundleSource(spec.Source)
	if err != nil {
		return gitOpsTargetMaterialization{}, err
	}
	destinationNamespace := strutil.FirstNonBlankTrimmed(obj.Spec.ApplicationSet.Parameters["namespace"], spec.DestinationNamespace, "default")
	return gitOpsTargetMaterialization{
		Source:               source,
		DestinationNamespace: destinationNamespace,
		Project:              strutil.FirstNonBlankTrimmed(spec.Project, "default"),
	}, nil
}

func validateGitOpsTemplateSpec(spec gitOpsTemplateSpec) []string {
	var problems []string
	if len(spec.Source.SecretRefs) > 0 {
		problems = append(problems, "source.secretRefs are not allowed in GitOps template ConfigMaps")
	}
	sourceType := strings.TrimSpace(spec.Source.Type)
	switch sourceType {
	case "helm":
		if strings.TrimSpace(spec.Source.RepoURL) == "" {
			problems = append(problems, "source.repoURL is required for helm templates")
		}
		if strings.TrimSpace(spec.Source.Chart) == "" {
			problems = append(problems, "source.chart is required for helm templates")
		}
	case "kustomize", "git-path", "raw":
		if strings.TrimSpace(spec.Source.RepoURL) == "" {
			problems = append(problems, "source.repoURL is required for git-backed templates")
		}
		if strings.TrimSpace(spec.Source.Path) == "" {
			problems = append(problems, "source.path is required for git-backed templates")
		}
	default:
		problems = append(problems, "source.type must be one of helm, kustomize, git-path, raw")
	}
	return problems
}

func componentBundleApplicationSource(spec ComponentBundleSpec) (map[string]any, error) {
	return applicationSourceFromComponentBundleSource(spec.Source)
}

func applicationSourceFromComponentBundleSource(source ComponentBundleSourceSpec) (map[string]any, error) {
	revision := strings.TrimSpace(source.TargetRevision)
	switch strings.TrimSpace(source.Type) {
	case "helm":
		return map[string]any{
			"repoURL":        strings.TrimSpace(source.RepoURL),
			"chart":          strings.TrimSpace(source.Chart),
			"targetRevision": strutil.FirstNonBlankTrimmed(revision, "*"),
		}, nil
	case "kustomize", "git-path", "raw":
		return map[string]any{
			"repoURL":        strings.TrimSpace(source.RepoURL),
			"path":           strings.TrimSpace(source.Path),
			"targetRevision": strutil.FirstNonBlankTrimmed(revision, "HEAD"),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported source type %q", source.Type)
	}
}

func resolveComponentBundleVersion(bundle ComponentBundle, requestedVersion string) (ComponentBundleSpec, error) {
	requestedVersion = strings.TrimSpace(requestedVersion)
	if requestedVersion == "" || requestedVersion == strings.TrimSpace(bundle.Spec.Version) {
		return componentBundleTopLevelVersionSpec(bundle.Spec), nil
	}
	for _, version := range bundle.Spec.Versions {
		if requestedVersion == strings.TrimSpace(version.Version) {
			return componentBundleSpecFromVersion(bundle.Spec, version), nil
		}
	}
	availableVersions := componentBundleAvailableVersions(bundle.Spec)
	if len(availableVersions) == 0 {
		availableVersions = []string{"<none>"}
	}
	return ComponentBundleSpec{}, fmt.Errorf("ComponentBundle %s/%s version mismatch: requested %s, available %s", bundle.Namespace, bundle.Name, requestedVersion, strings.Join(availableVersions, ", "))
}

func componentBundleTopLevelVersionSpec(spec ComponentBundleSpec) ComponentBundleSpec {
	spec.Versions = nil
	return spec
}

func componentBundleSpecFromVersion(base ComponentBundleSpec, version ComponentBundleVersionSpec) ComponentBundleSpec {
	return ComponentBundleSpec{
		Version:                strings.TrimSpace(version.Version),
		Description:            strutil.FirstNonBlankTrimmed(version.Description, base.Description),
		DefaultNamespace:       strutil.FirstNonBlankTrimmed(version.DefaultNamespace, base.DefaultNamespace),
		Source:                 version.Source,
		CapabilityRequirements: append([]ComponentBundleRequirement(nil), version.CapabilityRequirements...),
		HealthChecks:           append([]ComponentBundleHealthCheck(nil), version.HealthChecks...),
		UpgradePolicy:          version.UpgradePolicy,
	}
}

func componentBundleAvailableVersions(spec ComponentBundleSpec) []string {
	versions := make([]string, 0, 1+len(spec.Versions))
	seen := map[string]struct{}{}
	for _, version := range append([]string{spec.Version}, componentBundleNestedVersionNames(spec.Versions)...) {
		version = strings.TrimSpace(version)
		if version == "" {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	return versions
}

func componentBundleNestedVersionNames(versions []ComponentBundleVersionSpec) []string {
	names := make([]string, 0, len(versions))
	for _, version := range versions {
		names = append(names, version.Version)
	}
	return names
}

func gitOpsTargetApplicationSyncPolicy(policy GitOpsTargetSyncPolicy, destinationNamespace string) map[string]any {
	out := map[string]any{}
	if policy.Automated || policy.Prune || policy.SelfHeal {
		out["automated"] = map[string]any{
			"prune":    policy.Prune,
			"selfHeal": policy.SelfHeal,
		}
	}
	if strings.TrimSpace(destinationNamespace) != "" {
		out["syncOptions"] = []any{"CreateNamespace=true"}
	}
	return out
}

func gitOpsTargetClusterSelector(selector GitOpsTargetSelectorSpec) map[string]any {
	matchLabels := map[string]any{
		"astronomer.io/managed-by": "astronomer",
	}
	for key, value := range selector.MatchLabels {
		matchLabels[key] = value
	}
	out := map[string]any{"matchLabels": matchLabels}
	if len(selector.ClusterRefs) > 0 {
		out["matchExpressions"] = []any{
			map[string]any{
				"key":      "astronomer.io/cluster-name",
				"operator": "In",
				"values":   stringSliceToAny(selector.ClusterRefs),
			},
		}
	}
	return out
}

func gitOpsTargetApplicationSetName(obj GitOpsTarget) string {
	return dnsLabel("astronomer-gitops", obj.Namespace, obj.Name)
}

func validateGitOpsTargetSpec(spec GitOpsTargetSpec) []string {
	var problems []string
	if len(spec.Selector.MatchLabels) == 0 && len(spec.Selector.ClusterRefs) == 0 {
		problems = append(problems, "selector.matchLabels or selector.clusterRefs is required")
	}
	if len(spec.Selector.MatchLabels) > 0 && spec.Selector.MatchLabels["astronomer.io/managed-by"] != "astronomer" {
		problems = append(problems, "selector.matchLabels must include astronomer.io/managed-by=astronomer")
	}
	if strings.TrimSpace(spec.BundleRef.Version) != "" && strings.TrimSpace(spec.BundleRef.Name) == "" {
		problems = append(problems, "bundleRef.name is required when bundleRef.version is set")
	}
	if strings.TrimSpace(spec.ApplicationSet.TemplateRef) == "" && strings.TrimSpace(spec.BundleRef.Name) == "" {
		if strings.TrimSpace(spec.ApplicationSet.SourceRepo) == "" {
			problems = append(problems, "applicationSet.templateRef, applicationSet.sourceRepo, or bundleRef.name is required")
		}
		if strings.TrimSpace(spec.ApplicationSet.Path) == "" {
			problems = append(problems, "applicationSet.templateRef, applicationSet.path, or bundleRef.name is required")
		}
	}
	for i, window := range spec.SyncWindows {
		if window.Kind != "allow" && window.Kind != "deny" {
			problems = append(problems, fmt.Sprintf("syncWindows[%d].kind must be allow or deny", i))
		}
	}
	return problems
}

func finalizerTimeoutExceeded(obj client.Object) bool {
	ts := obj.GetDeletionTimestamp()
	if ts == nil || ts.IsZero() {
		return false
	}
	return time.Since(ts.Time) >= finalizerTimeout
}

func finalizerTimeoutConditions(generation int64, message string) []metav1.Condition {
	now := metav1.Time{Time: time.Now().UTC()}
	return []metav1.Condition{
		{
			Type:               "Accepted",
			Status:             metav1.ConditionTrue,
			Reason:             "Accepted",
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
		{
			Type:               "Reconciled",
			Status:             metav1.ConditionFalse,
			Reason:             "FinalizerTimeout",
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
		{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "FinalizerTimeout",
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
	}
}

func standardCRDConditions(generation int64, reason string, accepted bool, readyReason, message string) []metav1.Condition {
	now := metav1.Time{Time: time.Now().UTC()}
	acceptedStatus := metav1.ConditionTrue
	readyStatus := metav1.ConditionTrue
	reconciledStatus := metav1.ConditionTrue
	if !accepted {
		acceptedStatus = metav1.ConditionFalse
		readyStatus = metav1.ConditionFalse
		reconciledStatus = metav1.ConditionFalse
	}
	return []metav1.Condition{
		{
			Type:               "Accepted",
			Status:             acceptedStatus,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
		{
			Type:               "Reconciled",
			Status:             reconciledStatus,
			Reason:             readyReason,
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
		{
			Type:               "Ready",
			Status:             readyStatus,
			Reason:             readyReason,
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
	}
}

func suspendedCRDConditions(generation int64, message string) []metav1.Condition {
	now := metav1.Time{Time: time.Now().UTC()}
	return []metav1.Condition{
		{
			Type:               "Accepted",
			Status:             metav1.ConditionTrue,
			Reason:             "Suspended",
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
		{
			Type:               "Reconciled",
			Status:             metav1.ConditionFalse,
			Reason:             "Suspended",
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
		{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "Suspended",
			Message:            message,
			ObservedGeneration: generation,
			LastTransitionTime: now,
		},
	}
}

func dnsLabel(parts ...string) string {
	raw := strings.ToLower(strings.Join(parts, "-"))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "astronomer"
	}
	if len(out) <= 63 {
		return out
	}
	sum := sha1.Sum([]byte(out))
	suffix := hex.EncodeToString(sum[:])[:10]
	prefix := strings.Trim(out[:52], "-")
	if prefix == "" {
		prefix = "astronomer"
	}
	return prefix + "-" + suffix
}
