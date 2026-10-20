package handler

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
)

const (
	adminDLQPageSize        = 100
	maxAdminDLQPage         = 10000
	maxInspectedAdminQueues = 1024
)

// readAdminDLQPage validates one explicit native Asynq page. The bound is
// separate from SQL legacy offsets; it is never clamped or silently ignored.
func readAdminDLQPage(w http.ResponseWriter, r *http.Request) (int, bool, bool) {
	// ParseQuery, unlike URL.Query, reports malformed escaping and separators.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidRequest, "queue pagination query is malformed")
		return 0, false, false
	}
	if query.Has("cursor") || query.Has("offset") || query.Has("limit") {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidRequest, "this queue supports fixed-size native page pagination")
		return 0, false, false
	}
	values, supplied := query["page"]
	if !supplied {
		return 1, false, true
	}
	if len(values) != 1 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidRequest, "page must be one integer in 1..10000")
		return 0, true, false
	}
	page, parseErr := strconv.ParseUint(values[0], 10, 14)
	if parseErr != nil || page < 1 || page > maxAdminDLQPage {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidRequest, "page must be one integer in 1..10000")
		return 0, true, false
	}
	return int(page), true, true
}

func validAdminQueueInfo(info *asynq.QueueInfo) bool {
	return info != nil && info.Size >= 0 && info.Active >= 0 && info.Pending >= 0 &&
		info.Scheduled >= 0 && info.Retry >= 0 && info.Archived >= 0 && info.Completed >= 0
}

// Asynq's inspector API is contextless. Check before every vendor call and
// after it returns; cancellation cannot interrupt an in-flight vendor call.
func adminQueueRequestActive(w http.ResponseWriter, r *http.Request) bool {
	if r.Context().Err() != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.InspectorUnavailable, "queue inspection no longer active")
		return false
	}
	return true
}
