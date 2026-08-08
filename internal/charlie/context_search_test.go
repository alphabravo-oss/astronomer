package charlie

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type contextSearchFake struct {
	rows []sqlc.CharlieAgentFleetListRow
}

func (f contextSearchFake) CharlieAgentFleetList(context.Context, sqlc.CharlieAgentFleetListParams) ([]sqlc.CharlieAgentFleetListRow, error) {
	return f.rows, nil
}

func TestContextSearchExposesAgentMetadataNotDownstreamResources(t *testing.T) {
	actor, clusterID := uuid.New(), uuid.New()
	authorizer := &sessionAuthorizerFake{use: true, incident: true}
	service := NewContextSearchService(contextSearchFake{rows: []sqlc.CharlieAgentFleetListRow{{ClusterID: clusterID, DisplayName: "Payments", Environment: "prod", Region: "east"}}}, authorizer, func() bool { return true })
	results, err := service.Search(context.Background(), actor, "payments", 20)
	if err != nil || len(results) != 1 {
		t.Fatalf("search=%#v err=%v", results, err)
	}
	if results[0].Type != "agent_connection_record" || results[0].ID != clusterID.String() || results[0].RequiredVerb != "read" {
		t.Fatalf("downstream record widened to resource access: %#v", results[0])
	}
	for _, forbidden := range []string{"pod", "namespace", "workload", "log", "kubernetes api"} {
		if strings.Contains(strings.ToLower(results[0].Summary), forbidden) {
			t.Fatalf("search result implies downstream access: %#v", results[0])
		}
	}
}

func TestContextSearchIsDormantAndLiveAuthorized(t *testing.T) {
	actor := uuid.New()
	inert := NewContextSearchService(contextSearchFake{}, &sessionAuthorizerFake{use: true, incident: true}, func() bool { return false })
	if _, err := inert.Search(context.Background(), actor, "agent", 20); err == nil {
		t.Fatal("disabled Charlie context search ran")
	}
	denied := NewContextSearchService(contextSearchFake{}, &sessionAuthorizerFake{use: false}, func() bool { return true })
	if _, err := denied.Search(context.Background(), actor, "agent", 20); err == nil {
		t.Fatal("unauthorized Charlie context search ran")
	}
}
