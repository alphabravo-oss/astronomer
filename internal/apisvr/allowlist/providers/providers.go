// Package providers is the cloud-LB / firewall driver registry for the
// apiserver allow-list feature (migration 070).
//
// Each driver implements the Provider interface:
//
//   - Detect(cluster) → returns the provider id ("eks","gke","aks","doks",
//     "self_managed") when this driver applies, else "". Detection is by
//     cluster annotations / labels stamped at register-time; the cluster
//     row carries `provider` already but we re-detect here because the
//     row's provider value isn't always populated for legacy rows.
//
//   - GetEffective(cluster) → returns the current authorized-IP-ranges as
//     the cloud LB / firewall sees them. Used by the reconciler to compute
//     drift.
//
//   - Apply(cluster, cidrs) → sets the authorized-IP-ranges. Idempotent —
//     calling Apply with the same set twice is a no-op (the cloud SDK
//     itself dedupes; we double-check by comparing GetEffective to the
//     desired set in the reconciler before the apply path runs).
//
// v1 cloud-LB writers:
//   - EKS  : aws eks update-cluster-config --resources-vpc-config
//     endpointPublicAccess=true,publicAccessCidrs=...
//   - GKE  : gcloud container clusters update --master-authorized-networks
//   - AKS  : az aks update --api-server-authorized-ip-ranges ...
//     (scaffolded; v1 ships as a TODO — see SCAFFOLDS below)
//   - DOKS : doctl k8s cluster update via firewall API
//     (scaffolded; v1 ships as a TODO — see SCAFFOLDS below)
//   - SelfManaged : SSA patch on a NetworkPolicy as fallback
//     (scaffolded; v1 ships as a TODO — see SCAFFOLDS below)
//
// Each provider uses the existing cloud-credentials materialization
// (sprint 053) to get its API client. NO new cloud-credential storage —
// re-use what's there. NewEKSProvider takes a CloudCredentialMaterializer
// and resolves to AWS creds the same way the existing cluster-create
// path does.
package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Cluster is the narrow shape the providers see — keep this disjoint
// from sqlc.Cluster so the cloud-LB write path doesn't pin a full DB
// row in memory and tests can stand up the driver with a tiny struct.
type Cluster struct {
	// ID is the cluster_id used to look up cloud credentials / agent state.
	ID uuid.UUID
	// Provider is the operator-declared provider string ("eks", "gke",
	// "aks", "doks", "self_managed", or ""). Detect() may override.
	Provider string
	// Name is the cluster name as known to the cloud provider (EKS:
	// the cluster name; GKE: the cluster name; AKS: the resource name).
	Name string
	// Region is the cluster's cloud region — required by every API
	// path. Empty for self-managed clusters.
	Region string
	// ResourceGroup is the AKS-specific Azure resource group; empty
	// for non-AKS clusters.
	ResourceGroup string
	// ProjectID is the GKE-specific GCP project; empty for non-GKE.
	ProjectID string
	// CredentialID is the cloud_credentials row used to materialize
	// the AWS / GCP / Azure client. Zero UUID when the cluster has no
	// linked credential (self-managed clusters).
	CredentialID uuid.UUID
	// Annotations carries the cluster's stamped annotations (used by
	// Detect when the Provider field is empty).
	Annotations map[string]string
}

// Provider is the cloud-LB / firewall driver surface.
type Provider interface {
	// Detect returns the provider id ("eks", "gke", "aks", "doks",
	// "self_managed") if this driver applies to the cluster, else "".
	Detect(ctx context.Context, cluster Cluster) string

	// GetEffective returns the current authorized-IP-ranges as the cloud
	// LB / firewall sees them. Empty slice means "no restriction" (every
	// cloud provider treats an empty list as "all CIDRs allowed"; the
	// reconciler treats that as drift relative to a non-empty desired
	// set).
	GetEffective(ctx context.Context, cluster Cluster) ([]string, error)

	// Apply sets the authorized-IP-ranges. Idempotent. May return an
	// error wrapping the cloud SDK's failure shape; callers surface the
	// error text into apiserver_allowlists.last_error.
	Apply(ctx context.Context, cluster Cluster, cidrs []string) error
}

// ProviderID is one of the string constants below.
type ProviderID = string

const (
	ProviderEKS         ProviderID = "eks"
	ProviderGKE         ProviderID = "gke"
	ProviderAKS         ProviderID = "aks"
	ProviderDOKS        ProviderID = "doks"
	ProviderSelfManaged ProviderID = "self_managed"
	ProviderUnknown     ProviderID = "unknown"
)

// CloudCredentialMaterializer is the slice of the existing cloud-creds
// surface (sprint 053) the providers use to resolve cluster_id →
// decrypted credential blob. Defined as an interface so the providers
// don't pin a concrete cloudcreds.Resolver and tests can stand up a
// fake with a single closure.
type CloudCredentialMaterializer interface {
	// ResolveForCluster returns the {key: value} map of decrypted
	// credential fields associated with the cluster's credential_id, or
	// an error if the credential isn't materialised.
	ResolveForCluster(ctx context.Context, clusterID uuid.UUID) (map[string]string, error)
}

// Registry is an ordered list of providers. The reconciler iterates
// Detect() on each and stops at the first non-empty match. SelfManaged
// is intentionally LAST so the cloud-specific detectors get first
// crack at the cluster.
type Registry struct {
	providers []Provider
	mu        sync.RWMutex
}

// NewRegistry returns an empty registry. Wire one with Register; the
// reconciler is constructed against a fully-populated registry at
// server startup.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register appends a provider to the registry. Order matters — SelfManaged
// MUST be registered last so the cloud detectors see clusters first.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// Detect walks the registry and returns the (provider_id, provider)
// pair for the first driver whose Detect returns a non-empty id. Returns
// ("unknown", nil) when no driver claims the cluster.
func (r *Registry) Detect(ctx context.Context, cluster Cluster) (ProviderID, Provider) {
	r.mu.RLock()
	providers := append([]Provider(nil), r.providers...)
	r.mu.RUnlock()
	for _, p := range providers {
		if id := p.Detect(ctx, cluster); id != "" {
			return id, p
		}
	}
	return ProviderUnknown, nil
}

// Lookup returns the registered provider whose Detect returns the given
// id for a synthetic Cluster (used by the on-demand /reconcile/ endpoint
// to avoid re-detecting). Returns nil when no provider matches.
func (r *Registry) Lookup(id ProviderID) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		// Detect with an empty cluster but synthetic provider id - check
		// via reflection-free Provider.IsID() approximation by matching
		// type assertion. We keep this simple: store a small wrapper that
		// remembers the id.
		if pr, ok := p.(interface{ ID() ProviderID }); ok && pr.ID() == id {
			return p
		}
	}
	return nil
}

// matchAnnotationOrProvider is the shared "did the cluster stamp itself
// as us?" check every cloud driver runs first. Operators set the cluster's
// Provider field at create time; we also accept an `astronomer.io/provider`
// annotation for legacy rows / GitOps imports.
func matchAnnotationOrProvider(cluster Cluster, want ProviderID) bool {
	if strings.EqualFold(cluster.Provider, want) {
		return true
	}
	if a := cluster.Annotations["astronomer.io/provider"]; strings.EqualFold(a, want) {
		return true
	}
	return false
}

// errProviderNotImplemented is the sentinel scaffolded providers return
// from Apply when they're called in v1. The reconciler treats this as
// "log a warning, keep the row in 'monitor' even if mode='enforce'".
var errProviderNotImplemented = fmt.Errorf("provider not implemented in v1")

// ErrProviderNotImplemented reports whether the given error is the
// not-implemented sentinel. Used by the reconciler to special-case the
// scaffolded providers.
func ErrProviderNotImplemented(err error) bool {
	return err != nil && strings.Contains(err.Error(), "provider not implemented")
}
