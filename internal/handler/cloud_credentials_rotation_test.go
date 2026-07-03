package handler

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestCloudCredentialMaterializeDedupeKeyRotates proves a credential value
// change produces a NEW materialize dedupe key, so a rotation is not deduped
// away by the already-'delivered' outbox row / 'applied' materialization row.
// The unchanged case must stay stable so ordinary re-applies still coalesce.
func TestCloudCredentialMaterializeDedupeKeyRotates(t *testing.T) {
	credID := uuid.New()
	ref := TargetRef{ClusterID: uuid.New(), Namespace: "team-a", SecretName: "aws-creds"}

	v1 := cloudCredentialDataVersion(sqlc.CloudCredential{DataEncrypted: "ciphertext-OLD"})
	v2 := cloudCredentialDataVersion(sqlc.CloudCredential{DataEncrypted: "ciphertext-NEW-after-rotation"})
	if v1 == v2 {
		t.Fatalf("different encrypted payloads must yield different data versions")
	}

	keyOld := cloudCredentialMaterializeDedupeKey(credID, ref, "apply", v1)
	keyNew := cloudCredentialMaterializeDedupeKey(credID, ref, "apply", v2)
	if keyOld == keyNew {
		t.Fatalf("rotated credential must change the dedupe key so the task re-fires:\n old=%s\n new=%s", keyOld, keyNew)
	}

	// Same value → same key (idempotent re-apply still coalesces).
	if got := cloudCredentialMaterializeDedupeKey(credID, ref, "apply", v1); got != keyOld {
		t.Fatalf("identical value must produce a stable dedupe key, got %s want %s", got, keyOld)
	}
}
