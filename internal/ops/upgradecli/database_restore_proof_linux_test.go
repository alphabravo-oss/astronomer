//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTemporaryRestoreEncryptedProofUsesOnlyExactIDAndOwnedRestoredDatabase(t *testing.T) {
	fixture := temporaryFixture(t)
	v := fixture.verifier
	v.backup.source.options.DecryptProofWebhookID = "00000000-0000-4000-8000-000000000001"
	original := v.backup.source.fence.execute
	calls := 0
	v.backup.source.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, "\n"), temporaryEncryptedProofScript) {
			calls++
			if args[len(args)-2] != v.backup.source.options.RestoreDatabase || args[len(args)-1] != v.backup.source.options.DecryptProofWebhookID {
				t.Fatal("proof ID/destination binding differs")
			}
			return []byte("1\n"), nil
		}
		return original(ctx, tool, args)
	}
	if err := v.Verify(context.Background()); err != nil || !v.restored || calls != 1 {
		t.Fatal("owned restored-row proof missing", err)
	}
}
func TestTemporaryRestoreMissingOrCanceledEncryptedProofRetainsUnacknowledgedRecovery(t *testing.T) {
	for _, mode := range []string{"missing", "ambiguous", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			fixture := temporaryFixture(t)
			v := fixture.verifier
			v.backup.source.options.DecryptProofWebhookID = "00000000-0000-4000-8000-000000000001"
			original := v.backup.source.fence.execute
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			v.backup.source.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				if strings.Contains(strings.Join(args, "\n"), temporaryEncryptedProofScript) {
					if mode == "cancel" {
						cancel()
						return []byte("1\n"), nil
					}
					if mode == "ambiguous" {
						return []byte("1\n1\n"), nil
					}
					return []byte("0\n"), nil
				}
				return original(ctx, tool, args)
			}
			err := v.Verify(ctx)
			if err == nil || v.restored || !fixture.exists || fixture.drops != 0 || !v.backup.artifact.Acknowledged {
				t.Fatal("failed proof acknowledged restore or discarded recovery")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("proof cancellation lost")
			}
		})
	}
}
