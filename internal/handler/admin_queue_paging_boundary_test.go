package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAdminDLQInvalidPagingCannotInspect(t *testing.T) {
	for _, query := range []string{"page=0", "page=-1", "page=10001", "page=99999999999999999999999", "page=", "page=1&page=2", "page=wat", "page=%zz", "page=1;unused=x", "page=1&offset=0", "page=1&cursor=opaque", "page=1&limit=1"} {
		t.Run(query, func(t *testing.T) {
			inspector := &pagedAdminQueueInspector{}
			w := adminQueueReadRequest(t, inspector, "/admin/queues/fixture/dlq/?"+query)
			if w.Code != http.StatusBadRequest || len(inspector.pages) != 0 || inspector.runs != 0 || inspector.deletes != 0 {
				t.Fatalf("invalid paging reached inspection or effects: status=%d pages=%v runs=%d deletes=%d", w.Code, inspector.pages, inspector.runs, inspector.deletes)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "invalid_request" {
				t.Fatalf("unsafe pagination category=%q", body.Error.Code)
			}
		})
	}
}

func TestAdminDLQNativePagesKeepLegacyPayloadAndHonestContinuation(t *testing.T) {
	for _, test := range []struct {
		query          string
		page, length   int
		more, metadata bool
	}{
		{"", 1, 100, false, false}, {"?page=1", 1, 100, true, true}, {"?page=2", 2, 1, false, true}, {"?page=3", 3, 0, false, true}, {"?page=10000", 10000, 0, false, true},
	} {
		t.Run(test.query, func(t *testing.T) {
			i := &pagedAdminQueueInspector{}
			w := adminQueueReadRequest(t, i, "/admin/queues/fixture/dlq/"+test.query)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d", w.Code)
			}
			var response struct {
				Data struct {
					Queue string     `json:"queue"`
					DLQ   []DLQEntry `json:"dlq"`
					Count int        `json:"count"`
				} `json:"data"`
				Pagination *struct {
					Total      *int64 `json:"total"`
					Limit      int    `json:"limit"`
					Offset     int    `json:"offset"`
					HasMore    bool   `json:"has_more"`
					NextOffset *int   `json:"next_offset"`
				} `json:"pagination"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(i.pages) != 1 || i.pages[0] != test.page || response.Data.Queue != "fixture" || response.Data.Count != test.length || len(response.Data.DLQ) != test.length || response.Data.DLQ == nil {
				t.Fatalf("native paging/payload drift: pages=%v count=%d", i.pages, response.Data.Count)
			}
			if (response.Pagination != nil) != test.metadata {
				t.Fatal("legacy response acquired opt-in paging fields")
			}
			if test.metadata {
				page := response.Pagination
				if page.Total != nil || page.Limit != 100 || page.Offset != (test.page-1)*100 || page.HasMore != test.more {
					t.Fatalf("fabricated count/native offset: %+v", page)
				}
				if test.more {
					if page.NextOffset == nil || *page.NextOffset != 100 {
						t.Fatal("first native page does not advance")
					}
				} else if page.NextOffset != nil {
					t.Fatal("short native page offered another continuation")
				}
			}
		})
	}
}
