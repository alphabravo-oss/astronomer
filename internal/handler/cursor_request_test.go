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
		mode        bool
	}{
		{"legacy absent cursor", "", codec, http.StatusOK, false},
		{"explicit initial", "cursor=", codec, http.StatusOK, true},
		{"repeated initial", "cursor=&cursor=", codec, http.StatusBadRequest, false},
		{"offset with initial", "cursor=&offset=0", codec, http.StatusBadRequest, false},
		{"malformed continuation", "cursor=p1.invalid", codec, http.StatusBadRequest, false},
		{"malformed cursor query", "cursor=%GG", codec, http.StatusBadRequest, false},
		{"malformed filter query", "search=a;b&cursor=", codec, http.StatusBadRequest, false},
		{"authority unavailable", "cursor=", nil, http.StatusServiceUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: uuid.NewString()}))
			rec := httptest.NewRecorder()
			page, ok := readCursorRequest(rec, req, 20, tc.codec, true, "fixture-binding")
			if rec.Code != tc.want || ok != (tc.want == http.StatusOK) {
				t.Fatalf("status%d ok%v; want%d", rec.Code, ok, tc.want)
			}
			wantLimit := int32(20)
			if tc.mode {
				wantLimit++
			}
			if ok && (page.mode != tc.mode || page.provided || page.queryLimit() != wantLimit || page.seen != 0) {
				t.Fatal("empty initial cursor was decoded as a position or lost signed sentinel mode")
			}
		})
	}
}
