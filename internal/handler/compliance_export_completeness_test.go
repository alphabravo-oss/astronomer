package handler

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

var complianceCollectionReadFailure = errors.New("owned compliance collection read failed")

func complianceCollectionID(kind string, index int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("owned-compliance-%s-%d", kind, index)))
}

type compliancePolicyPages struct {
	project      uuid.UUID
	rows         []sqlc.ProjectRoleBinding
	failOffset   int32
	calls        []sqlc.ListProjectRoleBindingsByProjectParams
	projectCalls int
}

func (q *compliancePolicyPages) ListAllProjectsForCompliance(context.Context) ([]sqlc.ListAllProjectsForComplianceRow, error) {
	q.projectCalls++
	if q.project == uuid.Nil {
		return nil, nil
	}
	return []sqlc.ListAllProjectsForComplianceRow{{ID: q.project, Name: "owned-project"}}, nil
}

func (q *compliancePolicyPages) ListProjectRoleBindingsByProject(_ context.Context, p sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error) {
	q.calls = append(q.calls, p)
	if p.ProjectID != q.project || p.Limit <= 0 || p.Offset < 0 {
		return nil, errors.New("owned policy page arguments changed")
	}
	if p.Offset == q.failOffset {
		return nil, complianceCollectionReadFailure
	}
	start := int(p.Offset)
	if start >= len(q.rows) {
		return nil, nil
	}
	end := start + int(p.Limit)
	if end > len(q.rows) {
		end = len(q.rows)
	}
	return q.rows[start:end], nil
}

func compliancePolicyFixture(size int) *compliancePolicyPages {
	q := &compliancePolicyPages{project: complianceCollectionID("project", 0), failOffset: -1}
	for i := 0; i < size; i++ {
		q.rows = append(q.rows, sqlc.ProjectRoleBinding{ID: complianceCollectionID("binding", i), ProjectID: q.project, Group: fmt.Sprintf("group-%d", i)})
	}
	return q
}

type compliancePolicyBundle struct {
	*fakeComplianceQuerier
	pages *compliancePolicyPages
}

func (q *compliancePolicyBundle) ListAllProjectsForCompliance(ctx context.Context) ([]sqlc.ListAllProjectsForComplianceRow, error) {
	return q.pages.ListAllProjectsForCompliance(ctx)
}

func (q *compliancePolicyBundle) ListProjectRoleBindingsByProject(ctx context.Context, p sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error) {
	return q.pages.ListProjectRoleBindingsByProject(ctx, p)
}

func TestCompliancePolicySnapshotCollectionCompleteness(t *testing.T) {
	t.Run("201-bindings", func(t *testing.T) {
		q := compliancePolicyFixture(201)
		var body bytes.Buffer
		if err := WritePolicySnapshotJSON(context.Background(), &body, q, nil); err != nil {
			t.Fatal("policy snapshot traversal returned an unexpected section error")
		}
		var entries []PolicySnapshotEntry
		if json.Unmarshal(body.Bytes(), &entries) != nil || len(entries) != 1 || len(entries[0].Members) != 201 || entries[0].Members[200] != "group:group-200" {
			t.Fatal("policy snapshot silently omitted binding 201")
		}
		if len(q.calls) != 2 || q.calls[0].Limit != 200 || q.calls[1].Offset != 200 {
			t.Fatal("policy snapshot did not traverse real project binding pages")
		}
	})
	for _, tc := range []struct {
		name   string
		offset int32
	}{{"binding-read-error", 0}, {"later-page-read-error", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			q := compliancePolicyFixture(201)
			q.failOffset = tc.offset
			var body bytes.Buffer
			err := WritePolicySnapshotJSON(context.Background(), &body, q, nil)
			if !errors.Is(err, complianceCollectionReadFailure) || body.Len() != 0 {
				t.Fatal("policy snapshot concealed an unknown binding collection as successful JSON")
			}
		})
	}
	t.Run("wrong-project-row", func(t *testing.T) {
		q := compliancePolicyFixture(1)
		q.rows[0].ProjectID = complianceCollectionID("other-project", 0)
		var body bytes.Buffer
		if WritePolicySnapshotJSON(context.Background(), &body, q, nil) == nil || body.Len() != 0 {
			t.Fatal("policy snapshot accepted a binding from another project")
		}
	})
	t.Run("cancelled-before-read", func(t *testing.T) {
		q := compliancePolicyFixture(1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var body bytes.Buffer
		if !errors.Is(WritePolicySnapshotJSON(ctx, &body, q, nil), context.Canceled) || q.projectCalls != 0 || len(q.calls) != 0 {
			t.Fatal("cancelled policy snapshot still read project bindings")
		}
	})
	t.Run("bundle-read-error", func(t *testing.T) {
		caller := complianceCollectionID("caller", 0)
		pages := compliancePolicyFixture(201)
		pages.failOffset = 0
		q := &compliancePolicyBundle{fakeComplianceQuerier: newFakeComplianceQuerier(caller, true), pages: pages}
		w := httptest.NewRecorder()
		NewComplianceHandler(q, nil).Export(w, makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", caller))
		if w.Code != http.StatusOK {
			t.Fatal("one failed compliance section changed the bundle HTTP contract")
		}
		files := readZipFiles(t, w.Body.Bytes())
		if !strings.Contains(string(files["README.md"]), "policy-snapshot.json  FAILED:") || !strings.Contains(string(files["README.md"]), "rbac-snapshot.csv  OK") || len(files["rbac-snapshot.csv"]) == 0 {
			t.Fatal("compliance manifest concealed policy collection failure or changed canonical RBAC CSV")
		}
	})
	t.Run("empty-projects", func(t *testing.T) {
		q := &compliancePolicyPages{failOffset: -1}
		var body bytes.Buffer
		if WritePolicySnapshotJSON(context.Background(), &body, q, nil) != nil || strings.TrimSpace(body.String()) != "[]" || len(q.calls) != 0 {
			t.Fatal("empty policy snapshot wire shape changed")
		}
	})
	t.Run("compatible-group-member", func(t *testing.T) {
		q := compliancePolicyFixture(1)
		var body bytes.Buffer
		if WritePolicySnapshotJSON(context.Background(), &body, q, nil) != nil || !strings.Contains(body.String(), "group:group-0") {
			t.Fatal("policy snapshot group label contract changed")
		}
	})
}

type complianceDrillPages struct {
	rows       []sqlc.BackupDrillResult
	count      int64
	countError error
	bounded    bool
	calls      []sqlc.ListBackupDrillResultsParams
	countCalls int
}

func (q *complianceDrillPages) CountBackupDrillResults(context.Context) (int64, error) {
	q.countCalls++
	return q.count, q.countError
}

func (q *complianceDrillPages) ListBackupDrillResults(_ context.Context, p sqlc.ListBackupDrillResultsParams) ([]sqlc.BackupDrillResult, error) {
	q.calls = append(q.calls, p)
	if p.Limit <= 0 || p.Offset < 0 || q.bounded && p.Limit > 200 {
		return nil, errors.New("owned drill page arguments exceed finite batch")
	}
	start := int(p.Offset)
	if start >= len(q.rows) {
		return nil, nil
	}
	end := start + int(p.Limit)
	if end > len(q.rows) {
		end = len(q.rows)
	}
	return q.rows[start:end], nil
}

func complianceDrillFixture(size int) *complianceDrillPages {
	q := &complianceDrillPages{count: int64(size)}
	for i := 0; i < size; i++ {
		q.rows = append(q.rows, sqlc.BackupDrillResult{ID: complianceCollectionID("drill", i), Status: "success", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(-i) * time.Second)})
	}
	return q
}

type complianceFailWriter struct{}

func (complianceFailWriter) Write([]byte) (int, error) { return 0, complianceCollectionReadFailure }

func TestComplianceDrillHistoryCollectionCompleteness(t *testing.T) {
	t.Run("10001-count-failure", func(t *testing.T) {
		q := complianceDrillFixture(10001)
		q.countError = complianceCollectionReadFailure
		var body bytes.Buffer
		n, err := WriteBackupDrillHistoryCSV(context.Background(), &body, q)
		if !errors.Is(err, complianceCollectionReadFailure) || n != 0 || len(q.calls) != 0 {
			t.Fatal("drill history count failure silently exported a successful 10000-row subset")
		}
	})
	for _, tc := range []struct {
		name  string
		count int64
	}{{"overflow-count", 1 << 31}, {"negative-count", -1}} {
		t.Run(tc.name, func(t *testing.T) {
			q := &complianceDrillPages{count: tc.count}
			n, err := WriteBackupDrillHistoryCSV(context.Background(), io.Discard, q)
			if err == nil || n != 0 || len(q.calls) != 0 {
				t.Fatal("drill history invalid count reached SQL pagination")
			}
		})
	}
	t.Run("10001-bounded-pages", func(t *testing.T) {
		q := complianceDrillFixture(10001)
		q.bounded = true
		var body bytes.Buffer
		n, err := WriteBackupDrillHistoryCSV(context.Background(), &body, q)
		if err != nil || n != 10001 || len(q.calls) != 51 {
			t.Fatal("drill history failed complete bounded 10001-row traversal")
		}
		rows, err := csv.NewReader(bytes.NewReader(body.Bytes())).ReadAll()
		if err != nil || len(rows) != 10002 || rows[1][0] != q.rows[0].ID.String() || rows[10001][0] != q.rows[10000].ID.String() {
			t.Fatal("drill history changed CSV rows or ordering during bounded traversal")
		}
		seen := map[string]bool{}
		for _, row := range rows[1:] {
			if seen[row[0]] {
				t.Fatal("drill history duplicated a row while paging")
			}
			seen[row[0]] = true
		}
	})
	t.Run("short-collection", func(t *testing.T) {
		q := complianceDrillFixture(200)
		q.count = 201
		if _, err := WriteBackupDrillHistoryCSV(context.Background(), io.Discard, q); err == nil {
			t.Fatal("drill history claimed a complete export after count-row mismatch")
		}
	})
	t.Run("cancelled-before-read", func(t *testing.T) {
		q := complianceDrillFixture(1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := WriteBackupDrillHistoryCSV(ctx, io.Discard, q); !errors.Is(err, context.Canceled) || q.countCalls != 0 || len(q.calls) != 0 {
			t.Fatal("cancelled drill history still read a collection")
		}
	})
	t.Run("flush-error", func(t *testing.T) {
		q := complianceDrillFixture(1)
		if _, err := WriteBackupDrillHistoryCSV(context.Background(), complianceFailWriter{}, q); !errors.Is(err, complianceCollectionReadFailure) {
			t.Fatal("drill CSV reported success after buffered output flush failed")
		}
	})
	t.Run("empty", func(t *testing.T) {
		q := complianceDrillFixture(0)
		var body bytes.Buffer
		n, err := WriteBackupDrillHistoryCSV(context.Background(), &body, q)
		rows, csvErr := csv.NewReader(bytes.NewReader(body.Bytes())).ReadAll()
		if err != nil || csvErr != nil || n != 0 || len(rows) != 1 || len(q.calls) != 0 {
			t.Fatal("empty drill history header contract changed")
		}
	})
	t.Run("single-row", func(t *testing.T) {
		q := complianceDrillFixture(1)
		var body bytes.Buffer
		n, err := WriteBackupDrillHistoryCSV(context.Background(), &body, q)
		rows, csvErr := csv.NewReader(bytes.NewReader(body.Bytes())).ReadAll()
		if err != nil || csvErr != nil || n != 1 || len(rows) != 2 || len(rows[1]) != 7 || rows[1][0] != q.rows[0].ID.String() || rows[1][3] != "success" || rows[1][4] != "" {
			t.Fatal("single drill history CSV contract changed")
		}
	})
}
