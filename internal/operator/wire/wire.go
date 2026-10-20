// Package wire composes the management operator: the controller-runtime
// manager, its scheme and informer cache, and the registration of the
// lifecycle reconcilers selected by configuration. The reconcile logic lives
// in internal/operator/lifecycle.
package wire

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/alphabravocompany/astronomer-go/internal/operator/lifecycle"
)

// Controller names accepted in Config.Controllers.
const (
	ControllerCluster      = "cluster"
	ControllerProject      = "project"
	ControllerAgentProfile = "agentprofile"
)

// AllControllers is the registration order used when Config.Controllers is
// empty.
var AllControllers = []string{ControllerCluster, ControllerProject, ControllerAgentProfile}

// DefaultMaxConcurrentReconciles bounds parallel reconciles per controller.
// Reconciles of one object are serialized by the lifecycle lock regardless.
const DefaultMaxConcurrentReconciles = 4

// LeaderElectionID is the Lease the HA server replicas contend for.
const LeaderElectionID = "astronomer-management-crd-controller"

// Config selects and parameterizes the operator.
type Config struct {
	K8sConfig *rest.Config
	// WatchNamespace restricts the informer cache; empty watches all.
	WatchNamespace string
	// Controllers lists the controllers to register (see the Controller*
	// constants); empty registers AllControllers.
	Controllers []string
	// MaxConcurrentReconciles per controller; <=0 uses the default.
	MaxConcurrentReconciles int
	// ClusterSync is required when the cluster controller is registered.
	ClusterSync lifecycle.ClusterSync
	// ProjectSync is required when the project controller is registered.
	ProjectSync             lifecycle.ProjectSync
	Log                     *slog.Logger
	PollPeriod              time.Duration
	Observer                lifecycle.Observer
	LeaderElection          bool
	LeaderElectionNamespace string
}

// Operator is the composed manager plus the reconcilers registered on it;
// a reconciler that was not selected is nil.
type Operator struct {
	Manager      manager.Manager
	Cluster      *lifecycle.ClusterReconciler
	Project      *lifecycle.ProjectReconciler
	AgentProfile *lifecycle.AgentProfileReconciler
}

// NewScheme returns the scheme the operator's client and cache use: the
// client-go core kinds plus the management.astronomer.io kinds.
func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register core kinds: %w", err)
	}
	if err := lifecycle.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register management kinds: %w", err)
	}
	return scheme, nil
}

// New builds the manager and registers the selected controllers. Call
// Operator.Manager.Start to run it.
func New(cfg Config) (*Operator, error) {
	if cfg.K8sConfig == nil {
		return nil, errors.New("operator: Kubernetes config is required")
	}
	selected, err := selectControllers(cfg.Controllers)
	if err != nil {
		return nil, err
	}
	if slices.Contains(selected, ControllerCluster) && cfg.ClusterSync == nil {
		return nil, errors.New("operator: cluster controller needs a ClusterSync")
	}
	if slices.Contains(selected, ControllerProject) && cfg.ProjectSync == nil {
		return nil, errors.New("operator: project controller needs a ProjectSync")
	}
	mgr, err := newManager(cfg)
	if err != nil {
		return nil, err
	}
	op := &Operator{Manager: mgr}
	opts := lifecycle.Options{Log: cfg.Log, Poll: cfg.PollPeriod, Observer: cfg.Observer}
	maxConcurrent := cfg.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrentReconciles
	}
	for _, name := range selected {
		object, reconciler, err := op.build(name, mgr.GetClient(), cfg, opts)
		if err != nil {
			return nil, err
		}
		if err := ctrl.NewControllerManagedBy(mgr).
			Named(name).
			For(object).
			WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
			Complete(reconciler); err != nil {
			return nil, fmt.Errorf("operator: register %s controller: %w", name, err)
		}
	}
	return op, nil
}

func (op *Operator) build(name string, c client.Client, cfg Config, opts lifecycle.Options) (client.Object, reconcile.Reconciler, error) {
	switch name {
	case ControllerCluster:
		r, err := lifecycle.NewClusterReconciler(c, cfg.ClusterSync, opts)
		op.Cluster = r
		return &lifecycle.Cluster{}, r, err
	case ControllerProject:
		r, err := lifecycle.NewProjectReconciler(c, cfg.ProjectSync, opts)
		op.Project = r
		return &lifecycle.Project{}, r, err
	case ControllerAgentProfile:
		r, err := lifecycle.NewAgentProfileReconciler(c, opts)
		op.AgentProfile = r
		return &lifecycle.AgentProfile{}, r, err
	default:
		return nil, nil, fmt.Errorf("operator: unknown controller %q", name)
	}
}

func selectControllers(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return slices.Clone(AllControllers), nil
	}
	out := make([]string, 0, len(requested))
	for _, name := range requested {
		if !slices.Contains(AllControllers, name) {
			return nil, fmt.Errorf("operator: unknown controller %q (known: %v)", name, AllControllers)
		}
		if slices.Contains(out, name) {
			return nil, fmt.Errorf("operator: controller %q selected twice", name)
		}
		out = append(out, name)
	}
	return out, nil
}

func newManager(cfg Config) (manager.Manager, error) {
	scheme, err := NewScheme()
	if err != nil {
		return nil, err
	}
	opts := manager.Options{
		Scheme:                     scheme,
		Metrics:                    metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:     "0",
		LeaderElection:             cfg.LeaderElection,
		LeaderElectionNamespace:    cfg.LeaderElectionNamespace,
		LeaderElectionID:           LeaderElectionID,
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
		return nil, fmt.Errorf("operator: build manager: %w", err)
	}
	return mgr, nil
}
