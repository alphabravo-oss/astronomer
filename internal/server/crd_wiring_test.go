package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type crdDecommissionTxFake struct {
	row      sqlc.ClusterDecommission
	tasks    []sqlc.UpsertTaskOutboxParams
	audits   []sqlc.UpsertAuditOutboxParams
	taskErr  error
	auditErr error
}

func (f *crdDecommissionTxFake) CreateClusterDecommission(_ context.Context, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error) {
	f.row = sqlc.ClusterDecommission{ID: uuid.New(), ClusterID: arg.ClusterID, ClusterName: arg.ClusterName, Status: tasks.PhaseStatusPending}
	return f.row, nil
}

func (f *crdDecommissionTxFake) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if f.taskErr != nil {
		return sqlc.TaskOutbox{}, f.taskErr
	}
	f.tasks = append(f.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), DedupeKey: arg.DedupeKey, TaskType: arg.TaskType, Payload: arg.Payload, QueueName: arg.QueueName}, nil
}

func (f *crdDecommissionTxFake) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if f.auditErr != nil {
		return sqlc.AuditOutbox{}, f.auditErr
	}
	f.audits = append(f.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, DedupeKey: arg.DedupeKey, Action: arg.Action}, nil
}

func TestCRDClusterDecommissionCommitsRowTaskAndSystemAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskErr    error
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "task intent failure rolls back", taskErr: errors.New("task outbox unavailable"), wantErr: true},
		{name: "audit intent failure rolls back", auditErr: errors.New("audit outbox unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var committedRows []sqlc.ClusterDecommission
			var committedTasks []sqlc.UpsertTaskOutboxParams
			var committedAudits []sqlc.UpsertAuditOutboxParams
			service := &crdClusterDecommissionService{runTx: func(_ context.Context, fn func(crdClusterDecommissionMutationTx) error) error {
				tx := &crdDecommissionTxFake{taskErr: tc.taskErr, auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedRows = append(committedRows, tx.row)
				committedTasks = append(committedTasks, tx.tasks...)
				committedAudits = append(committedAudits, tx.audits...)
				return nil
			}}
			cluster := sqlc.Cluster{ID: uuid.New(), Name: "prod-crd"}

			row, err := service.Request(context.Background(), cluster)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Request error = %v, wantErr=%v", err, tc.wantErr)
			}
			if len(committedRows) != tc.wantCommit || len(committedTasks) != tc.wantCommit || len(committedAudits) != tc.wantCommit {
				t.Fatalf("committed row/task/audit = %d/%d/%d, want %d each", len(committedRows), len(committedTasks), len(committedAudits), tc.wantCommit)
			}
			if tc.wantCommit == 0 {
				return
			}
			if row.ID == uuid.Nil || committedTasks[0].QueueName != tasks.ClusterTemplateApplyQueueName || committedTasks[0].TaskType != tasks.ClusterDecommissionType {
				t.Fatalf("task intent = %#v", committedTasks[0])
			}
			var payload tasks.ClusterDecommissionPayload
			if err := json.Unmarshal(committedTasks[0].Payload, &payload); err != nil || payload.DecommissionID != row.ID.String() {
				t.Fatalf("task payload = %#v, error=%v", payload, err)
			}
			auditRow := committedAudits[0]
			if auditRow.Action != "cluster.decommission.requested" || auditRow.Source != "server" || auditRow.ActorAuthMethod != "system" || auditRow.ActionClass != "system" {
				t.Fatalf("system audit intent = %#v", auditRow)
			}
		})
	}
}

func TestClusterAnnotationsWithAdoptionPolicy(t *testing.T) {
	got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
		Annotations: map[string]string{"existing": "kept"},
		AdoptionPolicy: crd.ClusterAdoptionPolicySpec{
			Mode:                   "auto",
			AllowedManagementModes: []string{"helm", "", "flux"},
		},
	}, nil)

	if got["existing"] != "kept" {
		t.Fatalf("existing annotation lost: %+v", got)
	}
	if got["management.astronomer.io/adoption-policy-mode"] != "auto" {
		t.Fatalf("adoption policy mode = %q", got["management.astronomer.io/adoption-policy-mode"])
	}
	if got["management.astronomer.io/allowed-management-modes"] != "flux,helm" {
		t.Fatalf("allowed management modes = %q", got["management.astronomer.io/allowed-management-modes"])
	}
}

// TestClusterCRCannotWriteImpersonationMode pins the CRD path as a NON-writer of
// the downstream-impersonation flag.
//
// PRE-FIX: clusterAnnotationsWithAgentProfile copied spec.Annotations verbatim
// and UpdateCluster replaces clusters.annotations wholesale, so the CR was a
// second, ungated writer of the key that the REST path gates on superuser plus
// the agent capability advertisement — and it also silently cleared a
// superuser-set mode on the next sync.
func TestClusterCRCannotWriteImpersonationMode(t *testing.T) {
	t.Run("a CR cannot raise the mode", func(t *testing.T) {
		got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
			Annotations: map[string]string{
				"existing":              "kept",
				callerid.ModeAnnotation: string(callerid.ModeEnforce),
			},
		}, json.RawMessage(`{}`))

		if _, present := got[callerid.ModeAnnotation]; present {
			t.Fatalf("CR raised the impersonation mode: %+v", got)
		}
		if got["existing"] != "kept" {
			t.Fatalf("unrelated annotation lost: %+v", got)
		}
	})

	t.Run("a CR cannot clear a superuser-set mode", func(t *testing.T) {
		stored := json.RawMessage(`{"` + callerid.ModeAnnotation + `":"attribute"}`)
		got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
			Annotations: map[string]string{"existing": "kept"},
		}, stored)

		if got[callerid.ModeAnnotation] != string(callerid.ModeAttribute) {
			t.Fatalf("stored mode not preserved across CRD sync: %+v", got)
		}
	})

	t.Run("a CR cannot lower a superuser-set mode", func(t *testing.T) {
		stored := json.RawMessage(`{"` + callerid.ModeAnnotation + `":"enforce"}`)
		got := clusterAnnotationsWithAgentProfile(crd.ClusterSpec{
			Annotations: map[string]string{callerid.ModeAnnotation: string(callerid.ModeOff)},
		}, stored)

		if got[callerid.ModeAnnotation] != string(callerid.ModeEnforce) {
			t.Fatalf("CR lowered the impersonation mode: %+v", got)
		}
	})
}
