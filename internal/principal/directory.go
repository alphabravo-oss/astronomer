// Package principal provides provider-neutral external identity discovery.
// Connectors without a safe directory API are reported as unsupported; they
// are never approximated with public profile search or unverified identities.
package principal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	MinQueryLength = 3
	MaxQueryLength = 128
	MaxResults     = 25
)

var ErrUnsupported = errors.New("principal discovery is unsupported")

type Connector struct {
	ID          uuid.UUID
	Name        string
	Type        string
	DisplayName string
	Config      map[string]any
}

type ExternalPrincipal struct {
	ConnectorID   uuid.UUID `json:"connector_id"`
	ConnectorName string    `json:"connector_name"`
	ConnectorType string    `json:"connector_type"`
	Subject       string    `json:"subject"`
	Email         string    `json:"email"`
	Username      string    `json:"username"`
	DisplayName   string    `json:"display_name"`
}

type ConnectorStatus struct {
	ConnectorID   uuid.UUID `json:"connector_id"`
	ConnectorName string    `json:"connector_name"`
	ConnectorType string    `json:"connector_type"`
	Supported     bool      `json:"supported"`
	Error         string    `json:"error,omitempty"`
}

type Adapter interface {
	Type() string
	Search(context.Context, Connector, string, int) ([]ExternalPrincipal, error)
	Resolve(context.Context, Connector, string) (ExternalPrincipal, error)
}

type Directory struct {
	adapters map[string]Adapter
}

func NewDirectory(adapters ...Adapter) *Directory {
	registry := make(map[string]Adapter, len(adapters))
	for _, adapter := range adapters {
		if adapter != nil {
			registry[adapter.Type()] = adapter
		}
	}
	return &Directory{adapters: registry}
}

func (d *Directory) Search(ctx context.Context, connectors []Connector, query string, limit int) ([]ExternalPrincipal, []ConnectorStatus) {
	query = strings.TrimSpace(query)
	limit = min(max(limit, 1), MaxResults)
	type outcome struct {
		index      int
		principals []ExternalPrincipal
		status     ConnectorStatus
	}
	outcomes := make(chan outcome, len(connectors))
	var wg sync.WaitGroup
	for index, connector := range connectors {
		status := ConnectorStatus{ConnectorID: connector.ID, ConnectorName: connector.Name, ConnectorType: connector.Type}
		adapter, supported := d.adapters[connector.Type]
		if !supported {
			status.Error = ErrUnsupported.Error()
			outcomes <- outcome{index: index, status: status}
			continue
		}
		wg.Add(1)
		go func(index int, connector Connector, adapter Adapter, status ConnectorStatus) {
			defer wg.Done()
			status.Supported = true
			found, err := adapter.Search(ctx, connector, query, limit)
			if err != nil {
				status.Error = "directory search failed"
				found = nil
			}
			outcomes <- outcome{index: index, principals: found, status: status}
		}(index, connector, adapter, status)
	}
	wg.Wait()
	close(outcomes)

	ordered := make([]outcome, len(connectors))
	for result := range outcomes {
		ordered[result.index] = result
	}
	principals := make([]ExternalPrincipal, 0, limit)
	statuses := make([]ConnectorStatus, 0, len(connectors))
	for _, result := range ordered {
		statuses = append(statuses, result.status)
		for _, candidate := range result.principals {
			if len(principals) == limit {
				break
			}
			principals = append(principals, candidate)
		}
	}
	sort.SliceStable(principals, func(i, j int) bool {
		return strings.ToLower(principals[i].DisplayName+principals[i].Email) < strings.ToLower(principals[j].DisplayName+principals[j].Email)
	})
	return principals, statuses
}

func (d *Directory) Resolve(ctx context.Context, connector Connector, subject string) (ExternalPrincipal, error) {
	adapter, ok := d.adapters[connector.Type]
	if !ok {
		return ExternalPrincipal{}, fmt.Errorf("%w for connector type %s", ErrUnsupported, connector.Type)
	}
	if strings.TrimSpace(subject) == "" || len(subject) > 512 {
		return ExternalPrincipal{}, errors.New("principal subject is invalid")
	}
	return adapter.Resolve(ctx, connector, subject)
}
