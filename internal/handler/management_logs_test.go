package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeManagementLogsQuerier is the minimum ManagementLogsQuerier the
// tests need. Same shape as the drill/queues fakes — return canned user
// rows + count audit invocations.
type fakeManagementLogsQuerier struct {
	user    sqlc.User
	userErr error

	// auditCalls is bumped each time recordAudit's CreateAuditLogV1
	// helper finds this fake satisfies auditWriterV1.
	auditCalls int
}

func (f *fakeManagementLogsQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, f.userErr
}

func (f *fakeManagementLogsQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	f.auditCalls++
	return nil
}

// newK8sWithPod builds a fake clientset pre-populated with a
// management-plane pod for the given component. The fake's GetLogs
// returns "fake logs" (no timestamp prefix); scanLogStream parses that
// into a single ManagementLogLine with empty Timestamp.
func newK8sWithPod(component string) kubernetes.Interface {
	return k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "astronomer-" + component + "-0",
			Namespace: "astronomer",
			Labels: map[string]string{
				"app.kubernetes.io/component": component,
				"app.kubernetes.io/part-of":   "astronomer",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: component},
			},
		},
	})
}

func TestManagementLogsHandler_TailServer(t *testing.T) {
	callerID := uuid.New()
	q := &fakeManagementLogsQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	k8s := newK8sWithPod("server")

	h := NewManagementLogsHandler(q, k8s, "astronomer", "astronomer")

	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/management-logs/?component=server", callerID)
	h.Tail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Data ManagementLogsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if got.Data.Component != "server" {
		t.Fatalf("component = %q, want server", got.Data.Component)
	}
	if len(got.Data.Lines) == 0 {
		t.Fatalf("lines empty; expected the fake clientset's canned logs")
	}
	// fake clientset returns the literal string "fake logs" with no
	// timestamp prefix; the handler should preserve it untouched.
	if got.Data.Lines[0].Message != "fake logs" {
		t.Fatalf("first line message = %q, want %q", got.Data.Lines[0].Message, "fake logs")
	}
	if got.Data.Lines[0].Pod == "" {
		t.Fatalf("first line pod is empty, want a value")
	}
	if got.Data.Lines[0].Container != "server" {
		t.Fatalf("first line container = %q, want server", got.Data.Lines[0].Container)
	}
	if q.auditCalls != 1 {
		t.Fatalf("audit calls = %d, want 1 (one per request)", q.auditCalls)
	}
}

func TestManagementLogsHandler_TailWorker(t *testing.T) {
	callerID := uuid.New()
	q := &fakeManagementLogsQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	k8s := newK8sWithPod("worker")

	h := NewManagementLogsHandler(q, k8s, "astronomer", "astronomer")

	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/management-logs/?component=worker", callerID)
	h.Tail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Data ManagementLogsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.Component != "worker" {
		t.Fatalf("component = %q, want worker", got.Data.Component)
	}
	if len(got.Data.Lines) == 0 {
		t.Fatalf("lines empty; expected the fake clientset's canned logs")
	}
}

func TestManagementLogsHandler_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := &fakeManagementLogsQuerier{user: sqlc.User{ID: callerID, IsSuperuser: false}}
	k8s := newK8sWithPod("server")

	h := NewManagementLogsHandler(q, k8s, "astronomer", "astronomer")

	t.Run("NonSuperuser403", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := makeRequest("/api/v1/admin/management-logs/?component=server", callerID)
		h.Tail(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("Anonymous401", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/management-logs/?component=server", nil)
		h.Tail(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
}

func TestManagementLogsHandler_BadComponent(t *testing.T) {
	callerID := uuid.New()
	q := &fakeManagementLogsQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	k8s := newK8sWithPod("server")

	h := NewManagementLogsHandler(q, k8s, "astronomer", "astronomer")

	t.Run("Missing", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := makeRequest("/api/v1/admin/management-logs/", callerID)
		h.Tail(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (component required)", w.Code)
		}
	})

	t.Run("Disallowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := makeRequest("/api/v1/admin/management-logs/?component=postgres", callerID)
		h.Tail(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (component must be in allow-list)", w.Code)
		}
	})
}

func TestManagementLogsHandler_K8sUnwired(t *testing.T) {
	callerID := uuid.New()
	q := &fakeManagementLogsQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}

	// k8sClient = nil → expect 503 logs_unavailable on every component.
	h := NewManagementLogsHandler(q, nil, "astronomer", "astronomer")

	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/management-logs/?component=server", callerID)
	h.Tail(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestManagementLogsHandler_NoMatchingPods(t *testing.T) {
	callerID := uuid.New()
	q := &fakeManagementLogsQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	// Build a clientset with NO matching pods at all.
	k8s := k8sfake.NewSimpleClientset()

	h := NewManagementLogsHandler(q, k8s, "astronomer", "astronomer")

	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/management-logs/?component=server", callerID)
	h.Tail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty result, not error)", w.Code)
	}
	var got struct {
		Data ManagementLogsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Data.Lines) != 0 {
		t.Fatalf("lines = %d, want 0", len(got.Data.Lines))
	}
}

func TestManagementLogsHandler_SinceTimestampInvalid(t *testing.T) {
	callerID := uuid.New()
	q := &fakeManagementLogsQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	k8s := newK8sWithPod("server")

	h := NewManagementLogsHandler(q, k8s, "astronomer", "astronomer")

	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/management-logs/?component=server&since=not-a-timestamp", callerID)
	h.Tail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (since must be RFC3339)", w.Code)
	}
}

func TestScanLogStream_ParsesTimestampPrefix(t *testing.T) {
	// kubelet's `timestamps=true` mode prefixes each line with an
	// RFC3339Nano timestamp. scanLogStream should split that off.
	body := "2026-05-12T03:00:00.123Z hello world\nplain line\n"
	out := []ManagementLogLine{}
	lines, bytes := 10, 4096
	added, trunc := scanLogStream(strings.NewReader(body), "p", "c", &out, &lines, &bytes)
	if trunc {
		t.Fatalf("truncated = true, want false")
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if out[0].Timestamp != "2026-05-12T03:00:00.123Z" {
		t.Fatalf("[0].Timestamp = %q, want 2026-05-12T03:00:00.123Z", out[0].Timestamp)
	}
	if out[0].Message != "hello world" {
		t.Fatalf("[0].Message = %q, want hello world", out[0].Message)
	}
	if out[1].Timestamp != "" {
		t.Fatalf("[1].Timestamp = %q, want empty (no prefix)", out[1].Timestamp)
	}
	if out[1].Message != "plain line" {
		t.Fatalf("[1].Message = %q, want plain line", out[1].Message)
	}
}

func TestScanLogStream_RespectsLineCap(t *testing.T) {
	body := strings.Repeat("line\n", 100)
	out := []ManagementLogLine{}
	lines, bytes := 5, 4096
	added, trunc := scanLogStream(strings.NewReader(body), "p", "c", &out, &lines, &bytes)
	if added != 5 {
		t.Fatalf("added = %d, want 5", added)
	}
	if !trunc {
		t.Fatalf("truncated = false, want true (line cap hit)")
	}
}

func TestScanLogStream_RespectsByteCap(t *testing.T) {
	body := strings.Repeat("aaaaaaaaaa\n", 100) // each "aaaaaaaaaa\n" costs 11
	out := []ManagementLogLine{}
	lines, bytes := 1000, 25 // 2 full lines + a slack
	added, trunc := scanLogStream(strings.NewReader(body), "p", "c", &out, &lines, &bytes)
	if !trunc {
		t.Fatalf("truncated = false, want true (byte cap hit)")
	}
	if added > 3 {
		t.Fatalf("added = %d, want <= 3 lines under 25-byte cap", added)
	}
}

func TestManagementLogsHandler_SetCaps(t *testing.T) {
	q := &fakeManagementLogsQuerier{}
	h := NewManagementLogsHandler(q, nil, "ns", "rel")
	if h.maxLines != defaultMaxLines || h.maxBytes != defaultMaxBytes {
		t.Fatalf("defaults wrong: lines=%d bytes=%d", h.maxLines, h.maxBytes)
	}
	h.SetCaps(50, 4096)
	if h.maxLines != 50 || h.maxBytes != 4096 {
		t.Fatalf("after SetCaps(50,4096) got lines=%d bytes=%d", h.maxLines, h.maxBytes)
	}
	// Non-positive values must be ignored (defaults stick).
	h.SetCaps(-1, 0)
	if h.maxLines != 50 || h.maxBytes != 4096 {
		t.Fatalf("non-positive SetCaps should be a no-op; got lines=%d bytes=%d", h.maxLines, h.maxBytes)
	}
}
