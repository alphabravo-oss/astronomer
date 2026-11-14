//go:build integration

package provisioning

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alphabravo-oss/identity/pkg/provision"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestJournalGenerationBounds(t *testing.T) {
	t.Parallel()
	j, intent := journalFixture(t)
	intent.Scope.Generation = math.MaxInt64
	row, err := j.Prepare(t.Context(), intent)
	if err != nil || row.Generation != math.MaxInt64 {
		t.Fatal("largest supported generation was not preserved")
	}
	intent.Scope.Generation++
	if _, err := j.Prepare(t.Context(), intent); !errors.Is(err, ErrInvalid) {
		t.Fatal("overflowing generation was not rejected")
	}
	if _, err := j.Claim(t.Context(), intent.Scope, row.ID, row.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatal("overflowing generation matched a persisted operation")
	}
}

func journalFixture(t *testing.T) (*Journal, Intent) {
	t.Helper()
	pool := testdb.Pool(t)
	j, err := NewJournal(pool)
	if err != nil {
		t.Fatal(err)
	}
	cluster, project, source, bundle, version, target, installation, actor := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	digest := "sha256:" + strings.Repeat("a", 64)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,email,username,is_active) VALUES($1,'provision@example.test','provision',true)`, []any{actor}},
		{`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, []any{cluster, cluster.String()}},
		{`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, []any{project, project.String(), cluster}},
		{`INSERT INTO delivery_sources(id,project_id,name,source_type,url) VALUES($1,$2,'source','helm_http','https://example.invalid/charts')`, []any{source, project}},
		{`INSERT INTO component_bundles(id,project_id,name) VALUES($1,$2,'bundle')`, []any{bundle, project}},
		{`INSERT INTO component_bundle_versions(id,bundle_id,source_id,version,renderer,requested_revision,spec_digest) VALUES($1,$2,$3,'1','helm','1',$4)`, []any{version, bundle, source, digest}},
		{`INSERT INTO delivery_targets(id,project_id,name,bundle_version_id) VALUES($1,$2,'target',$3)`, []any{target, project, version}},
		{`INSERT INTO installed_charts(id,cluster_id,release_name,request_id,status) VALUES($1,$2,'provision',$3,'installed')`, []any{installation, cluster, target}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(t.Context(), statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return j, Intent{Scope: Scope{Issuer: "https://identity.example.test", ProjectID: project, InstallationID: installation, Generation: 1},
		CredentialNamespace: "credential-authority", DesiredSpecDigest: digest, ActorID: actor, CorrelationID: uuid.New()}
}

func TestJournalConcurrentClaimsAndCrashRecovery(t *testing.T) {
	t.Parallel()
	j, intent := journalFixture(t)
	row, err := j.Prepare(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleJournalClaim(t, j, intent, row)
	restarted, err := NewJournal(j.pool)
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := restarted.Prepare(t.Context(), intent)
	if err != nil || interrupted.State != "provisioning" || interrupted.Revision != 2 {
		t.Fatal("restart reset ambiguous work")
	}
	if _, err := restarted.Claim(t.Context(), intent.Scope, row.ID, interrupted.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("interrupted work was reclaimed")
	}
	recovery, err := restarted.RequireRecovery(t.Context(), intent.Scope, row.ID, interrupted.Revision)
	if err != nil || recovery.State != "recovery_required" || recovery.Revision != 3 {
		t.Fatal("recovery transition failed")
	}
	if _, err := restarted.Claim(t.Context(), intent.Scope, row.ID, recovery.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("recovery permitted another creation")
	}
	changed := intent
	changed.DesiredSpecDigest = "sha256:" + strings.Repeat("b", 64)
	if _, err := restarted.Prepare(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("same generation accepted changed spec")
	}
	var count int
	if err := j.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_outbox WHERE resource_id=$1`, row.ID.String()).Scan(&count); err != nil || count != 3 {
		t.Fatal("audit did not match durable transitions")
	}
}

func TestJournalScopeAndAuditFailureAreClosed(t *testing.T) {
	t.Parallel()
	j, intent := journalFixture(t)
	wrong := intent
	wrong.Scope.ProjectID = uuid.New()
	if _, err := j.Prepare(t.Context(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign project accepted")
	}
	if _, err := j.pool.Exec(t.Context(), `CREATE FUNCTION reject_identity_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$;
 CREATE TRIGGER reject_identity_audit BEFORE INSERT ON audit_outbox FOR EACH ROW EXECUTE FUNCTION reject_identity_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Prepare(t.Context(), intent); !errors.Is(err, ErrUnavailable) {
		t.Fatal("intent committed without audit")
	}
	var count int
	if err := j.pool.QueryRow(t.Context(), `SELECT count(*) FROM identity_provisioning_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("audit failure left durable intent")
	}
	if _, err := j.pool.Exec(t.Context(), `DROP TRIGGER reject_identity_audit ON audit_outbox`); err != nil {
		t.Fatal(err)
	}
	row, err := j.Prepare(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Claim(t.Context(), wrong.Scope, row.ID, row.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("claim crossed project scope")
	}
	if _, err := j.pool.Exec(t.Context(), `CREATE TRIGGER reject_identity_audit BEFORE INSERT ON audit_outbox FOR EACH ROW EXECUTE FUNCTION reject_identity_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Claim(t.Context(), intent.Scope, row.ID, row.Revision); !errors.Is(err, ErrUnavailable) {
		t.Fatal("claim committed without audit")
	}
	current, err := sqlc.New(j.pool).GetIdentityProvisioningOperation(t.Context(), row.ID)
	if err != nil || current.State != "pending" || current.Revision != 1 {
		t.Fatal("failed audit advanced operation")
	}
	if _, err := j.pool.Exec(t.Context(), `DROP TRIGGER reject_identity_audit ON audit_outbox; UPDATE installed_charts SET status='uninstalling'`); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Claim(t.Context(), intent.Scope, row.ID, row.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("claim proceeded after uninstall began")
	}
}

func TestJournalCompletionRequiresDurableCredentialAndCurrentRevision(t *testing.T) {
	t.Parallel()
	client, namespace := credentialTestCluster(t)
	j, intent := journalFixture(t)
	intent.CredentialNamespace = namespace
	row, err := j.Prepare(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := j.Claim(t.Context(), intent.Scope, row.ID, row.Revision)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSecretStore(client.CoreV1(), namespace, intent.Scope, credentialTestCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Complete(t.Context(), store, row.ID, claimed.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing credential accepted as ready")
	}
	result := provision.Result{OrgID: "org", ProjectID: "project", AppID: "app", ClientID: "client", ClientSecret: uuid.NewString(), Created: true}
	if err := store.Put(t.Context(), intent.Scope.ProjectName(), result); err != nil {
		t.Fatal(err)
	}
	recovery, err := j.RequireRecovery(t.Context(), intent.Scope, row.ID, claimed.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Complete(t.Context(), store, row.ID, claimed.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("late worker bypassed recovery fence")
	}
	ready, err := j.Complete(t.Context(), store, row.ID, recovery.Revision)
	if err != nil || ready.State != "ready" || ready.CredentialSecretName != store.Name() || ready.UpstreamClientID != result.ClientID {
		t.Fatal("durable recovery failed")
	}
	var persisted string
	if err := j.pool.QueryRow(t.Context(), `SELECT jsonb_build_object('operations',(SELECT jsonb_agg(to_jsonb(o)) FROM identity_provisioning_operations o),'audit',(SELECT jsonb_agg(to_jsonb(a)) FROM audit_outbox a))::text`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, result.ClientSecret) {
		t.Fatal("plaintext credential leaked into journal or audit")
	}
	if _, err := j.pool.Exec(t.Context(), `DELETE FROM installed_charts WHERE id=$1`, intent.Scope.InstallationID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlc.New(j.pool).GetIdentityProvisioningOperation(t.Context(), row.ID); err != nil {
		t.Fatal("uninstall erased provisioning provenance")
	}
}

func assertSingleJournalClaim(t *testing.T, j *Journal, intent Intent, row sqlc.IdentityProvisioningOperation) {
	t.Helper()
	var wg sync.WaitGroup
	var claims atomic.Int32
	for range 8 {
		wg.Go(func() {
			retry, err := j.Prepare(t.Context(), intent)
			if err != nil || retry.ID != row.ID {
				t.Error("idempotent prepare changed operation")
			}
			_, err = j.Claim(t.Context(), intent.Scope, row.ID, row.Revision)
			if err == nil {
				claims.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("concurrent workers both claimed external mutation")
	}
}
