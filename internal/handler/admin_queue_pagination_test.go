package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

type pagedAdminQueueInspector struct {
	adminQueuesTestInspector
	inspectionErr error
	pages         []int
}

func (*pagedAdminQueueInspector) Queues() ([]string, error) { return []string{"fixture"}, nil }

func (i *pagedAdminQueueInspector) GetQueueInfo(string) (*asynq.QueueInfo, error) {
	if i.inspectionErr != nil {
		return nil, i.inspectionErr
	}
	return &asynq.QueueInfo{Size: 101, Archived: 101}, nil
}

func (i *pagedAdminQueueInspector) ListArchivedTasks(_ string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	page, size := 1, 30 // Defaults used by the actual Asynq inspector.
	for _, opt := range opts {
		switch reflect.TypeOf(opt) {
		case reflect.TypeOf(asynq.Page(1)):
			page = int(reflect.ValueOf(opt).Int())
		case reflect.TypeOf(asynq.PageSize(1)):
			size = int(reflect.ValueOf(opt).Int())
		}
	}
	i.pages = append(i.pages, page)
	var result []*asynq.TaskInfo
	for n := (page - 1) * size; n < page*size && n < 101; n++ {
		result = append(result, &asynq.TaskInfo{ID: fmt.Sprintf("fixture-task-%03d", n+1), Type: "fixture"})
	}
	return result, nil
}

func adminQueueReadRequest(t *testing.T, inspector SupportBundleAsynqInspector, target string) *httptest.ResponseRecorder {
	t.Helper()
	actor := uuid.New()
	q := &adminQueuesTestQueries{user: sqlc.User{ID: actor, IsActive: true, IsSuperuser: true}}
	h := NewAdminQueuesHandler(inspector, q)
	router := chi.NewRouter()
	router.Get("/admin/queues/", h.List)
	router.Get("/admin/queues/{queue}/dlq/", h.DLQ)
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r = r.WithContext(reqctx.WithUser(context.Background(), &reqctx.User{ID: actor.String(), AuthMethod: "jwt"}))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestAdminQueueDLQPageTwoExposesTask101(t *testing.T) {
	i := &pagedAdminQueueInspector{}
	w := adminQueueReadRequest(t, i, "/admin/queues/fixture/dlq/?page=2")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var response struct {
		Data struct {
			DLQ   []DLQEntry `json:"dlq"`
			Count int        `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.DLQ) != 1 || response.Data.DLQ[0].ID != "fixture-task-101" || response.Data.Count != 1 {
		t.Fatalf("task 101 unreachable: entries=%d count=%d pages=%v", len(response.Data.DLQ), response.Data.Count, i.pages)
	}
}

func TestAdminQueueInspectionFailureCannotAdvertiseHealthyZeroDepth(t *testing.T) {
	i := &pagedAdminQueueInspector{inspectionErr: errors.New("private upstream diagnostic")}
	w := adminQueueReadRequest(t, i, "/admin/queues/")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable queue advertised as inspected: status=%d", w.Code)
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "inspector_unavailable" {
		t.Fatalf("unsafe error category=%q", response.Error.Code)
	}
}
