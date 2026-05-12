package providers

import (
	"context"
)

// SCAFFOLDS — v1 placeholders.
//
// These three drivers detect their respective providers and return the
// not-implemented sentinel from Apply / GetEffective. The reconciler
// treats the sentinel as "log a warning + keep the row in monitor" so
// operators on AKS / DOKS / self-managed clusters still get drift
// detection (well, "drift relative to empty effective set") and
// audit-log entries; they just don't get auto-patching until the
// corresponding driver lands.
//
// To finish a driver:
//   1. Replace the GetEffective / Apply bodies with the real cloud SDK
//      calls (Azure: aks.ManagedClusters.Get / CreateOrUpdate; DO:
//      godo's Kubernetes.UpdateCluster; SelfManaged: SSA patch on a
//      NetworkPolicy + nginx-front-proxy whitelist-source-range annot).
//   2. Add a *_test.go alongside that drives the new logic.
//   3. Drop the not-implemented sentinel and the "scaffolded" comment.

// AKSProvider — Azure Kubernetes Service. TODO(sprint 071): wire the
// az-sdk-for-go ManagedClusters client and write
// .Properties.ApiServerAccessProfile.AuthorizedIpRanges.
type AKSProvider struct {
	Materializer CloudCredentialMaterializer
}

func NewAKSProvider(m CloudCredentialMaterializer) *AKSProvider {
	return &AKSProvider{Materializer: m}
}

func (p *AKSProvider) ID() ProviderID { return ProviderAKS }

func (p *AKSProvider) Detect(ctx context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderAKS) {
		return ProviderAKS
	}
	return ""
}

func (p *AKSProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	// TODO(sprint 071): call ManagedClustersClient.Get and read
	// .Properties.ApiServerAccessProfile.AuthorizedIpRanges. Until then
	// return empty + the not-implemented sentinel so the reconciler can
	// skip the patch path and just emit a warning.
	return nil, errProviderNotImplemented
}

func (p *AKSProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	// TODO(sprint 071): call ManagedClustersClient.BeginCreateOrUpdate
	// with .Properties.ApiServerAccessProfile.AuthorizedIpRanges = cidrs.
	return errProviderNotImplemented
}

// DOKSProvider — DigitalOcean Kubernetes. TODO(sprint 071): wire the
// godo Kubernetes service and write the cluster's authorized-cidrs
// property (technically a separate firewall resource on DO).
type DOKSProvider struct {
	Materializer CloudCredentialMaterializer
}

func NewDOKSProvider(m CloudCredentialMaterializer) *DOKSProvider {
	return &DOKSProvider{Materializer: m}
}

func (p *DOKSProvider) ID() ProviderID { return ProviderDOKS }

func (p *DOKSProvider) Detect(ctx context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderDOKS) {
		return ProviderDOKS
	}
	return ""
}

func (p *DOKSProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	// TODO(sprint 071): call Kubernetes.Get and read whatever DO
	// surfaces as the access-CIDR list (in practice a Firewall row
	// they associate with the LB).
	return nil, errProviderNotImplemented
}

func (p *DOKSProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	return errProviderNotImplemented
}

// SelfManagedProvider — kubeadm / k3s / RKE clusters. Detection picks
// up "self_managed" annotation OR an empty Provider field on the
// cluster row. v1 ships as a TODO: when an operator's mode=enforce we
// log a warning and refuse the patch (see the reconciler — the
// constraint document says "v1 only auto-enforces on cloud-managed").
type SelfManagedProvider struct{}

func NewSelfManagedProvider() *SelfManagedProvider {
	return &SelfManagedProvider{}
}

func (p *SelfManagedProvider) ID() ProviderID { return ProviderSelfManaged }

func (p *SelfManagedProvider) Detect(ctx context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderSelfManaged) {
		return ProviderSelfManaged
	}
	// Fall-back: clusters with no provider stamp default to self-managed.
	if cluster.Provider == "" {
		return ProviderSelfManaged
	}
	return ""
}

func (p *SelfManagedProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	// TODO(sprint 071): proxy through the tunnel to GET the kube-system
	// NetworkPolicy operators told us to patch + parse the egress rules.
	// Until then return empty so the reconciler's monitor-mode path
	// still snapshots + records audit; enforce mode short-circuits at
	// the reconciler level.
	return []string{}, nil
}

func (p *SelfManagedProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	// Self-managed enforcement is operator-driven (they tell us which
	// k8s object to patch). v1 refuses the apply with the sentinel.
	return errProviderNotImplemented
}
