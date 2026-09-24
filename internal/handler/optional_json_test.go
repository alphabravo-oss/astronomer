package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeOptionalJSON(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{"", true}, {"  ", true}, {`{}`, true}, {`{"revision":2}`, true}, {`null`, false},
		{`{"revision":`, false}, {`{"revision":"two"}`, false}, {`{"revision":2} {}`, false},
		{`{"revision":2} garbage`, false}, {`{"revison":2}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var req struct {
				Revision int `json:"revision"`
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			if got := decodeOptionalJSON(w, r, &req); got != tc.ok {
				t.Fatalf("accepted=%v, want %v", got, tc.ok)
			}
			if !tc.ok && w.Code != 400 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}
