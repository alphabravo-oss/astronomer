package compliance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// setBool writes a JSON bool platform_setting through the fake so the
// pre-apply state can be seeded / asserted.
func setBool(t *testing.T, db *fakeBaselineDB, key string, v bool) {
	t.Helper()
	val, _ := json.Marshal(v)
	if _, err := db.UpsertPlatformSetting(context.Background(), sqlc.UpsertPlatformSettingParams{Key: key, Value: val}); err != nil {
		t.Fatalf("seed %s: %v", key, err)
	}
}

func getBool(t *testing.T, db *fakeBaselineDB, key string) bool {
	t.Helper()
	row, err := db.GetPlatformSetting(context.Background(), key)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	var v bool
	if err := json.Unmarshal(row.Value, &v); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return v
}

// TestRevert_TurnsOffFlagsTheBaselineTurnedOn is the regression for the
// set-only-when-truthy revert bug: a hardening baseline that flips
// totp.required / smtp.required from false→true must, on Revert, put
// them back to false. The old writeSpec-based revert skipped false
// scalars, leaving the security settings pinned ON after a "successful"
// revert.
func TestRevert_TurnsOffFlagsTheBaselineTurnedOn(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")

	// Pre-apply: operator had TOTP + SMTP requirements OFF.
	setBool(t, db, "totp.required", false)
	setBool(t, db, "smtp.required", false)

	appID, err := Apply(ctx, db, id, uuid.New(), "harden", nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// PCI turns both requirements ON.
	if !getBool(t, db, "totp.required") {
		t.Fatal("apply should have set totp.required=true")
	}
	if !getBool(t, db, "smtp.required") {
		t.Fatal("apply should have set smtp.required=true")
	}

	if err := Revert(ctx, db, appID, uuid.New(), nil); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	if getBool(t, db, "totp.required") {
		t.Error("revert must restore totp.required=false, still true")
	}
	if getBool(t, db, "smtp.required") {
		t.Error("revert must restore smtp.required=false, still true")
	}
}

// TestRevert_RestoresPreExistingTrueFlag guards the non-buggy direction:
// when the flag was ALREADY true pre-apply, revert must leave it true.
func TestRevert_RestoresPreExistingTrueFlag(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")

	setBool(t, db, "totp.required", true)
	setBool(t, db, "smtp.required", false)

	appID, err := Apply(ctx, db, id, uuid.New(), "harden", nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := Revert(ctx, db, appID, uuid.New(), nil); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	if !getBool(t, db, "totp.required") {
		t.Error("revert must keep totp.required=true (was true pre-apply)")
	}
	if getBool(t, db, "smtp.required") {
		t.Error("revert must restore smtp.required=false")
	}
}
