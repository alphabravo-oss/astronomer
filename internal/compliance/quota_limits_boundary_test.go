package compliance

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type quotaBoundaryDB struct {
	*fakeBaselineDB
	settingsWrites, quotaWrites, revertedWrites int
}

func (q *quotaBoundaryDB) UpsertPlatformSetting(ctx context.Context, params sqlc.UpsertPlatformSettingParams) (sqlc.PlatformSetting, error) {
	q.settingsWrites++
	return q.fakeBaselineDB.UpsertPlatformSetting(ctx, params)
}
func (q *quotaBoundaryDB) UpsertQuotaPlan(ctx context.Context, params sqlc.UpsertQuotaPlanParams) (sqlc.QuotaPlan, error) {
	q.quotaWrites++
	return q.fakeBaselineDB.UpsertQuotaPlan(ctx, params)
}
func (q *quotaBoundaryDB) MarkComplianceBaselineApplicationReverted(ctx context.Context, params sqlc.MarkComplianceBaselineApplicationRevertedParams) error {
	q.revertedWrites++
	return q.fakeBaselineDB.MarkComplianceBaselineApplicationReverted(ctx, params)
}

var quotaBoundaryFields = []string{
	"MaxClustersPerProject", "MaxNamespacesPerProject", "MaxMembersPerProject",
	"MaxProjectsPerUser", "MaxTokensPerUser", "MaxStreamsPerUser",
	"MaxTotalClusters", "MaxTotalUsers",
}

func quotaSnapshotFixture(t *testing.T, field string, value int64) (*quotaBoundaryDB, uuid.UUID) {
	t.Helper()
	db := newFakeDB()
	baselineID := db.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	applicationID, err := Apply(t.Context(), db, baselineID, uuid.New(), "boundary fixture", nil)
	if err != nil {
		t.Fatal("fixture application failed", err)
	}
	plan := QuotaPlanSpec{Name: "pci-prod", Enforcement: "hard"}
	reflect.ValueOf(&plan).Elem().FieldByName(field).SetInt(value)
	snapshot := BaselineSpec{AuditRetentionDays: 1, QuotaPlans: []QuotaPlanSpec{plan}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal("fixture snapshot failed", err)
	}
	db.applications[0].PreviousState = raw
	return &quotaBoundaryDB{fakeBaselineDB: db}, applicationID
}

func TestRevertRejectsUnrepresentableQuotaSnapshotBeforeWrites(t *testing.T) {
	for _, field := range quotaBoundaryFields {
		for _, edge := range []struct {
			name  string
			value int64
		}{{"overflow", int64(math.MaxInt32) + 1}, {"underflow", int64(math.MinInt32) - 1}} {
			t.Run(field+"/"+edge.name, func(t *testing.T) {
				q, id := quotaSnapshotFixture(t, field, edge.value)
				err := Revert(t.Context(), q, id, uuid.New(), nil)
				if err == nil {
					t.Fatal("corrupt quota snapshot accepted instead of rejecting integer overflow")
				}
				if q.settingsWrites != 0 || q.quotaWrites != 0 || q.revertedWrites != 0 {
					t.Fatalf("invalid stored quota reached writer effects: settings=%d quotas=%d reverted=%d", q.settingsWrites, q.quotaWrites, q.revertedWrites)
				}
			})
		}
	}
}

func TestRevertPreservesRepresentableSignedQuotaValues(t *testing.T) {
	for _, field := range quotaBoundaryFields {
		for _, edge := range []struct {
			name  string
			value int64
		}{{"maximum", math.MaxInt32}, {"minimum", math.MinInt32}, {"zero", 0}, {"negative", -1}} {
			t.Run(field+"/"+edge.name, func(t *testing.T) {
				q, id := quotaSnapshotFixture(t, field, edge.value)
				if err := Revert(t.Context(), q, id, uuid.New(), nil); err != nil {
					t.Fatal("representable stored quota rejected", err)
				}
				stored := q.quotaPlans["pci-prod"]
				if got := reflect.ValueOf(stored).FieldByName(field).Int(); got != edge.value {
					t.Fatalf("stored signed quota changed: got=%d expected=%d", got, edge.value)
				}
				if q.quotaWrites != 1 || q.revertedWrites != 1 {
					t.Fatalf("valid snapshot writer effects changed: quotas=%d reverted=%d", q.quotaWrites, q.revertedWrites)
				}
			})
		}
	}
}
