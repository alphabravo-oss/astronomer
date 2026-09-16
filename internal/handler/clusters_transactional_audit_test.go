package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedClusterMutationTx struct {
	ClusterMutationTx
	cluster   sqlc.Cluster
	auditRows []sqlc.UpsertAuditOutboxParams
	tokenRows []sqlc.CreateAPITokenParams
	updateArg sqlc.UpdateClusterParams
	lockReads int
	auditErr  error
}

type stagedClusterRegistryMutationTx struct {
	ClusterRegistryMutationTx
	row       sqlc.ClusterRegistryConfig
	taskRows  []sqlc.UpsertTaskOutboxParams
	auditRows []sqlc.UpsertAuditOutboxParams
	taskErr   error
	auditErr  error
}

func (tx *stagedClusterRegistryMutationTx) CreateClusterRegistryConfig(_ context.Context, arg sqlc.CreateClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error) {
	tx.row = sqlc.ClusterRegistryConfig{ID: uuid.New(), ClusterID: arg.ClusterID, PrivateRegistryUrl: arg.PrivateRegistryUrl}
	return tx.row, nil
}

func (tx *stagedClusterRegistryMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if tx.taskErr != nil {
		return sqlc.TaskOutbox{}, tx.taskErr
	}
	tx.taskRows = append(tx.taskRows, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType}, nil
}

func (tx *stagedClusterRegistryMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.auditRows = append(tx.auditRows, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func (tx *stagedClusterMutationTx) CreateCluster(_ context.Context, arg sqlc.CreateClusterParams) (sqlc.Cluster, error) {
	tx.cluster = sqlc.Cluster{ID: uuid.New(), Name: arg.Name, DisplayName: arg.DisplayName}
	return tx.cluster, nil
}

func (tx *stagedClusterMutationTx) GetClusterByIDForUpdate(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	tx.lockReads++
	return tx.cluster, nil
}

func (tx *stagedClusterMutationTx) UpdateCluster(_ context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	tx.updateArg = arg
	tx.cluster.DisplayName = arg.DisplayName
	tx.cluster.Description = arg.Description
	tx.cluster.Environment = arg.Environment
	tx.cluster.Region = arg.Region
	tx.cluster.Labels = arg.Labels
	tx.cluster.Annotations = arg.Annotations
	tx.cluster.ApiServerUrl = arg.ApiServerUrl.String
	tx.cluster.CaCertificate = arg.CaCertificate.String
	return tx.cluster, nil
}

func (tx *stagedClusterMutationTx) CreateAPIToken(_ context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error) {
	tx.tokenRows = append(tx.tokenRows, arg)
	return sqlc.ApiToken{ID: uuid.New(), UserID: arg.UserID, Name: arg.Name, TokenHash: arg.TokenHash}, nil
}

func (tx *stagedClusterMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.auditRows = append(tx.auditRows, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, ResourceType: arg.ResourceType}, nil
}

func TestExecuteClusterMutationCommitsClusterAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name             string
		auditErr         error
		wantErr          bool
		wantClusters     int
		wantAuditIntents int
	}{
		{name: "commit", wantClusters: 1, wantAuditIntents: 1},
		{name: "audit failure rolls back cluster", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var committedClusters []sqlc.Cluster
			var committedAudits []sqlc.UpsertAuditOutboxParams
			h := &ClusterHandler{}
			h.SetRunTx(func(_ context.Context, fn func(ClusterMutationTx) error) error {
				tx := &stagedClusterMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedClusters = append(committedClusters, tx.cluster)
				committedAudits = append(committedAudits, tx.auditRows...)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/", nil)
			params := sqlc.CreateClusterParams{Name: "prod-east", DisplayName: "Production East"}

			_, err := executeMutation(r, h.runTx,
				func(q ClusterMutationTx) (sqlc.Cluster, error) { return q.CreateCluster(r.Context(), params) },
				func(cluster sqlc.Cluster) mutationAuditEvent {
					return mutationAuditEvent{
						action: "cluster.create", resourceType: "cluster", resourceID: cluster.ID.String(),
						resourceName: cluster.Name, status: http.StatusCreated,
					}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("executeMutation error = %v, wantErr=%v", err, tc.wantErr)
			}
			if len(committedClusters) != tc.wantClusters || len(committedAudits) != tc.wantAuditIntents {
				t.Fatalf("committed clusters/audits = %d/%d, want %d/%d", len(committedClusters), len(committedAudits), tc.wantClusters, tc.wantAuditIntents)
			}
			if len(committedAudits) == 1 && (committedAudits[0].Action != "cluster.create" || committedAudits[0].ResourceType != "cluster") {
				t.Fatalf("audit intent = %#v", committedAudits[0])
			}
		})
	}
}

func TestUpdateClusterOneFieldPreservesLockedRow(t *testing.T) {
	id := uuid.New()
	existing := sqlc.Cluster{
		ID: id, Name: "prod", DisplayName: "Production", Description: "old",
		Environment: "production", Region: "us-east-1",
		Labels: json.RawMessage(`{"team":"platform"}`), Annotations: json.RawMessage(`{"owner":"ops"}`),
		ApiServerUrl: "https://api.example.test:6443", CaCertificate: "public-ca",
	}
	base := newFakeAutoAttachClusterQuerier()
	base.clusters[id] = existing
	tx := &stagedClusterMutationTx{cluster: existing}
	h := NewClusterHandler(base)
	h.SetRunTx(func(_ context.Context, fn func(ClusterMutationTx) error) error { return fn(tx) })

	r := httptest.NewRequest(http.MethodPatch, "/api/v1/clusters/"+id.String()+"/", bytes.NewBufferString(`{"description":"changed"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.Update(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if tx.lockReads != 1 {
		t.Fatalf("locked reads = %d, want 1", tx.lockReads)
	}
	if tx.updateArg.Description != "changed" || tx.updateArg.DisplayName != existing.DisplayName ||
		tx.updateArg.Environment != existing.Environment || tx.updateArg.Region != existing.Region ||
		string(tx.updateArg.Labels) != string(existing.Labels) || string(tx.updateArg.Annotations) != string(existing.Annotations) ||
		tx.updateArg.ApiServerUrl.String != existing.ApiServerUrl || tx.updateArg.CaCertificate.String != existing.CaCertificate {
		t.Fatalf("lossy partial update: %#v", tx.updateArg)
	}
}

func TestMergeClusterUpdateDirectAccessClearAndValidation(t *testing.T) {
	id := uuid.New()
	existing := sqlc.Cluster{ID: id, ApiServerUrl: "https://api.example.test:6443", CaCertificate: "public-ca"}
	empty := ""
	params, err := mergeClusterUpdate(id, existing, UpdateClusterRequest{ApiServerUrl: &empty, CaCertificate: &empty})
	if err != nil {
		t.Fatalf("explicit direct-access clear: %v", err)
	}
	if !params.ApiServerUrl.Valid || params.ApiServerUrl.String != "" || !params.CaCertificate.Valid || params.CaCertificate.String != "" {
		t.Fatalf("clear params = %#v", params)
	}
	malformed := "http://api.example.test:6443"
	if _, err := mergeClusterUpdate(id, existing, UpdateClusterRequest{ApiServerUrl: &malformed}); err == nil {
		t.Fatal("non-HTTPS direct endpoint unexpectedly accepted")
	}
}

func TestMintKubeconfigTokenCommitsTokenAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure rolls back token", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var committedTokens []sqlc.CreateAPITokenParams
			var committedAudits []sqlc.UpsertAuditOutboxParams
			h := &ClusterHandler{}
			h.SetRunTx(func(_ context.Context, fn func(ClusterMutationTx) error) error {
				tx := &stagedClusterMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedTokens = append(committedTokens, tx.tokenRows...)
				committedAudits = append(committedAudits, tx.auditRows...)
				return nil
			})
			actor := uuid.New()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/id/generate-kubeconfig/", nil)
			r = r.WithContext(reqctx.WithUser(r.Context(), &reqctx.User{ID: actor.String(), AuthMethod: "jwt"}))

			plaintext, _, err := h.mintKubeconfigToken(r, sqlc.Cluster{ID: uuid.New(), Name: "prod"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if len(committedTokens) != tc.wantCommit || len(committedAudits) != tc.wantCommit {
				t.Fatalf("committed token/audit = %d/%d, want %d each", len(committedTokens), len(committedAudits), tc.wantCommit)
			}
			if tc.wantCommit == 1 && (plaintext == "" || committedAudits[0].Action != "cluster.proxy_kubeconfig.issued" || string(committedTokens[0].Scopes) != `["read"]`) {
				t.Fatalf("credential transaction = plaintext=%t token=%#v audit=%#v", plaintext != "", committedTokens[0], committedAudits[0])
			}
		})
	}
}

func TestClusterRegistryMutationCommitsDomainTaskAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskErr    error
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit all", wantCommit: 1},
		{name: "task intent failure rolls back all", taskErr: errors.New("task outbox unavailable"), wantErr: true},
		{name: "audit intent failure rolls back all", auditErr: errors.New("audit outbox unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedRows, committedTasks, committedAudits := 0, 0, 0
			h := NewClusterRegistriesHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(ClusterRegistryMutationTx) error) error {
				tx := &stagedClusterRegistryMutationTx{taskErr: tc.taskErr, auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedRows++
				committedTasks += len(tx.taskRows)
				committedAudits += len(tx.auditRows)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c/registries/", nil)
			clusterID := uuid.New()
			params := sqlc.CreateClusterRegistryConfigParams{ClusterID: clusterID, PrivateRegistryUrl: "registry.example.com"}

			_, err := executeMutation(r, h.runTx,
				func(q ClusterRegistryMutationTx) (sqlc.ClusterRegistryConfig, error) {
					row, mutationErr := q.CreateClusterRegistryConfig(r.Context(), params)
					if mutationErr != nil {
						return sqlc.ClusterRegistryConfig{}, mutationErr
					}
					mutationErr = h.enqueueApplyOutbox(r, q, row.ID, clusterID)
					return row, mutationErr
				},
				func(row sqlc.ClusterRegistryConfig) mutationAuditEvent {
					return mutationAuditEvent{action: "cluster.registry.created", resourceType: "cluster_registry_config", resourceID: row.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedRows != tc.wantCommit || committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed domain/task/audit = %d/%d/%d, want %d each", committedRows, committedTasks, committedAudits, tc.wantCommit)
			}
		})
	}
}

type generatedClusterOwnershipFake struct {
	getRow sqlc.GetClusterOwnershipRow
	setRow sqlc.SetClusterOwnershipRow
}

func (f *generatedClusterOwnershipFake) GetClusterOwnership(context.Context, uuid.UUID) (sqlc.GetClusterOwnershipRow, error) {
	return f.getRow, nil
}

func (f *generatedClusterOwnershipFake) SetClusterOwnership(_ context.Context, arg sqlc.SetClusterOwnershipParams) (sqlc.SetClusterOwnershipRow, error) {
	f.setRow = sqlc.SetClusterOwnershipRow{ID: arg.ID, ManagedBy: arg.ManagedBy}
	return f.setRow, nil
}

func TestGeneratedClusterOwnershipSurfaceEnforcesAndTransfersCRDOwnership(t *testing.T) {
	id := uuid.New()
	q := &generatedClusterOwnershipFake{getRow: sqlc.GetClusterOwnershipRow{
		ID: id, ManagedBy: "crd", ExternalRefApiVersion: "platform.astronomer.io/v1alpha1",
		ExternalRefKind: "AstronomerCluster", ExternalRefNamespace: "platform", ExternalRefName: "prod-east",
	}}

	blocked, err := clusterUpdateBlockedByOwnership(context.Background(), q, id)
	if err != nil || blocked == "" {
		t.Fatalf("CRD-owned generated row block = %q, err=%v", blocked, err)
	}
	previous, updated, transferred, err := transferClusterOwnershipToAPI(context.Background(), q, id)
	if err != nil {
		t.Fatal(err)
	}
	if !transferred || previous.ManagedBy != "crd" || updated.ManagedBy != "api" {
		t.Fatalf("transfer = previous=%q updated=%q transferred=%v", previous.ManagedBy, updated.ManagedBy, transferred)
	}
}

func TestEveryClusterAdministrationMutationUsesTransactionalExecutor(t *testing.T) {
	assertHandlerMutationsUseExecutor(t, "ClusterHandler", []string{
		"Create", "Update", "TakeoverOwnership", "Delete",
		"GenerateRegistrationToken", "RotateAgentToken", "RevokeAgentToken",
		"GetManifest", "UpdateRegistryConfig", "DeleteRegistryConfig",
	})
}

func TestEveryMultiRegistryMutationUsesTransactionalExecutor(t *testing.T) {
	assertHandlerMutationsUseExecutor(t, "ClusterRegistriesHandler", []string{"Create", "Update", "Delete"})
}
