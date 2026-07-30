package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// storageConfigId is the one field of a per-cluster stack install that does NOT
// name an object of the routed cluster. It is a bare id into
// backup_storage_configs, a table the backups API gates on rbac.ResourceBackups
// and whose raw access/secret keys that API never surfaces at all — and the
// install writes exactly those keys into a plaintext Secret in a namespace of
// the caller's own cluster.
//
// While /clusters/{id}/monitoring/... resolved to a GLOBAL monitoring check,
// "the caller got here" already implied a fleet-wide grant and dereferencing a
// fleet-wide storage config was not an escalation. ClusterScopeFromIDParam
// admits single-cluster tenants to those routes, so the reference now needs its
// own authorization: clusterStorageConfigAuthorizer.
//
// These tests drive the real InstallStack handler behind the real
// RequirePermission middleware with the real ClusterScopeFromIDParam
// declaration, i.e. the production chain from internal/server/routes_clusters.go.

// storageAuthzCluster / storageAuthzOtherCluster are the routed cluster and a
// neighbouring tenant's.
var (
	storageAuthzCluster      = uuid.MustParse("3f1c5d8a-9b2e-4c77-8a10-6d5e4f3c2b1a")
	storageAuthzOtherCluster = uuid.MustParse("7e2d4c6b-1a3f-4589-9c02-8b7a6d5e4f3c")
)

// clusterMonitoringBinding is a monitoring grant scoped to exactly one cluster,
// the shape a cluster_role_bindings row produces for a Cluster Owner.
func clusterMonitoringBinding(clusterID uuid.UUID, verbs ...rbac.Verb) rbac.RoleBinding {
	values := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		values = append(values, string(verb))
	}
	return rbac.RoleBinding{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceMonitoring), Verbs: values}},
	}
}

// clusterBackupsReadBinding is a backups:read grant at one scope; pass
// uuid.Nil for the global (unscoped) form.
func clusterBackupsReadBinding(clusterID uuid.UUID) rbac.RoleBinding {
	binding := rbac.RoleBinding{
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceBackups), Verbs: []string{string(rbac.VerbRead)}}},
	}
	if clusterID != uuid.Nil {
		binding.ClusterID = clusterID.String()
	}
	return binding
}

func globalMonitoringBinding(verbs ...rbac.Verb) rbac.RoleBinding {
	values := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		values = append(values, string(verb))
	}
	return rbac.RoleBinding{
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceMonitoring), Verbs: values}},
	}
}

// storageConfigRow is a backup_storage_configs row. cluster is uuid.Nil for the
// fleet-wide (unscoped) config every deployment has as its default.
func storageConfigRow(id uuid.UUID, cluster uuid.UUID) sqlc.BackupStorageConfig {
	row := sqlc.BackupStorageConfig{
		ID:        id,
		Bucket:    "metrics",
		Region:    "us-east-1",
		AccessKey: "AKIA-LIVE-KEY",
		SecretKey: "live-secret-key",
	}
	if cluster != uuid.Nil {
		row.ClusterID = pgtype.UUID{Bytes: cluster, Valid: true}
	}
	return row
}

// installWithStorageConfig runs POST /clusters/{routed}/monitoring/stack/install/
// through the production middleware chain and returns the response recorder.
func installWithStorageConfig(t *testing.T, routed uuid.UUID, storage sqlc.BackupStorageConfig, bindings []rbac.RoleBinding) *httptest.ResponseRecorder {
	t.Helper()
	return stackRouteWithStorageConfig(t, routed, storage, bindings, rbac.VerbCreate)
}

// stackRouteWithStorageConfig is installWithStorageConfig for an arbitrary
// route verb: preview is monitoring:read, install create, upgrade/replace
// update. The verb matters to the authorizer's pre-fix-parity clause, so the
// read route is exercised separately from the write ones.
func stackRouteWithStorageConfig(t *testing.T, routed uuid.UUID, storage sqlc.BackupStorageConfig, bindings []rbac.RoleBinding, routeVerb rbac.Verb) *httptest.ResponseRecorder {
	t.Helper()
	h, q := newStackLifecycleHandler(t)
	q.storage = storage
	engine := rbac.NewEngine()
	querier := stubMonitoringRBACQuerier{bindings: bindings}
	h.SetAuthorization(engine, querier)

	// A zero-id row means "no storageConfigId in the body at all" — the control
	// request that proves a 403 came from the reference and not the route gate.
	body := `{"releaseName":"prometheus","namespace":"monitoring"}`
	if storage.ID != uuid.Nil {
		body = `{"releaseName":"prometheus","namespace":"monitoring","storageConfigId":"` + storage.ID.String() + `"}`
	}
	route := "install"
	target := h.InstallStack
	if routeVerb == rbac.VerbRead {
		route, target = "preview", h.PreviewStack
	}
	tc := stackLifecycleCase{
		method: http.MethodPost,
		target: "/api/v1/clusters/" + routed.String() + "/monitoring/stack/" + route + "/",
		body:   body,
		params: map[string]string{"id": routed.String()},
	}

	// The production chain, in order: the subtree declaration that makes {id}
	// resolve as the cluster, then the route's own permission gate.
	chain := middleware.ClusterScopeFromIDParam(
		middleware.RequirePermission(engine, querier, rbac.ResourceMonitoring, routeVerb)(
			http.HandlerFunc(target)))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, tc.request())
	return rec
}

// TestClusterStackRefusesStorageConfigTheCallerHasNoGrantOn is the denial. The
// caller is a Cluster Owner on the routed cluster and holds nothing on backups,
// which is exactly the principal ClusterScopeFromIDParam newly admits. The
// storage config they name is the fleet-wide default — a row they cannot read
// through /settings/backups/storage/ and whose keys that API would not return
// even if they could.
//
// The 403 must come from the storage reference, not from the route gate, so
// each case carries the positive control that the SAME caller installs fine
// with no storageConfigId at all.
func TestClusterStackRefusesStorageConfigTheCallerHasNoGrantOn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		storage sqlc.BackupStorageConfig
	}{
		{
			name:    "fleet-wide config",
			storage: storageConfigRow(uuid.New(), uuid.Nil),
		},
		{
			// Worse than the global case: these are another tenant's S3
			// credentials landing in a Secret on this caller's cluster.
			name:    "another cluster's config",
			storage: storageConfigRow(uuid.New(), storageAuthzOtherCluster),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings := []rbac.RoleBinding{clusterMonitoringBinding(storageAuthzCluster, rbac.VerbCreate, rbac.VerbUpdate, rbac.VerbRead)}
			rec := installWithStorageConfig(t, storageAuthzCluster, tc.storage, bindings)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("cluster-scoped monitoring caller dereferenced a storage config it holds no grant on: got %d, want 403; body=%s",
					rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "AKIA-LIVE-KEY") || strings.Contains(rec.Body.String(), "live-secret-key") {
				t.Fatalf("the refusal leaked the credentials it refused: %s", rec.Body.String())
			}

			// Control: without the cross-object reference the identical caller
			// gets past both the gate and the handler, so the 403 above is the
			// storage authorization and nothing else.
			control := installWithStorageConfig(t, storageAuthzCluster, sqlc.BackupStorageConfig{}, bindings)
			if control.Code == http.StatusForbidden {
				t.Fatalf("the same caller is 403 with NO storageConfigId, so this test is not measuring the storage reference; body=%s", control.Body.String())
			}
		})
	}
}

// TestClusterStackAllowsAuthorizedStorageConfigReferences is the other three
// directions. Each is a caller who SHOULD reach the config; a fix that denies
// any of them has broken the feature rather than secured it.
func TestClusterStackAllowsAuthorizedStorageConfigReferences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		storage  sqlc.BackupStorageConfig
		bindings []rbac.RoleBinding
	}{
		{
			// The routed cluster's OWN storage config. No backups grant needed:
			// it is an object of the cluster the caller already administers.
			name:     "config belongs to the routed cluster",
			storage:  storageConfigRow(uuid.New(), storageAuthzCluster),
			bindings: []rbac.RoleBinding{clusterMonitoringBinding(storageAuthzCluster, rbac.VerbCreate)},
		},
		{
			// A global config plus an explicit global backups:read — the rule
			// authorizeBackup applies to every other read of that row.
			name:    "global config with a global backups:read grant",
			storage: storageConfigRow(uuid.New(), uuid.Nil),
			bindings: []rbac.RoleBinding{
				clusterMonitoringBinding(storageAuthzCluster, rbac.VerbCreate),
				clusterBackupsReadBinding(uuid.Nil),
			},
		},
		{
			// REGRESSION FENCE. A Monitoring Admin is scope: global and holds
			// no backups grant, and installing a per-cluster stack against the
			// fleet's default storage config is the mainline flow. This is the
			// case that fails if the authorizer is tightened to "backups grant
			// or nothing".
			name:     "fleet-wide monitoring grant, no backups grant",
			storage:  storageConfigRow(uuid.New(), uuid.Nil),
			bindings: []rbac.RoleBinding{globalMonitoringBinding(rbac.VerbCreate, rbac.VerbUpdate, rbac.VerbRead)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := installWithStorageConfig(t, storageAuthzCluster, tc.storage, tc.bindings)
			if rec.Code == http.StatusForbidden {
				t.Fatalf("authorized caller was refused the storage config: %s", rec.Body.String())
			}
			if rec.Code != http.StatusAccepted {
				t.Fatalf("install did not succeed (%d): %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestStorageConfigParityClauseTracksTheRouteVerb pins the one clause that
// exists purely to preserve pre-fix behaviour, in both directions.
//
// Before ClusterScopeFromIDParam these routes were a GLOBAL monitoring check,
// so "could reach this handler" meant "holds a global monitoring grant AT THIS
// ROUTE'S VERB". The authorizer reproduces exactly that and no more:
//
//   - a global monitoring:read holder (Support Engineer) keeps preview, which
//     is a read route — dropping it would be a fix that took access away;
//   - the same holder does NOT get install, which is a create route they could
//     never reach — granting it would be a fix that handed access out.
func TestStorageConfigParityClauseTracksTheRouteVerb(t *testing.T) {
	globalConfig := storageConfigRow(uuid.New(), uuid.Nil)
	readOnly := []rbac.RoleBinding{
		globalMonitoringBinding(rbac.VerbRead),
		// Plus the cluster-scoped write grant, so the ROUTE gate admits the
		// install below and the only thing left to refuse it is the storage
		// reference.
		clusterMonitoringBinding(storageAuthzCluster, rbac.VerbCreate),
	}

	preview := stackRouteWithStorageConfig(t, storageAuthzCluster, globalConfig, readOnly, rbac.VerbRead)
	if preview.Code != http.StatusOK {
		t.Fatalf("global monitoring:read lost preview against a fleet-wide storage config: %d %s", preview.Code, preview.Body.String())
	}

	install := stackRouteWithStorageConfig(t, storageAuthzCluster, globalConfig, readOnly, rbac.VerbCreate)
	if install.Code != http.StatusForbidden {
		t.Fatalf("global monitoring:READ reached the install path's storage config: got %d, want 403; body=%s", install.Code, install.Body.String())
	}
}

// TestObjstoreConfigUsesDecryptedCredentials pins the credential source.
//
// buildObjstoreConfigYAML read cfg.AccessKey/cfg.SecretKey directly, but
// backups.go BLANKS those columns whenever encrypted_credentials is populated
// (legacyBackupCredentialColumns), which is every deployment with an encryptor
// — production, by config.ValidateProductionSecurity. So the stack installed
// with an empty-credential objstore secret, silently, and Thanos failed to ship
// blocks with no error anywhere in Astronomer. Same class as the known
// gitops-creds-never-decrypted defect.
func TestObjstoreConfigUsesDecryptedCredentials(t *testing.T) {
	encryptor := newTestEncryptor(t)
	sealed, err := encryptor.Encrypt(`{"access_key":"AKIA-SEALED","secret_key":"sealed-secret"}`)
	if err != nil {
		t.Fatalf("seal credentials: %v", err)
	}
	cfg := sqlc.BackupStorageConfig{
		ID:                   uuid.New(),
		Bucket:               "metrics",
		Region:               "us-east-1",
		EncryptedCredentials: sealed,
		// Blank, exactly as legacyBackupCredentialColumns writes them.
		AccessKey: "",
		SecretKey: "",
	}

	t.Run("decrypts when an encryptor is wired", func(t *testing.T) {
		h := NewMonitoringHandler()
		h.SetEncryptor(encryptor)
		content, err := h.buildObjstoreConfigYAML(cfg)
		if err != nil {
			t.Fatalf("build objstore config: %v", err)
		}
		if !strings.Contains(content, "AKIA-SEALED") || !strings.Contains(content, "sealed-secret") {
			t.Fatalf("objstore config does not carry the decrypted credentials:\n%s", content)
		}
	})

	t.Run("fails loudly when it cannot open the row", func(t *testing.T) {
		h := NewMonitoringHandler()
		if _, err := h.buildObjstoreConfigYAML(cfg); err == nil {
			t.Fatal("built an objstore config from a sealed row with no encryptor — this is the silent blank-credential install")
		}
	})

	t.Run("legacy plaintext rows still work", func(t *testing.T) {
		h := NewMonitoringHandler()
		legacy := sqlc.BackupStorageConfig{Bucket: "metrics", AccessKey: "AKIA-LEGACY", SecretKey: "legacy-secret"}
		content, err := h.buildObjstoreConfigYAML(legacy)
		if err != nil {
			t.Fatalf("build objstore config: %v", err)
		}
		if !strings.Contains(content, "AKIA-LEGACY") {
			t.Fatalf("legacy plaintext credentials were dropped:\n%s", content)
		}
	})
}
