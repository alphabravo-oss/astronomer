package charlie

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"k8s.io/client-go/discovery"
)

type sessionContextQueries interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
}

// ProductSessionContextProvider emits only allowlisted installation metadata
// and explicit resource references. Evidence, logs, metrics samples, audit
// bodies, configuration values, and downstream Kubernetes state are requested
// later through the live-authorized MCP catalog.
type ProductSessionContextProvider struct {
	queries   sessionContextQueries
	namespace string
	release   string
	chart     string
	// discovery is optional. When set, management-plane ServerVersion is pinned
	// into every session so the model can answer "what k8s version" without a
	// tool call. Downstream cluster kubectl is never used here.
	discovery discovery.DiscoveryInterface
}

func NewProductSessionContextProvider(queries sessionContextQueries, namespace, release, chart string, discovery discovery.DiscoveryInterface) (*ProductSessionContextProvider, error) {
	if queries == nil || len(namespace) > 63 || len(release) > 128 || len(chart) > 64 {
		return nil, fmt.Errorf("Charlie product context configuration is invalid")
	}
	return &ProductSessionContextProvider{
		queries: queries, namespace: strings.TrimSpace(namespace), release: strings.TrimSpace(release),
		chart: strings.TrimSpace(chart), discovery: discovery,
	}, nil
}

func (p *ProductSessionContextProvider) Context(ctx context.Context, resources []SessionResource, trigger, currentUI string) (SREContext, error) {
	connection, err := p.queries.GetActiveCharlieConnection(ctx)
	if err != nil || connection.InstallationID == uuid.Nil {
		return SREContext{}, fmt.Errorf("Charlie installation context is unavailable")
	}
	version, distribution := "", ""
	if p.discovery != nil {
		if info, versionErr := p.discovery.ServerVersion(); versionErr == nil && info != nil {
			version = strings.TrimSpace(info.GitVersion)
			if len(version) > 64 {
				version = version[:64]
			}
			distribution = kubernetesDistribution(version)
		}
	}
	return SREContext{
		Schema: SREContextSchema, InstallationID: connection.InstallationID.String(),
		ChartVersion: p.chart, Namespace: p.namespace, Release: p.release,
		KubernetesVersion: version, KubernetesDistribution: distribution,
		Trigger: strings.TrimSpace(trigger), CurrentUIContext: strings.TrimSpace(currentUI),
		Resources: append([]SessionResource(nil), resources...), CorrelationRef: uuid.NewString(),
	}, nil
}
