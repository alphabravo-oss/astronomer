package handler

import (
	"net/http"
	"net/url"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
)

type cursorRequest struct {
	limit    int32
	offset   int32
	seen     int
	provided bool
	mode     bool
	binding  string
	cursor   paging.Cursor
	codec    *paging.CursorCodec
}

func cursorActor(r *http.Request) string {
	actor := reqctx.UserUUID(r.Context())
	if !actor.Valid {
		return ""
	}
	return uuid.UUID(actor.Bytes).String()
}

func readCursorRequest(w http.ResponseWriter, r *http.Request, defaultLimit int, codec *paging.CursorCodec, capable bool, binding string) (cursorRequest, bool) {
	page := cursorRequest{binding: binding, codec: codec}
	query, ok := readCursorQuery(w, r)
	if !ok {
		return page, false
	}
	limit := queryLimit(r, defaultLimit)
	if limit < 1 || limit > defaultLimitCap {
		limit = defaultLimitCap
	}
	page.limit = int32(limit)
	offset, err := paging.LegacyOffset(query)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.OffsetNotSupported, "offset must be one value in the supported range 0..10000; this legacy offset cannot continue beyond that range")
		return page, false
	}
	page.offset = offset
	page.seen = int(offset)
	values, provided := query["cursor"]
	// An explicit empty cursor opts into the signed initial page. Only a
	// nonempty token carries a position; malformed tokens never restart.
	page.provided = provided && len(values) == 1 && values[0] != ""
	_, offsetProvided := query["offset"]
	if provided && (len(values) != 1 || offsetProvided) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cursor must be one opaque value and cannot be combined with offset")
		return page, false
	}
	page.mode = provided && !offsetProvided && codec != nil && capable
	if provided && !page.mode {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Signed cursor listing is unavailable")
		return page, false
	}
	if page.mode && cursorActor(r) == "" {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "An authenticated pagination actor is required")
		return page, false
	}
	if page.provided {
		page.cursor, err = codec.Decode(values[0], binding)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid or expired pagination cursor")
			return page, false
		}
		page.seen = page.cursor.Seen
	}
	return page, true
}

func (page cursorRequest) queryLimit() int32 {
	if page.mode {
		return page.limit + 1
	}
	return page.limit
}

func trimCursorRows[T any](rows []T, page cursorRequest) ([]T, bool) {
	if page.mode && len(rows) > int(page.limit) {
		return rows[:page.limit], true
	}
	return rows, false
}

func (page cursorRequest) metadata(w http.ResponseWriter, r *http.Request, length int, next *paging.Cursor, total *int64) (paging.Metadata, bool) {
	if !page.mode {
		if total != nil {
			return paging.Exact(*total, int(page.limit), page.seen, length), true
		}
		return paging.FromPage(int(page.limit), page.seen, length), true
	}
	token := ""
	if next != nil {
		var err error
		token, err = page.codec.Encode(*next)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode pagination cursor")
			return paging.Metadata{}, false
		}
	}
	return paging.CursorUncounted(int(page.limit), page.seen, length, token), true
}

// Parse query syntax once before scope resolution. URL.Query silently drops
// malformed values, which could otherwise restart an invalid continuation.
func readCursorQuery(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Malformed pagination query")
		return nil, false
	}
	for key, values := range query {
		if key == "" || len(values) != 1 {
			code := apierror.ValidationError
			if key == "offset" {
				code = apierror.OffsetNotSupported
			}
			RespondRequestError(w, r, http.StatusBadRequest, code, "Pagination query parameters must each have one value")
			return nil, false
		}
	}
	return query, true
}
