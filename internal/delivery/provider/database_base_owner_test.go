package provider

import (
	"context"
	"math"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestDatabaseBaseOwnerRequiresPrivatePhysicalConstructor(t *testing.T) {
	if _, err := NewDatabaseBaseOwner(t.Context(), nil, nil); err == nil {
		t.Fatal("missing pool/physical constructor accepted")
	}
	for _, owner := range []*DatabaseBaseOwner{nil, {}} {
		for _, op := range []databaseBaseOperation{databaseBaseInitialize, databaseBasePromote, databaseBaseRevoke} {
			if _, err := owner.transition(context.Background(), op); err == nil {
				t.Fatal("missing private owner accepted")
			}
		}
	}
}

func TestDatabaseBasePromotionMonotonicEpochAndHistoricalRevocation(t *testing.T) {
	current := sqlc.VulnerabilityDatabaseBasis{
		ID: uuid.New(), Epoch: 2, BaseRelease: "v1.2.2", DistributionDigest: "current", BindingFingerprint: "current-binding",
	}
	candidate := distribution.DatabaseAuthoritySnapshot{Base: distribution.DatabaseBaseSnapshot{
		Binding: distribution.DatabaseUpdateBinding{BaseRelease: "1.2.3", BaseDistributionDigest: "new-parent"},
	}}
	if err := validateDatabaseBasePromotion(current, candidate, "new-binding", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		mutate      func(*sqlc.VulnerabilityDatabaseBasis, *distribution.DatabaseAuthoritySnapshot)
		fingerprint string
		history     []sqlc.VulnerabilityDatabaseBasis
	}{
		{name: "zero epoch", mutate: func(c *sqlc.VulnerabilityDatabaseBasis, _ *distribution.DatabaseAuthoritySnapshot) { c.Epoch = 0 }},
		{name: "overflow epoch", mutate: func(c *sqlc.VulnerabilityDatabaseBasis, _ *distribution.DatabaseAuthoritySnapshot) {
			c.Epoch = math.MaxInt64
		}},
		{name: "unchanged binding", fingerprint: "current-binding"},
		{name: "invalid previous version", mutate: func(c *sqlc.VulnerabilityDatabaseBasis, _ *distribution.DatabaseAuthoritySnapshot) {
			c.BaseRelease = "invalid"
		}},
		{name: "invalid candidate version", mutate: func(_ *sqlc.VulnerabilityDatabaseBasis, s *distribution.DatabaseAuthoritySnapshot) {
			s.Base.Binding.BaseRelease = "invalid"
		}},
		{name: "version downgrade", mutate: func(_ *sqlc.VulnerabilityDatabaseBasis, s *distribution.DatabaseAuthoritySnapshot) {
			s.Base.Binding.BaseRelease = "1.2.1"
		}},
		{name: "old parent different trust", history: []sqlc.VulnerabilityDatabaseBasis{{RevokedAt: pgtype.Timestamptz{Valid: true}}}},
		{name: "current revoked parent", history: []sqlc.VulnerabilityDatabaseBasis{current}, mutate: func(c *sqlc.VulnerabilityDatabaseBasis, s *distribution.DatabaseAuthoritySnapshot) {
			c.RevokedAt.Valid = true
			s.Base.Binding.BaseDistributionDigest = c.DistributionDigest
		}},
		{name: "historical trust binding", history: []sqlc.VulnerabilityDatabaseBasis{{BindingFingerprint: "new-binding"}}, mutate: func(c *sqlc.VulnerabilityDatabaseBasis, s *distribution.DatabaseAuthoritySnapshot) {
			s.Base.Binding.BaseDistributionDigest = c.DistributionDigest
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := current, candidate
			if tc.mutate != nil {
				tc.mutate(&c, &s)
			}
			fingerprint := tc.fingerprint
			if fingerprint == "" {
				fingerprint = "new-binding"
			}
			if err := validateDatabaseBasePromotion(c, s, fingerprint, tc.history); err == nil {
				t.Fatal("invalid transition accepted")
			}
		})
	}
	t.Run("repeated trust rotation of active parent", func(t *testing.T) {
		s := candidate
		s.Base.Binding.BaseDistributionDigest = current.DistributionDigest
		history := []sqlc.VulnerabilityDatabaseBasis{{Epoch: 1, BindingFingerprint: "first-binding", RevokedAt: pgtype.Timestamptz{Valid: true}}, current}
		if err := validateDatabaseBasePromotion(current, s, "third-binding", history); err != nil {
			t.Fatal(err)
		}
	})
}
