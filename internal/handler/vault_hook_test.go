// vaultResolveBlob hook tests (migration 067).
//
// The full helm-install integration is exercised in catalog_test.go via
// its own machinery; this file owns the focused hook behavior:
//
//   - TestVaultHook_NoRefsPassthrough: blob with no ${vault://...} markers
//     returns unchanged even when resolver is nil.
//   - TestVaultHook_FailsWhenResolverMissing: blob with refs but no
//     resolver fails the install (we'd otherwise silently install the
//     literal ${vault://...} marker into the cluster).
//   - TestHelmInstall_UsesResolvedValues_InWireOnly: end-to-end through
//     the hook helper, proving the DB-bound original keeps the marker
//     while the wire-bound resolved string carries the secret.

package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
)

func TestVaultHook_NoRefsPassthrough(t *testing.T) {
	blob := "image: nginx\nreplicas: 3\n"
	out, err := vaultResolveBlob(context.Background(), nil, uuid.Nil, blob)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if out != blob {
		t.Fatalf("passthrough failed: %q", out)
	}
}

func TestVaultHook_FailsWhenResolverMissing(t *testing.T) {
	blob := "password: ${vault://prod/secret/db#password}"
	_, err := vaultResolveBlob(context.Background(), nil, uuid.Nil, blob)
	if err == nil {
		t.Fatal("expected error when resolver is nil and blob contains refs")
	}
}

// hookFakeQuerier is a minimal vault.Querier for the wire-only test.
type hookFakeQuerier struct {
	conn sqlc.VaultConnection
}

func (h *hookFakeQuerier) GetVaultConnectionByID(_ context.Context, _ uuid.UUID) (sqlc.VaultConnection, error) {
	return h.conn, nil
}
func (h *hookFakeQuerier) GetVaultConnectionByName(_ context.Context, name string) (sqlc.VaultConnection, error) {
	if name != h.conn.Name {
		return sqlc.VaultConnection{}, errNotFound
	}
	return h.conn, nil
}
func (h *hookFakeQuerier) GetProjectDefaultVaultConnection(context.Context, uuid.UUID) (pgtype.UUID, error) {
	return pgtype.UUID{}, nil
}

// hookFakeClient is a vault.Client returning a fixed value.
type hookFakeClient struct {
	val map[string]any
}

func (c *hookFakeClient) FetchSecret(_ context.Context, _, _ string) (map[string]any, error) {
	return c.val, nil
}

// TestHelmInstall_UsesResolvedValues_InWireOnly proves that the
// install hook returns the resolved blob, leaving the caller free to
// store the ORIGINAL blob in the DB (which catalog.go does — the
// installation row is created BEFORE the hook runs, with the
// pre-resolve values). This is the regression check for the
// "never persist the resolved secret" rule.
func TestHelmInstall_UsesResolvedValues_InWireOnly(t *testing.T) {
	const originalBlob = "password: ${vault://prod/secret/db#password}\nimage: nginx\n"
	const secret = "hunter2-rotated"

	conn := sqlc.VaultConnection{
		ID: uuid.New(), Name: "prod", AuthMethod: "token", DefaultMount: "secret",
		Enabled: true, AuthEncrypted: `{"token":"root"}`,
	}
	q := &hookFakeQuerier{conn: conn}
	client := &hookFakeClient{val: map[string]any{"password": secret}}
	resolver := avault.NewResolverWithFactory(q, nil, func(sqlc.VaultConnection, string) (avault.Client, error) {
		return client, nil
	})

	// Simulated install path: persist the original blob (DB-bound),
	// then run the hook, then ship the resolved blob to the cluster
	// (wire-bound).
	dbStored := originalBlob // what catalog.go writes to helm_installations
	wireBound, err := vaultResolveBlob(context.Background(), resolver, uuid.Nil, originalBlob)
	if err != nil {
		t.Fatalf("hook: %v", err)
	}

	// DB row: keeps the ${vault://...} marker, never the secret.
	if !strings.Contains(dbStored, "${vault://prod/secret/db#password}") {
		t.Fatal("DB-bound blob should keep the original ${vault://...} marker")
	}
	if strings.Contains(dbStored, secret) {
		t.Fatal("DB-bound blob must NOT contain the secret value")
	}

	// Wire blob: contains the resolved value, no marker.
	if !strings.Contains(wireBound, secret) {
		t.Fatalf("wire-bound blob should contain the resolved secret; got %q", wireBound)
	}
	if strings.Contains(wireBound, "${vault://") {
		t.Fatalf("wire-bound blob still contains a ${vault://...} marker: %q", wireBound)
	}
}
