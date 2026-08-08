package charlie

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/events"
)

func TestEventFindingPublisherEmitsBoundedActionableMetadata(t *testing.T) {
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := bus.Subscribe(ctx)
	publisher := NewEventFindingPublisher(bus)
	alert := FindingAlert{FindingID: "finding-1", Severity: "critical", Status: "open", ResourceType: "tunnel", ResourceID: "replica-a", BlockCode: "read_only", RepeatCount: 2}
	if err := publisher.PublishCharlieFinding(ctx, alert); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream:
		if event.Type != events.TypeCharlieFindingChanged {
			t.Fatalf("event type=%q", event.Type)
		}
		raw, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["id"] != alert.FindingID || payload["deep_link"] != "/dashboard/charlie?tab=findings&finding=finding-1" || payload["block_code"] != "read_only" {
			t.Fatalf("unexpected finding event payload: %#v", payload)
		}
		for _, prohibited := range []string{"prompt", "evidence", "arguments", "argument_digest", "credential", "authorization_ref", "manifest", "signature", "action_request", "request_id", "approval_id", "summary"} {
			if _, exists := payload[prohibited]; exists {
				t.Fatalf("sensitive field %q leaked into event", prohibited)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("finding event was not published")
	}
}
