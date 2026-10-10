package astroclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// CatalogControllerWindow describes a recent authorized operation window.
// Total is independently observed; it is never inferred from Returned.
type CatalogControllerWindow struct {
	Scope       string `json:"scope"`
	WindowLimit int    `json:"windowLimit"`
	Returned    int    `json:"returned"`
	Total       *int64 `json:"total"`
	Complete    bool   `json:"complete"`
	Notice      string `json:"notice"`
}

// CatalogControllerWindowFromResponse reads the additive contract while leaving
// the released generated response and anonymous JSON200 struct layouts intact.
// A nil window means the server omitted this optional field; it does not mean zero.
func CatalogControllerWindowFromResponse(response *GetCatalogControllerStatusResponse) (*CatalogControllerWindow, error) {
	invalid := errors.New("catalog controller operation window is unavailable or malformed")
	if response == nil || response.StatusCode() != http.StatusOK || len(response.Body) == 0 || len(response.Body) > 2<<20 {
		return nil, invalid
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body, &body); err != nil || body == nil {
		return nil, invalid
	}
	// The actual v1 handler writes RespondJSON's data envelope. Continue
	// accepting the released flat representation in existing compatible callers.
	if envelope, present := body["data"]; present {
		var status map[string]json.RawMessage
		if err := json.Unmarshal(envelope, &status); err != nil || status == nil {
			return nil, invalid
		}
		body = status
	}
	raw, present := body["operationSummary"]
	if !present {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, invalid
	}
	for _, field := range []string{"scope", "windowLimit", "returned", "total", "complete", "notice"} {
		value, ok := fields[field]
		if !ok || (field != "total" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return nil, invalid
		}
	}
	// OpenAPI permits compatible additive fields. Required known fields and
	// their types remain validated; unrelated future fields are ignored.
	var window CatalogControllerWindow
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&window); err != nil {
		return nil, invalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, invalid
	}
	if (window.Scope != "global" && window.Scope != "authorized-clusters") || window.WindowLimit != 1000 || window.Returned < 0 || window.Returned > window.WindowLimit || (window.Total != nil && *window.Total < 0) || len([]rune(window.Notice)) > 512 {
		return nil, invalid
	}
	if window.Complete && (window.Total == nil || *window.Total != int64(window.Returned)) {
		return nil, invalid
	}
	return &window, nil
}
