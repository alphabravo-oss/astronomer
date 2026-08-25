package handler

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type stagedAlertingMutationTx struct {
	AlertingMutationTx
	channel  sqlc.NotificationChannel
	audits   []sqlc.UpsertAuditOutboxParams
	tasks    []sqlc.UpsertTaskOutboxParams
	auditErr error
}

func (tx *stagedAlertingMutationTx) CreateNotificationChannel(_ context.Context, arg sqlc.CreateNotificationChannelParams) (sqlc.NotificationChannel, error) {
	tx.channel = sqlc.NotificationChannel{
		ID: uuid.New(), Name: arg.Name, ChannelType: arg.ChannelType,
		Configuration: arg.Configuration, Enabled: arg.Enabled,
	}
	return tx.channel, nil
}

func (tx *stagedAlertingMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func (tx *stagedAlertingMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType}, nil
}

func TestAlertingStateAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit channel and audit", wantCommit: 1},
		{name: "audit failure rolls back channel", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedChannels, committedAudits := 0, 0
			h := NewAlertingHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(AlertingMutationTx) error) error {
				tx := &stagedAlertingMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.channel.ID != uuid.Nil {
					committedChannels++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/alerting/channels/", nil)
			params := sqlc.CreateNotificationChannelParams{Name: "production", ChannelType: "slack", Configuration: []byte(`{}`), Enabled: true}

			_, err := executeAlertingMutation(r, h,
				func(q AlertingMutationTx) (sqlc.NotificationChannel, error) {
					return q.CreateNotificationChannel(r.Context(), params)
				},
				func() (sqlc.NotificationChannel, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.NotificationChannel{}, nil
				},
				func(row sqlc.NotificationChannel) clusterAuditEvent {
					return clusterAuditEvent{action: "alert.channel.create", resourceType: "notification_channel", resourceID: row.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedChannels != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed channel/audit = %d/%d, want %d each", committedChannels, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestAlertingTaskOutboxAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit task and audit", wantCommit: 1},
		{name: "audit failure rolls back task", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedTasks, committedAudits := 0, 0
			h := NewAlertingHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(AlertingMutationTx) error) error {
				tx := &stagedAlertingMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedTasks += len(tx.tasks)
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/alerting/channels/test/test/", nil)
			task, err := tasks.NewNotificationSendTask(tasks.NotificationSendPayload{Channel: "slack", Recipients: []string{"https://hooks.example.test"}})
			if err != nil {
				t.Fatal(err)
			}
			channel := sqlc.NotificationChannel{ID: uuid.New(), Name: "test", ChannelType: "slack"}

			_, err = executeAlertingMutation(r, h,
				func(q AlertingMutationTx) (sqlc.NotificationChannel, error) {
					_, enqueueErr := tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{DedupeKey: uuid.NewString()})
					return channel, enqueueErr
				},
				func() (sqlc.NotificationChannel, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.NotificationChannel{}, nil
				},
				func(row sqlc.NotificationChannel) clusterAuditEvent {
					return clusterAuditEvent{action: "alert.channel.test", resourceType: "notification_channel", resourceID: row.ID.String(), status: http.StatusOK}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed task/audit = %d/%d, want %d each", committedTasks, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryAlertingMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("alerting.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateChannel": false, "UpdateChannel": false, "TestChannel": false, "DeleteChannel": false,
		"CreateRule": false, "UpdateRule": false, "DeleteRule": false, "setRuleEnabled": false,
		"AcknowledgeEvent": false, "ResolveEvent": false,
		"CreateSilence": false, "ExpireSilence": false, "DeleteSilence": false,
		"CreateInhibition": false, "UpdateInhibition": false, "DeleteInhibition": false,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeAlertingMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeAlertingMutation", name)
		}
	}
}
