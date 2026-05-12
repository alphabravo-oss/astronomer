// Compliance export handler tests.
//
// Each test wires a tiny in-memory fakeComplianceQuerier (defined at
// the bottom of the file) into the real ComplianceHandler. The fake
// only has to satisfy the methods the test exercises; the seam is
// the ComplianceQuerier interface union in compliance.go.
package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// ── helpers ────────────────────────────────────────────────────────────

// makeComplianceRequest builds a GET request with an authenticated
// user injected via SetAuthenticatedUserForTest. The caller's ID is
// stamped onto context as the "current user" the gate() helper reads.
func makeComplianceRequest(target string, callerID uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         callerID.String(),
		AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

// readZipFiles returns a map of filename → file bytes from a ZIP
// archive in memory. The test asserts both presence and content.
func readZipFiles(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

// ── core test cases ────────────────────────────────────────────────────

func TestCompliance_StreamSmallRange(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, true)

	// Seed a tiny audit log and one binding so each section has at
	// least one row to write.
	q.auditRows = []sqlc.AuditLog{
		{
			ID:        uuid.New(),
			CreatedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
			Action:    "platform.thing.touched",
			Detail:    []byte(`{"k":"v"}`),
		},
		{
			ID:        uuid.New(),
			CreatedAt: time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC),
			Action:    "auth.login.succeeded",
			Detail:    []byte(`{}`),
		},
	}
	q.bindings = []sqlc.ListAllRoleBindingsWithRoleNamesRow{{
		Scope:     "global",
		BindingID: uuid.New(),
		UserID:    validUUID(callerID),
		RoleID:    uuid.New(),
		RoleName:  "platform-admin",
		Source:    "manual",
		CreatedAt: time.Now().UTC(),
	}}
	q.clusters = []sqlc.Cluster{{
		ID:          uuid.New(),
		Name:        "edge-1",
		DisplayName: "Edge",
		Environment: "production",
		Status:      "active",
	}}
	q.tokens = []sqlc.ComplianceAPITokenRow{{
		ID:           uuid.New(),
		UserID:       callerID,
		Username:     "alice",
		Email:        "alice@example.com",
		Name:         "ci-token",
		Prefix:       "ast_abc",
		Scopes:       []byte(`["read"]`),
		AllowedCIDRs: "10.0.0.0/8",
		CreatedAt:    time.Now().UTC(),
	}}
	q.projects = []sqlc.ComplianceProjectRow{{
		ID:                       uuid.New(),
		Name:                     "p1",
		ClusterID:                uuid.New(),
		PodSecurityProfile:       "restricted",
		NetworkPolicyMode:        "default-deny",
		ResourceQuotaCpuLimit:    "10",
		ResourceQuotaMemoryLimit: "32Gi",
		ResourceQuotaPodCount:    50,
	}}

	h := NewComplianceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", callerID)
	h.Export(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q, want application/zip", ct)
	}

	files := readZipFiles(t, w.Body.Bytes())
	expected := []string{
		"audit-log.csv",
		"auth-events.csv",
		"rbac-snapshot.csv",
		"cluster-inventory.csv",
		"access-tokens.csv",
		"backup-drill-history.csv",
		"policy-snapshot.json",
		"README.md",
	}
	for _, name := range expected {
		if _, ok := files[name]; !ok {
			t.Errorf("bundle missing %q (have: %v)", name, complianceKeys(files))
		}
	}

	// Header rows look right.
	for _, name := range []string{
		"audit-log.csv", "auth-events.csv", "rbac-snapshot.csv",
		"cluster-inventory.csv", "access-tokens.csv", "backup-drill-history.csv",
	} {
		if !bytes.HasPrefix(files[name], []byte(headerFirstFieldOf(name))) {
			t.Errorf("%s does not start with expected header; first 100 bytes: %q",
				name, string(files[name][:min(100, len(files[name]))]))
		}
	}

	// README mentions both controls.
	readme := string(files["README.md"])
	if !strings.Contains(readme, "CC7.2") {
		t.Errorf("README missing CC7.2 control mapping")
	}
	if !strings.Contains(readme, "A.12.4.1") {
		t.Errorf("README missing ISO 27001 control mapping")
	}
}

func TestCompliance_AuthEventsCSV_Filters(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, true)
	q.auditRows = []sqlc.AuditLog{
		{ID: uuid.New(), CreatedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC), Action: "auth.login.succeeded", Detail: []byte(`{}`)},
		{ID: uuid.New(), CreatedAt: time.Date(2026, 4, 10, 1, 0, 0, 0, time.UTC), Action: "auth.totp.enrolled", Detail: []byte(`{}`)},
		{ID: uuid.New(), CreatedAt: time.Date(2026, 4, 10, 2, 0, 0, 0, time.UTC), Action: "auth.group_sync.added", Detail: []byte(`{}`)},
		{ID: uuid.New(), CreatedAt: time.Date(2026, 4, 10, 3, 0, 0, 0, time.UTC), Action: "admin.user.locked", Detail: []byte(`{}`)},
		{ID: uuid.New(), CreatedAt: time.Date(2026, 4, 10, 4, 0, 0, 0, time.UTC), Action: "admin.group_mapping.created", Detail: []byte(`{}`)},
		// non-auth rows that MUST NOT appear in auth-events.csv:
		{ID: uuid.New(), CreatedAt: time.Date(2026, 4, 10, 5, 0, 0, 0, time.UTC), Action: "platform.cluster.upgraded", Detail: []byte(`{}`)},
		{ID: uuid.New(), CreatedAt: time.Date(2026, 4, 10, 6, 0, 0, 0, time.UTC), Action: "backup.drill.completed", Detail: []byte(`{}`)},
	}

	h := NewComplianceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", callerID)
	h.Export(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	files := readZipFiles(t, w.Body.Bytes())
	rows := parseCSV(t, files["auth-events.csv"])
	// Header + 5 matching rows
	if got, want := len(rows), 1+5; got != want {
		t.Fatalf("auth-events.csv rows = %d, want %d (header + 5 matches)", got, want)
	}
	// audit-log.csv should have all 7
	allRows := parseCSV(t, files["audit-log.csv"])
	if got, want := len(allRows), 1+7; got != want {
		t.Fatalf("audit-log.csv rows = %d, want %d", got, want)
	}
}

func TestCompliance_RBACSnapshot_IncludesSource(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, true)
	groupSyncBindingID := uuid.New()
	manualBindingID := uuid.New()
	q.bindings = []sqlc.ListAllRoleBindingsWithRoleNamesRow{
		{
			Scope:     "global",
			BindingID: manualBindingID,
			UserID:    validUUID(callerID),
			RoleID:    uuid.New(),
			RoleName:  "platform-admin",
			Source:    "manual",
		},
		{
			Scope:     "cluster",
			BindingID: groupSyncBindingID,
			UserID:    validUUID(callerID),
			RoleID:    uuid.New(),
			RoleName:  "cluster-viewer",
			ClusterID: validUUID(uuid.New()),
			Source:    "group_sync",
		},
	}

	h := NewComplianceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", callerID)
	h.Export(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	files := readZipFiles(t, w.Body.Bytes())
	rows := parseCSV(t, files["rbac-snapshot.csv"])
	// Header + 2 rows
	if len(rows) != 3 {
		t.Fatalf("rbac-snapshot rows = %d, want 3", len(rows))
	}
	// The 'source' column is at index 10 (see rbacSnapshotCSVHeader).
	sourceIdx := -1
	for i, h := range rows[0] {
		if h == "source" {
			sourceIdx = i
		}
	}
	if sourceIdx == -1 {
		t.Fatalf("source column missing from header: %v", rows[0])
	}
	gotSources := map[string]bool{rows[1][sourceIdx]: true, rows[2][sourceIdx]: true}
	if !gotSources["manual"] || !gotSources["group_sync"] {
		t.Errorf("expected both manual and group_sync sources; got %v", gotSources)
	}
}

func TestCompliance_PolicySnapshot_StructuredJSON(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, true)
	projID := uuid.New()
	q.projects = []sqlc.ComplianceProjectRow{{
		ID:                       projID,
		Name:                     "platform-team",
		DisplayName:              "Platform team",
		ClusterID:                uuid.New(),
		PodSecurityProfile:       "restricted",
		NetworkPolicyMode:        "default-deny",
		ResourceQuotaCpuLimit:    "20",
		ResourceQuotaMemoryLimit: "64Gi",
		ResourceQuotaPodCount:    100,
	}}
	q.projectBindings = map[uuid.UUID][]sqlc.ProjectRoleBinding{
		projID: {{
			ID:        uuid.New(),
			UserID:    validUUID(callerID),
			RoleID:    uuid.New(),
			ProjectID: projID,
			Source:    "manual",
		}},
	}
	q.user = sqlc.User{ID: callerID, IsSuperuser: true, Username: "alice", Email: "alice@example.com"}

	h := NewComplianceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", callerID)
	h.Export(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	files := readZipFiles(t, w.Body.Bytes())
	var entries []PolicySnapshotEntry
	if err := json.Unmarshal(files["policy-snapshot.json"], &entries); err != nil {
		t.Fatalf("policy-snapshot.json is not valid JSON: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.PodSecurityProfile != "restricted" {
		t.Errorf("pod_security_profile = %q, want restricted", e.PodSecurityProfile)
	}
	if e.ResourceQuotaPodCount != 100 {
		t.Errorf("resource_quota_pod_count = %d, want 100", e.ResourceQuotaPodCount)
	}
	if len(e.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(e.Members))
	}
	if !strings.Contains(e.Members[0], "alice") {
		t.Errorf("members[0] = %q, want it to mention alice", e.Members[0])
	}
}

func TestCompliance_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, false /* not superuser */)
	h := NewComplianceHandler(q, nil)

	w := httptest.NewRecorder()
	req := makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", callerID)
	h.Export(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	// Anonymous → 401, not 403.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", nil)
	h.Export(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d, want 401", w.Code)
	}
}

func TestCompliance_RejectsInvalidDateRange(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, true)
	h := NewComplianceHandler(q, nil)

	cases := []struct {
		name string
		path string
	}{
		{"to before from", "/api/v1/admin/compliance/export/?from=2026-05-01&to=2026-04-01"},
		{"to equal from", "/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-04-01"},
		{"unparseable from", "/api/v1/admin/compliance/export/?from=last-tuesday&to=2026-04-01"},
		{"missing to", "/api/v1/admin/compliance/export/?from=2026-04-01"},
		{"both missing", "/api/v1/admin/compliance/export/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := makeComplianceRequest(tc.path, callerID)
			h.Export(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCompliance_AsyncPathTriggered_LargeRange(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, true)
	q.countOverride = 200_000 // > the default 100K threshold

	tasks := &fakeEnqueuer{}
	h := NewComplianceHandler(q, tasks)
	w := httptest.NewRecorder()
	req := makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-01-01&to=2026-05-01", callerID)
	h.Export(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	if len(tasks.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(tasks.tasks))
	}
	if tasks.tasks[0].Type() != TaskTypeComplianceExport {
		t.Errorf("task type = %q, want %q", tasks.tasks[0].Type(), TaskTypeComplianceExport)
	}
	var body struct {
		Data struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			OutputKey string `json:"output_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Data.ID == "" {
		t.Fatalf("missing job id in response: %s", w.Body.String())
	}
	if body.Data.Status != ComplianceExportStatusPending {
		t.Errorf("status = %q, want %q", body.Data.Status, ComplianceExportStatusPending)
	}

	// And the status endpoint should now find it.
	statusW := httptest.NewRecorder()
	statusReq := makeComplianceRequest("/api/v1/admin/compliance/exports/"+body.Data.ID+"/", callerID)
	h.GetExportStatus(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d, want 200; body=%s", statusW.Code, statusW.Body.String())
	}
}

// TestCompliance_AccessTokensCSV_OmitsHash is the per-spec assertion
// that the bundle MUST NOT include any decrypted secret material —
// even the bcrypt-style token_hash. The ComplianceAPITokenRow struct
// has no TokenHash field; this test additionally asserts the CSV
// header doesn't mention it.
func TestCompliance_AccessTokensCSV_OmitsHash(t *testing.T) {
	callerID := uuid.New()
	q := newFakeComplianceQuerier(callerID, true)
	q.tokens = []sqlc.ComplianceAPITokenRow{{
		ID: uuid.New(), UserID: callerID, Name: "leaked-by-mistake",
	}}
	h := NewComplianceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeComplianceRequest("/api/v1/admin/compliance/export/?from=2026-04-01&to=2026-05-01", callerID)
	h.Export(w, req)

	files := readZipFiles(t, w.Body.Bytes())
	csvBody := files["access-tokens.csv"]
	if csvBody == nil {
		t.Fatalf("access-tokens.csv missing")
	}
	if bytes.Contains(bytes.ToLower(csvBody), []byte("token_hash")) {
		t.Errorf("access-tokens.csv leaked token_hash header")
	}
	if bytes.Contains(bytes.ToLower(csvBody), []byte("password")) {
		t.Errorf("access-tokens.csv contains 'password' substring (unexpected): %s", string(csvBody))
	}
}

// ── fakes ──────────────────────────────────────────────────────────────

// fakeComplianceQuerier is the in-memory ComplianceQuerier the tests
// drive. Every method either returns the pre-seeded slice or a
// pgx.ErrNoRows-ish empty result; nothing is dynamically looked up,
// which keeps the tests trivially deterministic.
type fakeComplianceQuerier struct {
	user            sqlc.User
	auditRows       []sqlc.AuditLog
	bindings        []sqlc.ListAllRoleBindingsWithRoleNamesRow
	clusters        []sqlc.Cluster
	tokens          []sqlc.ComplianceAPITokenRow
	projects        []sqlc.ComplianceProjectRow
	drillRows       []sqlc.BackupDrillResult
	drillCount      int64
	projectBindings map[uuid.UUID][]sqlc.ProjectRoleBinding
	countOverride   int64
}

func newFakeComplianceQuerier(callerID uuid.UUID, superuser bool) *fakeComplianceQuerier {
	return &fakeComplianceQuerier{
		user: sqlc.User{
			ID:          callerID,
			Username:    "tester",
			Email:       "tester@example.com",
			IsSuperuser: superuser,
		},
	}
}

func (f *fakeComplianceQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if id == f.user.ID {
		return f.user, nil
	}
	return f.user, nil
}
func (f *fakeComplianceQuerier) CountAuditLogV1ForRange(_ context.Context, _, _ time.Time) (int64, error) {
	if f.countOverride != 0 {
		return f.countOverride, nil
	}
	return int64(len(f.auditRows)), nil
}
func (f *fakeComplianceQuerier) ListAuditLogV1ForRange(_ context.Context, arg sqlc.ListAuditLogV1ForRangeParams) ([]sqlc.AuditLog, error) {
	// Honour the keyset cursor so the streamer's pagination contract
	// is actually tested rather than handed a free pass. Sort by
	// (created_at, id) ASC and filter to rows strictly after the
	// cursor, in [from, to).
	out := []sqlc.AuditLog{}
	for _, r := range f.auditRows {
		if r.CreatedAt.Before(arg.From) {
			continue
		}
		if !r.CreatedAt.Before(arg.To) {
			continue
		}
		if r.CreatedAt.Before(arg.AfterCreatedAt) {
			continue
		}
		if r.CreatedAt.Equal(arg.AfterCreatedAt) && uuidLE(r.ID, arg.AfterID) {
			continue
		}
		out = append(out, r)
	}
	// The seam doesn't have to perfectly emulate ASC ordering since
	// the test inputs are already in time order, but enforce a
	// stable order for safety. A bubble sort is plenty for the
	// handful of rows in these tests.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.Before(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if int32(len(out)) > arg.Limit {
		out = out[:arg.Limit]
	}
	return out, nil
}
func (f *fakeComplianceQuerier) ListAllRoleBindingsWithRoleNames(_ context.Context) ([]sqlc.ListAllRoleBindingsWithRoleNamesRow, error) {
	return f.bindings, nil
}
func (f *fakeComplianceQuerier) ListClusters(_ context.Context, _ sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return f.clusters, nil
}
func (f *fakeComplianceQuerier) GetClusterAgentTokenByClusterID(_ context.Context, _ uuid.UUID) (sqlc.ClusterAgentToken, error) {
	return sqlc.ClusterAgentToken{}, errPgxNoRowsForTest
}
func (f *fakeComplianceQuerier) ListAPITokensForCompliance(_ context.Context) ([]sqlc.ComplianceAPITokenRow, error) {
	return f.tokens, nil
}
func (f *fakeComplianceQuerier) ListBackupDrillResults(_ context.Context, _ sqlc.ListBackupDrillResultsParams) ([]sqlc.BackupDrillResult, error) {
	return f.drillRows, nil
}
func (f *fakeComplianceQuerier) CountBackupDrillResults(_ context.Context) (int64, error) {
	return f.drillCount, nil
}
func (f *fakeComplianceQuerier) ListAllProjectsForCompliance(_ context.Context) ([]sqlc.ComplianceProjectRow, error) {
	return f.projects, nil
}
func (f *fakeComplianceQuerier) ListProjectRoleBindingsByProject(_ context.Context, arg sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error) {
	if f.projectBindings == nil {
		return nil, nil
	}
	return f.projectBindings[arg.ProjectID], nil
}

// CreateAuditLogV1 lets recordAudit() write through the fake instead
// of no-oping silently. Tests don't currently assert on these but
// the surface keeps the audit code paths exercised.
func (f *fakeComplianceQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	return nil
}

// fakeEnqueuer is the asynq.Client stand-in used by the async-path
// test. Captures every Enqueue call so the test can assert on the
// task type + payload without standing up a Redis.
type fakeEnqueuer struct {
	tasks []*asynq.Task
	opts  [][]asynq.Option
}

func (f *fakeEnqueuer) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	f.tasks = append(f.tasks, task)
	f.opts = append(f.opts, opts)
	return &asynq.TaskInfo{ID: uuid.NewString(), Type: task.Type()}, nil
}

// ── tiny utilities used by the assertions ──────────────────────────────

// errPgxNoRowsForTest is a sentinel "no rows" error the fake returns
// from GetClusterAgentTokenByClusterID. The writer doesn't introspect
// the value — any non-nil err just means "leave the rotation columns
// empty".
var errPgxNoRowsForTest = stringErr("no rows")

type stringErr string

func (s stringErr) Error() string { return string(s) }

func validUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// uuidLE returns true when a <= b in the same byte-wise comparison
// the postgres uuid type uses. The streamer uses (created_at, id)
// > cursor, so the fake needs the same semantics to avoid re-emitting
// the cursor row.
func uuidLE(a, b uuid.UUID) bool {
	return bytes.Compare(a[:], b[:]) <= 0
}

func parseCSV(t *testing.T, body []byte) [][]string {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv read: %v", err)
	}
	return rows
}

func headerFirstFieldOf(filename string) string {
	switch filename {
	case "audit-log.csv", "auth-events.csv":
		return "id,"
	case "rbac-snapshot.csv":
		return "scope,"
	case "cluster-inventory.csv":
		return "id,"
	case "access-tokens.csv":
		return "id,"
	case "backup-drill-history.csv":
		return "id,"
	}
	return ""
}

// complianceKeys returns the keys of a map for diagnostic output.
// Named locally to avoid colliding with keysOf in
// logging_operation_test.go.
func complianceKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
