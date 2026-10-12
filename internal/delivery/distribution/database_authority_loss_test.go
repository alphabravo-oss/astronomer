//go:build unix

package distribution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObservedDatabaseTrustLossRequiresActualLiveCapturedMaterial(t *testing.T) {
	for _, material := range []string{"vendor", "operator", "registry"} {
		t.Run(material, func(t *testing.T) {
			options, registry, fixture, _ := pinnedDatabaseAuthorityFixture(t)
			authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
			if err != nil {
				t.Fatal(err)
			}
			defer authority.Close()
			proof, err := authority.Base(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if observed, err := authority.ObserveTrustLoss(t.Context(), proof); err == nil || observed != nil {
				t.Fatal("current physical material became a loss grant")
			}
			path := filepath.Join(options.TrustDirectory, "qa.pub")
			if material == "operator" {
				path = filepath.Join(options.TrustDirectory, "operator.pub")
			}
			if material == "registry" {
				path = filepath.Join(registry.TrustDirectory, "registry.pem")
			}
			original, err := os.ReadFile(path) // Public key/CA material only.
			if err != nil {
				t.Fatal(err)
			}
			before := fixture.readCount()
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			loss, err := authority.ObserveTrustLoss(t.Context(), proof)
			if err != nil || loss == nil {
				t.Fatal("actual missing captured material was not observed")
			}
			if err := authority.RevalidateTrustLoss(t.Context(), loss); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if observed, err := authority.ObserveTrustLoss(canceled, proof); !errors.Is(err, context.Canceled) || observed != nil {
				t.Fatal("cancellation became loss authority")
			}
			if err := authority.RevalidateTrustLoss(canceled, loss); !errors.Is(err, context.Canceled) {
				t.Fatal("loss revalidation lost canonical cancellation")
			}
			expired := *loss
			expired.expires = time.Now().UTC().Add(-time.Second)
			if authority.RevalidateTrustLoss(t.Context(), &expired) == nil {
				t.Fatal("expired observation remained authority")
			}
			if fixture.readCount() != before {
				t.Fatal("physical loss probe requested registry content")
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if authority.RevalidateTrustLoss(t.Context(), loss) == nil {
				t.Fatal("restored material still authorized loss revocation")
			}
			authority.Close()
			if observed, err := authority.ObserveTrustLoss(t.Context(), proof); err == nil || observed != nil {
				t.Fatal("closed owner became loss authority")
			}
			if authority.RevalidateTrustLoss(t.Context(), loss) == nil {
				t.Fatal("closed owner retained loss authority")
			}
		})
	}
}

func TestObservedDatabaseTrustLossRejectsForeignAndZeroProof(t *testing.T) {
	options, registry, _, _ := pinnedDatabaseAuthorityFixture(t)
	first, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	proof, err := first.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, candidate := range []*VerifiedDatabaseAuthorityBase{nil, {}, proof} {
		if observed, err := second.ObserveTrustLoss(t.Context(), candidate); err == nil || observed != nil {
			t.Fatal("foreign/zero base became loss authority")
		}
	}
	if first.RevalidateTrustLoss(t.Context(), &ObservedDatabaseTrustLoss{}) == nil {
		t.Fatal("zero loss accepted")
	}
	if observed, err := first.ObserveTrustLoss(nil, proof); err == nil || observed != nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context accepted")
	}
	if first.RevalidateTrustLoss(nil, nil) == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil loss/context accepted")
	}
	if err := os.Remove(filepath.Join(options.TrustDirectory, "qa.pub")); err != nil {
		t.Fatal(err)
	}
	loss, err := first.ObserveTrustLoss(t.Context(), proof)
	if err != nil {
		t.Fatal(err)
	}
	if second.RevalidateTrustLoss(t.Context(), loss) == nil {
		t.Fatal("another constructor used loss observation")
	}
}
