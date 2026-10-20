package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// The optional method assertion compiles against unchanged production so the
// missing snapshot composition is a real behavior failure, not a build error.
func TestComplianceCollectionSectionsUseSnapshot(t *testing.T) {
	outside := newFakeComplianceQuerier(uuid.New(), true)
	h := NewComplianceHandler(outside, nil)
	setter, ok := any(h).(interface {
		SetReadSnapshot(func(context.Context, func(ComplianceQuerier) error) error)
	})
	if !ok {
		t.Fatal("complete compliance sections have no owned read snapshot")
	}
	expected := errors.New("owned snapshot refused")
	calls := 0
	setter.SetReadSnapshot(func(ctx context.Context, fn func(ComplianceQuerier) error) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Fatal("snapshot lacks bounded collection deadline")
		}
		return expected
	})
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	log := newSectionLog()
	h.writeAllSections(context.Background(), zw, log, time.Unix(0, 0), time.Now())
	h.writeBundleReadme(zw, log, time.Unix(0, 0), time.Now())
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("complete collection sections used %d snapshots, want 2", calls)
	}
	zr, err := zip.NewReader(bytes.NewReader(body.Bytes()), int64(body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "README.md" {
			reader, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			var manifest bytes.Buffer
			_, err = manifest.ReadFrom(reader)
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("manifest read: %v %v", err, closeErr)
			}
			if bytes.Count(manifest.Bytes(), []byte(expected.Error())) != 2 {
				t.Fatalf("snapshot refusal was not retained for both sections: %s", manifest.Bytes())
			}
		}
	}
}

func TestCompliancePolicyProjectLookaheadRefusesDocument(t *testing.T) {
	q := newFakeComplianceQuerier(uuid.New(), true)
	q.projects = make([]sqlc.ListAllProjectsForComplianceRow, 100001)
	var body bytes.Buffer
	if err := WritePolicySnapshotJSON(context.Background(), &body, q, q); err == nil || body.Len() != 0 {
		t.Fatal("policy project lookahead silently emitted an oversized document")
	}
}

func TestCompliancePolicyRepeatedProjectRefusesDocument(t *testing.T) {
	q := newFakeComplianceQuerier(uuid.New(), true)
	id := uuid.New()
	q.projects = []sqlc.ListAllProjectsForComplianceRow{{ID: id}, {ID: id}}
	var body bytes.Buffer
	if err := WritePolicySnapshotJSON(context.Background(), &body, q, q); err == nil || body.Len() != 0 {
		t.Fatal("policy snapshot silently emitted a repeated project")
	}
}

type complianceSnapshotLookupQuerier struct {
	*fakeComplianceQuerier
	cancel context.CancelFunc
	calls  int
}

func (q *complianceSnapshotLookupQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	q.calls++
	if q.cancel != nil {
		q.cancel()
	}
	return sqlc.User{}, errors.New("owned user lookup unavailable")
}

func TestCompliancePolicyFinalLookupCancellationAndFallback(t *testing.T) {
	for _, name := range []string{"canceled-final-lookup", "genuine-lookup-error-fallback"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			userID, projectID := uuid.New(), uuid.New()
			base := newFakeComplianceQuerier(userID, true)
			base.projects = []sqlc.ListAllProjectsForComplianceRow{{ID: projectID, Name: "owned-final-lookup", ClusterID: uuid.New()}}
			base.projectBindings = map[uuid.UUID][]sqlc.ProjectRoleBinding{
				projectID: {{ID: uuid.New(), ProjectID: projectID, RoleID: uuid.New(), UserID: validUUID(userID)}},
			}
			lookup := &complianceSnapshotLookupQuerier{fakeComplianceQuerier: base}
			if name == "canceled-final-lookup" {
				lookup.cancel = cancel
			}
			var body bytes.Buffer
			err := WritePolicySnapshotJSON(ctx, &body, base, lookup)
			if lookup.calls != 1 {
				t.Fatal("last member lookup did not reach owned fixture")
			}
			if name == "canceled-final-lookup" {
				if !errors.Is(err, context.Canceled) || body.Len() != 0 {
					t.Fatal("best-effort lookup swallowed cancellation and encoded successful policy JSON")
				}
				return
			}
			var entries []PolicySnapshotEntry
			if err != nil || json.Unmarshal(body.Bytes(), &entries) != nil || len(entries) != 1 || len(entries[0].Members) != 1 || entries[0].Members[0] != userID.String() {
				t.Fatal("genuine lookup error lost the existing UUID fallback")
			}
		})
	}
}
