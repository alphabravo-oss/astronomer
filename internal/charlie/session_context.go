package charlie

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
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
}

func NewProductSessionContextProvider(queries sessionContextQueries, namespace, release, chart string) (*ProductSessionContextProvider, error) {
	if queries == nil || len(namespace) > 63 || len(release) > 128 || len(chart) > 64 {
		return nil, fmt.Errorf("Charlie product context configuration is invalid")
	}
	return &ProductSessionContextProvider{queries: queries, namespace: strings.TrimSpace(namespace), release: strings.TrimSpace(release), chart: strings.TrimSpace(chart)}, nil
}

func (p *ProductSessionContextProvider) Context(ctx context.Context, resources []SessionResource, trigger, currentUI string) (SREContext, error) {
	connection, err := p.queries.GetActiveCharlieConnection(ctx)
	if err != nil || connection.InstallationID == uuid.Nil {
		return SREContext{}, fmt.Errorf("Charlie installation context is unavailable")
	}
	return SREContext{
		Schema: SREContextSchema, InstallationID: connection.InstallationID.String(),
		ChartVersion: p.chart, Namespace: p.namespace, Release: p.release,
		Trigger: strings.TrimSpace(trigger), CurrentUIContext: strings.TrimSpace(currentUI),
		Resources: append([]SessionResource(nil), resources...), CorrelationRef: uuid.NewString(),
	}, nil
}
