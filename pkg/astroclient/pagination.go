package astroclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// WithPaginationCursor opts a converted list operation into signed pagination.
// Pass an empty string for its initial page, then the exact next_cursor from
// each response. Use it with the existing generated method and parameters;
// neither the method signature nor the released parameter layout changes.
// Offset and cursor are mutually exclusive. Apply this editor only to an
// operation whose canonical OpenAPI contract declares cursor support.
func WithPaginationCursor(cursor string) RequestEditorFn {
	return func(ctx context.Context, r *http.Request) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(cursor) > 4096 {
			return errors.New("pagination cursor exceeds the supported bound")
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return errors.New("pagination query is malformed")
		}
		for key, values := range query {
			if key == "" || len(values) != 1 {
				return errors.New("pagination query parameters must each have one value")
			}
		}
		if query.Has("offset") || query.Has("cursor") {
			return errors.New("pagination cursor must be one value without offset")
		}
		query.Set("cursor", cursor)
		r.URL.RawQuery = query.Encode()
		return nil
	}
}
