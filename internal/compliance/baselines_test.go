package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ── fake Querier ──────────────────────────────────────────────────────

// fakeBaselineDB is an in-memory implementation of compliance.Querier
// used by the apply / revert tests. Operations are protected by a
// mutex so the test can race-detect concurrent reads from buildSnapshot.
type fakeBaselineDB struct {
	mu           sync.Mutex
	baselines    map[uuid.UUID]sqlc.ComplianceBaseline
	bySlug       map[string]uuid.UUID
	applications []sqlc.ComplianceBaselineApplication
	settings     map[string]sqlc.PlatformSetting
	quotaPlans   map[string]sqlc.QuotaPlan
}

func newFakeDB() *fakeBaselineDB {
	return &fakeBaselineDB{
		baselines:  map[uuid.UUID]sqlc.ComplianceBaseline{},
		bySlug:     map[string]uuid.UUID{},
		settings:   map[string]sqlc.PlatformSetting{},
		quotaPlans: map[string]sqlc.QuotaPlan{},
	}
}

func (f *fakeBaselineDB) seedBaseline(slug, name string) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.baselines[id] = sqlc.ComplianceBaseline{
		ID: id, Slug: slug, Name: name, Description: name, Version: "1.0",
		Spec: json.RawMessage("{}"), Enabled: true,
	}
	f.bySlug[slug] = id
	return id
}

func (f *fakeBaselineDB) GetComplianceBaseline(_ context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.baselines[id]
	if !ok {
		return sqlc.ComplianceBaseline{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeBaselineDB) ListComplianceBaselines(_ context.Context) ([]sqlc.ComplianceBaseline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ComplianceBaseline, 0, len(f.baselines))
	for _, r := range f.baselines {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeBaselineDB) CreateComplianceBaselineApplication(_ context.Context, arg sqlc.CreateComplianceBaselineApplicationParams) (sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app := sqlc.ComplianceBaselineApplication{
		ID:            uuid.New(),
		BaselineID:    arg.BaselineID,
		PreviousState: arg.PreviousState,
		AppliedBy:     arg.AppliedBy,
		AppliedAt:     time.Now().UTC(),
		Status:        "applied",
		Notes:         arg.Notes,
	}
	f.applications = append(f.applications, app)
	return app, nil
}

func (f *fakeBaselineDB) GetComplianceBaselineApplication(_ context.Context, id uuid.UUID) (sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.applications {
		if a.ID == id {
			return a, nil
		}
	}
	return sqlc.ComplianceBaselineApplication{}, pgx.ErrNoRows
}

func (f *fakeBaselineDB) GetActiveComplianceBaselineApplication(_ context.Context) (sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.applications) - 1; i >= 0; i-- {
		if f.applications[i].Status == "applied" {
			return f.applications[i], nil
		}
	}
	return sqlc.ComplianceBaselineApplication{}, pgx.ErrNoRows
}

func (f *fakeBaselineDB) ListComplianceBaselineApplications(_ context.Context, limit int32) ([]sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// reverse-time order
	out := make([]sqlc.ComplianceBaselineApplication, 0, len(f.applications))
	for i := len(f.applications) - 1; i >= 0; i-- {
		out = append(out, f.applications[i])
		if int32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeBaselineDB) MarkComplianceBaselineApplicationReverted(_ context.Context, arg sqlc.MarkComplianceBaselineApplicationRevertedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.applications {
		if f.applications[i].ID == arg.ID && f.applications[i].Status == "applied" {
			f.applications[i].Status = "reverted"
			f.applications[i].RevertedBy = arg.RevertedBy
			f.applications[i].RevertedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
			return nil
		}
	}
	return nil
}

func (f *fakeBaselineDB) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.settings[key]
	if !ok {
		return sqlc.PlatformSetting{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeBaselineDB) UpsertPlatformSetting(_ context.Context, arg sqlc.UpsertPlatformSettingParams) (sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.PlatformSetting{
		Key: arg.Key, Value: arg.Value, Description: arg.Description,
		UpdatedBy: arg.UpdatedBy, UpdatedAt: time.Now().UTC(),
	}
	f.settings[arg.Key] = row
	return row, nil
}

func (f *fakeBaselineDB) GetQuotaPlan(_ context.Context, name string) (sqlc.QuotaPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.quotaPlans[name]
	if !ok {
		return sqlc.QuotaPlan{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeBaselineDB) UpsertQuotaPlan(_ context.Context, arg sqlc.UpsertQuotaPlanParams) (sqlc.QuotaPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plan := sqlc.QuotaPlan{
		Name: arg.Name, Enforcement: arg.Enforcement, Description: arg.Description,
		MaxClustersPerProject:   arg.MaxClustersPerProject,
		MaxNamespacesPerProject: arg.MaxNamespacesPerProject,
		MaxMembersPerProject:    arg.MaxMembersPerProject,
		MaxProjectsPerUser:      arg.MaxProjectsPerUser,
		MaxTokensPerUser:        arg.MaxTokensPerUser,
		MaxStreamsPerUser:       arg.MaxStreamsPerUser,
		MaxTotalClusters:        arg.MaxTotalClusters,
		MaxTotalUsers:           arg.MaxTotalUsers,
	}
	f.quotaPlans[arg.Name] = plan
	return plan, nil
}

// ── tests ─────────────────────────────────────────────────────────────

func TestRegistry_HasFourBuiltins(t *testing.T) {
	reg := Registry()
	if len(reg) != 4 {
		t.Fatalf("Registry size = %d, want 4", len(reg))
	}
	slugs := map[string]bool{}
	for _, b := range reg {
		slugs[b.Slug] = true
	}
	for _, want := range []string{"pci_dss_4_0", "hipaa", "fedramp_moderate", "soc2"} {
		if !slugs[want] {
			t.Errorf("Registry missing slug %q", want)
		}
	}
}

func TestRegistry_PCISpecLooksRight(t *testing.T) {
	b, ok := BySlug("pci_dss_4_0")
	if !ok {
		t.Fatal("PCI baseline not in registry")
	}
	if b.Spec.AuditRetentionDays != 365 {
		t.Errorf("PCI audit_retention_days = %d, want 365", b.Spec.AuditRetentionDays)
	}
	if b.Spec.PSSProfile != "restricted" {
		t.Errorf("PCI pss_profile = %q, want %q", b.Spec.PSSProfile, "restricted")
	}
	if !b.Spec.RequiredTOTP {
		t.Error("PCI required_totp should be true")
	}
	if !b.Spec.RequiredSMTP {
		t.Error("PCI required_smtp should be true")
	}
	if len(b.Spec.QuotaPlans) == 0 {
		t.Error("PCI should ship at least one quota plan")
	}
	if len(b.Spec.AlertRules) == 0 {
		t.Error("PCI should ship alert rules")
	}
}

func TestRegistry_HIPAARetention(t *testing.T) {
	b, _ := BySlug("hipaa")
	if b.Spec.AuditRetentionDays != 2190 {
		t.Errorf("HIPAA audit_retention_days = %d, want 2190 (6 yr)", b.Spec.AuditRetentionDays)
	}
}

func TestRegistry_FedRAMPRetention(t *testing.T) {
	b, _ := BySlug("fedramp_moderate")
	if b.Spec.AuditRetentionDays != 1095 {
		t.Errorf("FedRAMP audit_retention_days = %d, want 1095", b.Spec.AuditRetentionDays)
	}
}

func TestRegistry_SOC2Defaults(t *testing.T) {
	b, _ := BySlug("soc2")
	if b.Spec.PSSProfile != "baseline" {
		t.Errorf("SOC2 pss_profile = %q, want %q", b.Spec.PSSProfile, "baseline")
	}
	if b.Spec.MaintenanceWindowTpl == nil {
		t.Error("SOC2 should ship a maintenance window template")
	}
}

func TestApply_SnapshotsPreviousState(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")

	// Seed an existing audit.retention_days so the snapshot has
	// something to capture.
	v, _ := json.Marshal(120)
	db.settings["audit.retention_days"] = sqlc.PlatformSetting{Key: "audit.retention_days", Value: v}

	appID, err := Apply(context.Background(), db, id, uuid.New(), "", nil)
	if err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	if appID == uuid.Nil {
		t.Fatal("Apply returned uuid.Nil")
	}

	app, _ := db.GetComplianceBaselineApplication(context.Background(), appID)
	var snap BaselineSpec
	if err := json.Unmarshal(app.PreviousState, &snap); err != nil {
		t.Fatalf("decode previous_state: %v", err)
	}
	if snap.AuditRetentionDays != 120 {
		t.Errorf("snapshot audit_retention_days = %d, want 120", snap.AuditRetentionDays)
	}
}

func TestApply_WritesAuditRetention(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")

	if _, err := Apply(context.Background(), db, id, uuid.New(), "", nil); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	row, err := db.GetPlatformSetting(context.Background(), "audit.retention_days")
	if err != nil {
		t.Fatalf("audit.retention_days not written: %v", err)
	}
	var v int
	if err := json.Unmarshal(row.Value, &v); err != nil {
		t.Fatalf("unmarshal audit_retention_days: %v", err)
	}
	if v != 365 {
		t.Errorf("written audit_retention_days = %d, want 365", v)
	}
}

func TestApply_WritesQuotaPlans(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	if _, err := Apply(context.Background(), db, id, uuid.New(), "", nil); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	if _, err := db.GetQuotaPlan(context.Background(), "pci-prod"); err != nil {
		t.Errorf("pci-prod quota plan not written: %v", err)
	}
}

func TestApply_DoesNotOverwriteDefaultQuotaPlan(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("custom", "Custom")
	// Inject a built-in registry entry on the fly. We do this by
	// re-using the SOC2 spec but with a default-named plan.
	// Since BySlug only knows the four built-ins, this test asserts
	// that the existing operator 'default' plan is left alone even
	// when a baseline accidentally targets it.
	// We can verify the apply logic directly via writeSpec on a
	// crafted spec.
	db.quotaPlans["default"] = sqlc.QuotaPlan{Name: "default", Enforcement: "soft", MaxClustersPerProject: 999}
	spec := BaselineSpec{
		QuotaPlans: []QuotaPlanSpec{
			{Name: "default", Enforcement: "hard", MaxClustersPerProject: 5},
		},
	}
	if err := writeSpec(context.Background(), db, spec, uuid.New(), nil); err != nil {
		t.Fatalf("writeSpec err = %v", err)
	}
	got := db.quotaPlans["default"]
	if got.MaxClustersPerProject != 999 {
		t.Errorf("default plan was overwritten: max_clusters_per_project=%d, want 999", got.MaxClustersPerProject)
	}
	_ = id
}

func TestApply_WritesMaintenanceWindowTemplate(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("soc2", "SOC 2")
	if _, err := Apply(context.Background(), db, id, uuid.New(), "", nil); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	row, err := db.GetPlatformSetting(context.Background(), "maintenance.template")
	if err != nil {
		t.Fatalf("maintenance.template not written: %v", err)
	}
	var tpl MaintenanceWindowSpec
	if err := json.Unmarshal(row.Value, &tpl); err != nil {
		t.Fatalf("unmarshal maintenance template: %v", err)
	}
	if tpl.Name != "soc2-change-management" {
		t.Errorf("template name = %q, want soc2-change-management", tpl.Name)
	}
}

func TestApply_IdempotentNoOpOnReapply(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("soc2", "SOC 2")
	if _, err := Apply(context.Background(), db, id, uuid.New(), "first", nil); err != nil {
		t.Fatalf("first apply err = %v", err)
	}
	if _, err := Apply(context.Background(), db, id, uuid.New(), "second", nil); err != nil {
		t.Fatalf("second apply err = %v", err)
	}
	// Two application rows. The second snapshot should reflect the
	// state AFTER the first apply — i.e. all the fields the first
	// apply wrote should now appear in the second's previous_state
	// (no longer empty).
	if len(db.applications) != 2 {
		t.Fatalf("applications = %d, want 2", len(db.applications))
	}
	var snap BaselineSpec
	if err := json.Unmarshal(db.applications[1].PreviousState, &snap); err != nil {
		t.Fatalf("unmarshal previous state: %v", err)
	}
	if snap.PSSProfile != "baseline" {
		t.Errorf("second-apply snapshot pss_profile = %q, want baseline (post-first-apply state)", snap.PSSProfile)
	}
}

func TestApply_RefusesAuditRetentionDowngrade(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	// Current retention is 9999 days — applying PCI (365) would be
	// a downgrade.
	v, _ := json.Marshal(9999)
	db.settings["audit.retention_days"] = sqlc.PlatformSetting{Key: "audit.retention_days", Value: v}

	_, err := Apply(context.Background(), db, id, uuid.New(), "", nil)
	if !errors.Is(err, ErrAuditRetentionDowngrade) {
		t.Fatalf("Apply err = %v, want ErrAuditRetentionDowngrade", err)
	}
}

func TestRevert_RestoresPreviousState(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")

	// Seed initial state: audit retention = 60, no PSS profile.
	v, _ := json.Marshal(60)
	db.settings["audit.retention_days"] = sqlc.PlatformSetting{Key: "audit.retention_days", Value: v}

	appID, err := Apply(context.Background(), db, id, uuid.New(), "", nil)
	if err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	// Sanity: PCI bumped retention to 365.
	row, _ := db.GetPlatformSetting(context.Background(), "audit.retention_days")
	var cur int
	if err := json.Unmarshal(row.Value, &cur); err != nil {
		t.Fatalf("unmarshal retention after apply: %v", err)
	}
	if cur != 365 {
		t.Fatalf("post-apply retention = %d, want 365", cur)
	}

	// Revert.
	if err := Revert(context.Background(), db, appID, uuid.New(), nil); err != nil {
		t.Fatalf("Revert err = %v", err)
	}
	row, _ = db.GetPlatformSetting(context.Background(), "audit.retention_days")
	if err := json.Unmarshal(row.Value, &cur); err != nil {
		t.Fatalf("unmarshal retention after revert: %v", err)
	}
	if cur != 60 {
		t.Errorf("post-revert retention = %d, want 60", cur)
	}
}

func TestRevert_RefusesIfNewerApplicationExists(t *testing.T) {
	db := newFakeDB()
	pciID := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	soc2ID := db.seedBaseline("soc2", "SOC 2")

	app1ID, err := Apply(context.Background(), db, pciID, uuid.New(), "", nil)
	if err != nil {
		t.Fatalf("apply1 err = %v", err)
	}
	if _, err := Apply(context.Background(), db, soc2ID, uuid.New(), "", nil); err != nil {
		t.Fatalf("apply2 err = %v", err)
	}

	// Reverting the FIRST application should refuse — the second is
	// the active one and v1 policy is "revert latest only".
	err = Revert(context.Background(), db, app1ID, uuid.New(), nil)
	if !errors.Is(err, ErrNewerApplicationExists) {
		t.Errorf("Revert err = %v, want ErrNewerApplicationExists", err)
	}
}

func TestDiff_ReturnsExpectedFields(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	res, err := Diff(context.Background(), db, id)
	if err != nil {
		t.Fatalf("Diff err = %v", err)
	}
	if res.BaselineSlug != "pci_dss_4_0" {
		t.Errorf("Diff slug = %q, want pci_dss_4_0", res.BaselineSlug)
	}
	if res.Target["audit_retention_days"] != 365 {
		t.Errorf("Diff target audit_retention_days = %v, want 365", res.Target["audit_retention_days"])
	}
	if len(res.Changes) == 0 {
		t.Error("Diff should report at least one change on a fresh DB")
	}
	// Changes should include audit_retention_days since it's empty.
	found := false
	for _, c := range res.Changes {
		if c == "audit_retention_days" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Diff changes %v should include audit_retention_days", res.Changes)
	}
}

func TestApply_DisabledBaselineRefused(t *testing.T) {
	db := newFakeDB()
	id := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	// Flip enabled off.
	row := db.baselines[id]
	row.Enabled = false
	db.baselines[id] = row

	_, err := Apply(context.Background(), db, id, uuid.New(), "", nil)
	if !errors.Is(err, ErrBaselineDisabled) {
		t.Errorf("err = %v, want ErrBaselineDisabled", err)
	}
}
