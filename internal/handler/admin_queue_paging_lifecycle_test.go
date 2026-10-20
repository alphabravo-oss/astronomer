package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

type lifecycleAdminInspector struct {
	pagedAdminQueueInspector
	cancel       context.CancelFunc
	infoCalls    int
	cancelAtInfo bool
	cancelAtDLQ  bool
}

func (*lifecycleAdminInspector) Queues() ([]string, error) { return []string{"first", "second"}, nil }
func (i *lifecycleAdminInspector) GetQueueInfo(string) (*asynq.QueueInfo, error) {
	i.infoCalls++
	if i.cancelAtInfo {
		i.cancel()
	}
	return &asynq.QueueInfo{}, nil
}
func (i *lifecycleAdminInspector) ListArchivedTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	result, err := i.pagedAdminQueueInspector.ListArchivedTasks(queue, opts...)
	if i.cancelAtDLQ {
		i.cancel()
	}
	return result, err
}

func TestAdminQueueCanceledReadDoesNotInspectOrPublishPartialPayload(t *testing.T) {
	for _, test := range []struct {
		name, path            string
		before, atInfo, atDLQ bool
		infos, pages          int
	}{
		{"before-list", "/admin/queues/", true, false, false, 0, 0},
		{"before-dlq", "/admin/queues/fixture/dlq/?page=2", true, false, false, 0, 0},
		{"between-queues", "/admin/queues/", false, true, false, 1, 0},
		{"after-dlq-call", "/admin/queues/fixture/dlq/?page=2", false, false, true, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			i := &lifecycleAdminInspector{cancel: cancel, cancelAtInfo: test.atInfo, cancelAtDLQ: test.atDLQ}
			actor := uuid.New()
			q := &adminQueuesTestQueries{user: sqlc.User{ID: actor, IsActive: true, IsSuperuser: true}}
			h := NewAdminQueuesHandler(i, q)
			router := chi.NewRouter()
			router.Get("/admin/queues/", h.List)
			router.Get("/admin/queues/{queue}/dlq/", h.DLQ)
			if test.before {
				cancel()
			}
			r := httptest.NewRequest(http.MethodGet, test.path, nil).WithContext(reqctx.WithUser(ctx, &reqctx.User{ID: actor.String(), AuthMethod: "jwt"}))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != apiutil.StatusClientClosedRequest || w.Body.Len() != 0 || i.infoCalls != test.infos || len(i.pages) != test.pages || i.runs != 0 || i.deletes != 0 {
				t.Fatalf("canceled inspection returned partial payload or additional effects: status=%d bytes=%d infos=%d pages=%d", w.Code, w.Body.Len(), i.infoCalls, len(i.pages))
			}
		})
	}
}

func TestAdminDLQContinuationRechecksCurrentSuperuserAuthority(t *testing.T) {
	actor := uuid.New()
	q := &adminQueuesTestQueries{user: sqlc.User{ID: actor, IsActive: true, IsSuperuser: true}}
	i := &pagedAdminQueueInspector{}
	h := NewAdminQueuesHandler(i, q)
	router := chi.NewRouter()
	router.Get("/admin/queues/{queue}/dlq/", h.DLQ)
	read := func(page string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/admin/queues/fixture/dlq/?page="+page, nil)
		r = r.WithContext(reqctx.WithUser(r.Context(), &reqctx.User{ID: actor.String(), AuthMethod: "jwt"}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := read("1"); w.Code != http.StatusOK {
		t.Fatalf("first page status=%d", w.Code)
	}
	q.user.IsSuperuser = false
	w := read("2")
	if w.Code != http.StatusForbidden || len(i.pages) != 1 || i.pages[0] != 1 {
		t.Fatalf("revoked continuation reached inspector: status=%d pages=%v", w.Code, i.pages)
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "forbidden" {
		t.Fatalf("revocation category=%q", response.Error.Code)
	}
}
