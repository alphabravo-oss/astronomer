package astroclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// WithAdminDLQPage supplies the native one-based page declared by the admin
// DLQ contract without changing the released generated method or struct layout.
// Each page contains at most 100 tasks. Continue until count is below 100,
// possibly requiring an empty final page. Task ordering is mutable Redis state.
func WithAdminDLQPage(page int) RequestEditorFn {
	return func(ctx context.Context, r *http.Request) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if page < 1 || page > 10000 {
			return errors.New("admin DLQ page must be in 1..10000")
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return errors.New("admin DLQ query is malformed")
		}
		for key, values := range query {
			if key == "" || len(values) != 1 {
				return errors.New("admin DLQ query parameters must each have one value")
			}
		}
		if query.Has("page") || query.Has("offset") || query.Has("cursor") || query.Has("limit") {
			return errors.New("admin DLQ page must be one value without offset, cursor or limit")
		}
		query.Set("page", strconv.Itoa(page))
		r.URL.RawQuery = query.Encode()
		return nil
	}
}
