package handler

import (
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCursorRequestEmptyInitialAndInvalidContinuation(t *testing.T) {
	codec, err := paging.NewCursorCodec("fixture-cursor-authority")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query string
		codec       *paging.CursorCodec
		want        int
	}{
		{"explicit initial", "cursor=", codec, http.StatusOK},
		{"repeated initial", "cursor=&cursor=", codec, http.StatusBadRequest},
		{"offset with initial", "cursor=&offset=0", codec, http.StatusBadRequest},
		{"malformed continuation", "cursor=p1.invalid", codec, http.StatusBadRequest},
		{"authority unavailable", "cursor=", nil, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: uuid.NewString()}))
			rec := httptest.NewRecorder()
			page, ok := readCursorRequest(rec, req, 20, tc.codec, true, "fixture-binding")
			if rec.Code != tc.want || ok != (tc.want == http.StatusOK) {
				t.Fatalf("status%d ok%v; want%d", rec.Code, ok, tc.want)
			}
			if ok && (!page.mode || page.provided || page.queryLimit() != 21 || page.seen != 0) {
				t.Fatal("empty initial cursor was decoded as a position or lost signed sentinel mode")
			}
		})
	}
}
