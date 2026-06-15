package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestNewHTTPRequestEventCopiesRequestMetadata(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/123", nil)
	req.Header.Set("User-Agent", "astronomer-test")

	event := NewHTTPRequestEvent(HTTPRequestEvent{
		Request:       req,
		Source:        "service",
		CorrelationID: "corr-1",
		Action:        "cluster.create",
		ResourceType:  "cluster",
		ResourceID:    "123",
		StatusCode:    http.StatusAccepted,
		DurationMs:    42,
		RequestID:     "req-1",
	})

	if event.HTTPMethod != http.MethodPost || event.Path != "/api/v1/clusters/123" || event.UserAgent != "astronomer-test" {
		t.Fatalf("request metadata not copied: %#v", event)
	}
	if event.Source != "service" || event.CorrelationID != "corr-1" || event.StatusCode != http.StatusAccepted || event.DurationMs != 42 {
		t.Fatalf("explicit metadata not copied: %#v", event)
	}
}

func TestUserIDFromUUID(t *testing.T) {
	if UserIDFromUUID(uuid.Nil).Valid {
		t.Fatal("nil UUID should produce a null pgtype UUID")
	}
	id := uuid.New()
	got := UserIDFromUUID(id)
	if !got.Valid || got.Bytes != id {
		t.Fatalf("unexpected user id: %#v", got)
	}
}
