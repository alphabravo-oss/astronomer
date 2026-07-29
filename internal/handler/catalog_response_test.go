package handler

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fixtureHelmRepository returns a helm_repositories row populated across every
// column type the table carries. withOptionals drives the pgtype fields, which
// is the whole point of the fixture: helmRepositoryToResponse unpacks
// pgtype.UUID and pgtype.Timestamptz by hand, and only the Valid=true branch
// can prove the unpack produces the same bytes pgx's own MarshalJSON did.
func fixtureHelmRepository(withOptionals bool) sqlc.HelmRepository {
	repo := sqlc.HelmRepository{
		ID:          uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Name:        "bitnami",
		Url:         "https://charts.bitnami.com/bitnami",
		RepoType:    "helm",
		Description: "Bitnami charts",
		IsDefault:   true,
		AuthType:    "basic",
		AuthConfig:  json.RawMessage(`{"username":"u"}`),
		Enabled:     true,
		// Set on the row but deliberately NOT on the wire — see the DTO's
		// closing note. Populating it here is what makes its absence from the
		// response an assertion rather than an accident.
		AuthConfigEncrypted: "gAAAAABm-not-a-real-token",
		LastSyncError:       "repository returned status 401",
		CreatedAt:           time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 2, 20, 12, 45, 30, 123456789, time.UTC),
	}
	if withOptionals {
		repo.LastSyncedAt = pgtype.Timestamptz{
			Time:  time.Date(2026, 5, 10, 8, 15, 0, 987654321, time.UTC),
			Valid: true,
		}
		repo.LastSyncAttemptedAt = pgtype.Timestamptz{
			Time:  time.Date(2026, 5, 11, 9, 0, 0, 0, time.UTC),
			Valid: true,
		}
		repo.CreatedByID = pgtype.UUID{
			Bytes: uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			Valid: true,
		}
		repo.OwnerProjectID = pgtype.UUID{
			Bytes: uuid.MustParse("55555555-5555-5555-5555-555555555555"),
			Valid: true,
		}
	}
	return repo
}

// TestHelmRepositoryResponse_WireCompat is the assertion the DTO's doc comment
// promises: helmRepositoryToResponse emits byte-for-byte what marshalling the
// sqlc.HelmRepository row directly used to emit, on every key the old shape
// carried, with exactly two intended differences —
//
//   - auth_config_encrypted is REMOVED (the migration-145 Fernet envelope,
//     which no client has any use for), and
//   - chart_count is ADDED (the field the frontend had always believed in and
//     no column, query or spec entry ever produced).
//
// Without this the hand-rolled pgtype unpacking is untested on its valid
// branch: deleting the CreatedByID/OwnerProjectID blocks outright — so both
// fields go permanently null on the wire — passes the rest of the suite,
// because the only other test that mentions those keys just checks they are
// present and leaves both pgtype.UUIDs invalid in its fixture.
func TestHelmRepositoryResponse_WireCompat(t *testing.T) {
	const chartCount int64 = 42

	for _, tc := range []struct {
		name          string
		withOptionals bool
	}{
		{"all fields populated", true},
		{"optionals invalid (null pgtype)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := fixtureHelmRepository(tc.withOptionals)

			// The legacy shape: the raw row, which is what these endpoints
			// marshalled before the DTO existed.
			legacyJSON, err := json.Marshal(repo)
			if err != nil {
				t.Fatalf("marshal legacy row: %v", err)
			}
			var legacy map[string]any
			if err := json.Unmarshal(legacyJSON, &legacy); err != nil {
				t.Fatalf("unmarshal legacy row: %v", err)
			}

			dtoJSON, err := json.Marshal(helmRepositoryToResponse(repo, chartCount))
			if err != nil {
				t.Fatalf("marshal dto: %v", err)
			}
			var dto map[string]any
			if err := json.Unmarshal(dtoJSON, &dto); err != nil {
				t.Fatalf("unmarshal dto: %v", err)
			}

			// The two intended departures, asserted before they are removed
			// so the comparison below can be total.
			if _, ok := legacy["auth_config_encrypted"]; !ok {
				t.Fatal("fixture no longer exercises auth_config_encrypted")
			}
			if _, ok := dto["auth_config_encrypted"]; ok {
				t.Fatalf("the Fernet envelope is on the wire: %s", dtoJSON)
			}
			if got := dto["chart_count"]; got != float64(chartCount) {
				t.Fatalf("chart_count = %v, want %d", got, chartCount)
			}
			delete(legacy, "auth_config_encrypted")
			delete(dto, "chart_count")

			// Same key set, same values, byte for byte after canonical
			// re-marshalling (Go sorts map keys, so this compares content
			// rather than field order).
			wantJSON, err := json.Marshal(legacy)
			if err != nil {
				t.Fatalf("remarshal legacy: %v", err)
			}
			gotJSON, err := json.Marshal(dto)
			if err != nil {
				t.Fatalf("remarshal dto: %v", err)
			}
			if !bytes.Equal(wantJSON, gotJSON) {
				t.Fatalf("wire mismatch:\n legacy: %s\n dto:    %s", wantJSON, gotJSON)
			}
		})
	}
}

// TestHelmRepositoryResponse_NilAuthConfigRendersEmptyObject pins the one place
// the DTO deliberately does NOT reproduce the row: a nil auth_config marshals
// as `null` off the raw struct, which the catalog UI would have to special-case.
func TestHelmRepositoryResponse_NilAuthConfigRendersEmptyObject(t *testing.T) {
	repo := fixtureHelmRepository(false)
	repo.AuthConfig = nil

	got := helmRepositoryToResponse(repo, 0)
	if string(got.AuthConfig) != `{}` {
		t.Fatalf("auth_config = %s, want {}", got.AuthConfig)
	}
}
