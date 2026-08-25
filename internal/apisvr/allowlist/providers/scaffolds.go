package providers

import (
	"context"
)

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

func (p *SelfManagedProvider) Capability() Capability {
	return Capability{Provider: ProviderSelfManaged, CanMonitor: true, Reason: "self-managed API-server firewalls are operator-owned"}
}

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
	return &UnsupportedEnforcementError{Provider: ProviderSelfManaged, Reason: p.Capability().Reason}
}
