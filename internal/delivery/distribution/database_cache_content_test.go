package distribution

import (
	"bytes"
	"testing"
	"time"
)

// These fixtures exercise the archive/content boundary only. Their internally
// constructed feed is explicitly unit DATA, not a signature/acceptance grant.
func TestDatabaseCacheContentDerivedOnlyFromWholeClosedPayload(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		t.Run(role, func(t *testing.T) {
			update, snapshot, payload := databasePayloadUnitFixture(t, now, role)
			verified, err := InspectDatabaseUpdatePayload(t.Context(), update, role, databasePayloadOwnedFile(t, payload), CadenceDatabaseUpdateTiming(), now)
			if err != nil {
				t.Fatal(err)
			}
			content, valid := verified.CacheContent()
			expectedName := "trivy.db"
			if role == "trivy-java-database" {
				expectedName = "trivy-java.db"
			}
			original := []byte("opaque QA database, not a supplier DB")
			if !valid || content.Role != role || content.File != expectedName || content.Size != int64(len(original)) || content.Digest != contentDigest(original) {
				t.Fatal("expanded original database bytes were not measured under whole layer authentication")
			}
			feed, actual, valid := verified.Snapshot()
			if !valid || feed != update.digest || actual != snapshot {
				t.Fatal("expanded measurement changed old snapshot contract")
			}
			content.File = "caller-path"
			content.Digest = contentDigest([]byte("caller"))
			repeat, valid := verified.CacheContent()
			if !valid || repeat.File != expectedName || repeat.Digest != contentDigest(original) {
				t.Fatal("returned DATA changed retained measured content")
			}
			changed := bytes.Clone(payload)
			changed[len(changed)-1] ^= 1
			if proof, err := InspectDatabaseUpdatePayload(t.Context(), update, role, databasePayloadOwnedFile(t, changed), CadenceDatabaseUpdateTiming(), now); err == nil || proof != nil {
				t.Fatal("changed compressed layer produced cache content")
			}
		})
	}
}

func TestDatabaseCacheContentCannotBeInferredFromDescriptorOnlyProof(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	update, snapshot, _ := databasePayloadUnitFixture(t, now, "trivy-database")
	for _, proof := range []*VerifiedDatabaseUpdatePayload{nil, {}, {feedDigest: update.digest, snapshot: snapshot}} {
		if _, valid := proof.CacheContent(); valid {
			t.Fatal("absence of complete file measurement was treated as measured cache content")
		}
	}
}
