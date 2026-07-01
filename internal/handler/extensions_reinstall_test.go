package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// reinstallExtQuerier faithfully models the real UpsertUIExtension ON CONFLICT
// behavior: bundle_verified is NOT in the SET list, so it is PRESERVED across a
// re-install. (The shared fakeExtensionQuerier zeroes it, which would mask the
// gate-bypass this test guards against.)
type reinstallExtQuerier struct {
	rows map[string]sqlc.UIExtension
}

func (f *reinstallExtQuerier) ListUIExtensions(context.Context) ([]sqlc.UIExtension, error) {
	out := make([]sqlc.UIExtension, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *reinstallExtQuerier) UpsertUIExtension(_ context.Context, arg sqlc.UpsertUIExtensionParams) (sqlc.UIExtension, error) {
	row := sqlc.UIExtension{
		ID:                  uuid.New(),
		Name:                arg.Name,
		DisplayName:         arg.DisplayName,
		Version:             arg.Version,
		Source:              arg.Source,
		Checksum:            arg.Checksum,
		Enabled:             arg.Enabled,
		CompatibilityStatus: arg.CompatibilityStatus,
		Manifest:            arg.Manifest,
		InstalledBy:         arg.InstalledBy,
		InstalledAt:         time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if existing, ok := f.rows[arg.Name]; ok {
		row.ID = existing.ID
		row.InstalledAt = existing.InstalledAt
		// Real ON CONFLICT preserves the prior verification flag.
		row.BundleVerified = existing.BundleVerified
	}
	f.rows[arg.Name] = row
	return row, nil
}

func (f *reinstallExtQuerier) SetUIExtensionEnabled(_ context.Context, arg sqlc.SetUIExtensionEnabledParams) (sqlc.UIExtension, error) {
	row, ok := f.rows[arg.Name]
	if !ok {
		return sqlc.UIExtension{}, pgx.ErrNoRows
	}
	row.Enabled = arg.Enabled
	f.rows[arg.Name] = row
	return row, nil
}

func (f *reinstallExtQuerier) SetUIExtensionBundleVerified(_ context.Context, arg sqlc.SetUIExtensionBundleVerifiedParams) (sqlc.UIExtension, error) {
	row, ok := f.rows[arg.Name]
	if !ok {
		return sqlc.UIExtension{}, pgx.ErrNoRows
	}
	row.BundleVerified = arg.BundleVerified
	f.rows[arg.Name] = row
	return row, nil
}

func (f *reinstallExtQuerier) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	return nil
}

func installExtension(t *testing.T, h *ExtensionHandler, m ExtensionManifest) {
	t.Helper()
	raw, _ := json.Marshal(InstallExtensionRequest{Manifest: m, Source: "unit-test", Enable: true})
	rr := httptest.NewRecorder()
	h.Install(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/", string(raw)))
	if rr.Code != http.StatusOK {
		t.Fatalf("install status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestExtensionHandler_ReinstallResetsBundleVerified proves the signed-bundle
// gate is re-armed on re-install: swapping in a new (unsigned) bundle clears
// bundle_verified so it cannot mount as a verified Tier-2 bundle without being
// re-verified. A byte-identical re-install preserves the flag (legit path).
func TestExtensionHandler_ReinstallResetsBundleVerified(t *testing.T) {
	q := &reinstallExtQuerier{rows: map[string]sqlc.UIExtension{}}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	h := NewExtensionHandler(q)
	h.SetCurrentVersion("0.9.1")
	// A trusted key IS configured, so the enabled fail-closed guard does not
	// fire — this is the exact precondition of the reported bypass.
	if err := h.SetTrustedBundleKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("set key: %v", err)
	}

	m1 := tier2Manifest()
	installExtension(t, h, m1)

	// Trusted-key holder signs + verifies bundle m1 -> bundle_verified=true.
	if _, err := q.SetUIExtensionBundleVerified(context.Background(), sqlc.SetUIExtensionBundleVerifiedParams{Name: m1.Name, BundleVerified: true}); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if got := q.rows[m1.Name]; !got.Enabled || !got.BundleVerified {
		t.Fatalf("precondition: want enabled+verified, got enabled=%v verified=%v", got.Enabled, got.BundleVerified)
	}

	// Re-install the SAME bundle: the gate must stay verified (legit path).
	installExtension(t, h, m1)
	if got := q.rows[m1.Name]; !got.BundleVerified {
		t.Fatalf("identical re-install must preserve bundle_verified")
	}

	// Re-install with a DIFFERENT, unsigned bundle descriptor.
	m2 := tier2Manifest()
	m2.ExtensionPoints.ClusterTabs[0].Render.Bundle.URL = "https://cdn.attacker.example/evil.js"
	m2.ExtensionPoints.ClusterTabs[0].Render.Bundle.SHA256 = "sha256:" + strings.Repeat("b", 64)
	installExtension(t, h, m2)

	if got := q.rows[m2.Name]; got.BundleVerified {
		t.Fatalf("re-install with a changed bundle must reset bundle_verified (signed-bundle gate bypass)")
	}
}
