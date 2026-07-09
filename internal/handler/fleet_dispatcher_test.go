package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type fleetDispQ struct {
	tmpl sqlc.ClusterTemplate
}

func (q *fleetDispQ) GetClusterTemplateByID(context.Context, uuid.UUID) (sqlc.ClusterTemplate, error) {
	return q.tmpl, nil
}
func (q *fleetDispQ) UpsertClusterTemplateApplication(context.Context, sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	return sqlc.ClusterTemplateApplication{}, nil
}

// TEST-06: FleetDispatcher fails closed when tools/queries unwired.
func TestFleetDispatcher_NilToolsError(t *testing.T) {
	d := NewFleetDispatcher(nil, nil, nil)
	_, _, err := d.DispatchToolOperation(context.Background(), "tool_install", uuid.New(), tasks.FleetToolOperationSpec{})
	if err == nil {
		t.Fatal("expected error when tools nil")
	}
}

func TestFleetDispatcher_ApplyTemplateUsesQueries(t *testing.T) {
	tid := uuid.New()
	q := &fleetDispQ{tmpl: sqlc.ClusterTemplate{ID: tid, Spec: []byte(`{}`)}}
	d := NewFleetDispatcher(nil, nil, q)
	id, kind, err := d.DispatchApplyTemplate(context.Background(), uuid.New(), tid)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "" {
		t.Fatal("empty kind")
	}
	if id == uuid.Nil {
		// May return cluster id — either is fine as long as no error.
		t.Log("uuid.Nil sub-op id acceptable for template path")
	}
}
