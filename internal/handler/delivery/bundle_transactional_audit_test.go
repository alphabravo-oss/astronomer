package delivery

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
)

type stagedBundleMutationTx struct {
	BundleMutationTx
	version  sqlc.ComponentBundleVersion
	tasks    int
	audits   []sqlc.UpsertAuditOutboxParams
	taskErr  error
	auditErr error
}

func (tx *stagedBundleMutationTx) CreateComponentBundleVersion(_ context.Context, arg sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error) {
	tx.version = sqlc.ComponentBundleVersion{ID: uuid.New(), BundleID: arg.BundleID, SourceID: arg.SourceID, Version: arg.Version}
	return tx.version, nil
}

func (tx *stagedBundleMutationTx) CreateDeliverySourceResolutionAndOutbox(_ context.Context, arg sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error) {
	if tx.taskErr != nil {
		return sqlc.CreateDeliverySourceResolutionAndOutboxRow{}, tx.taskErr
	}
	tx.tasks++
	return sqlc.CreateDeliverySourceResolutionAndOutboxRow{ID: uuid.New(), SourceID: arg.SourceID, Status: "pending"}, nil
}

func (tx *stagedBundleMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestBundleVersionTaskAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskErr    error
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit all", wantCommit: 1},
		{name: "resolution task failure rolls back version", taskErr: errors.New("task unavailable"), wantErr: true},
		{name: "audit failure rolls back version and task", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedVersions, committedTasks, committedAudits := 0, 0, 0
			h := NewBundleHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(BundleMutationTx) error) error {
				tx := &stagedBundleMutationTx{taskErr: tc.taskErr, auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedVersions++
				committedTasks += tx.tasks
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/bundles/b/versions/", nil)
			params := sqlc.CreateComponentBundleVersionParams{BundleID: uuid.New(), SourceID: uuid.New(), Version: "2026.08"}

			_, err := executeBundleMutation(r, h,
				func(q BundleMutationTx) (bundleVersionMutationResult, error) {
					version, mutationErr := q.CreateComponentBundleVersion(r.Context(), params)
					if mutationErr != nil {
						return bundleVersionMutationResult{}, mutationErr
					}
					resolution, mutationErr := q.CreateDeliverySourceResolutionAndOutbox(r.Context(), sqlc.CreateDeliverySourceResolutionAndOutboxParams{SourceID: params.SourceID})
					return bundleVersionMutationResult{version: version, resolution: resolution}, mutationErr
				},
				func() (bundleVersionMutationResult, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return bundleVersionMutationResult{}, nil
				},
				func(result bundleVersionMutationResult) deliveryAuditEvent {
					return deliveryAuditEvent{action: "delivery.bundle.version_created", resourceType: "component_bundle_version", resourceID: result.version.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedVersions != tc.wantCommit || committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed version/task/audit = %d/%d/%d, want %d each", committedVersions, committedTasks, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryDeliveryBundleMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("bundle.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false, "CreateVersion": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeBundleMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeBundleMutation", name)
		}
	}
}
