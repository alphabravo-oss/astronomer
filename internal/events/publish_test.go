package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// changedTypeContract is the envelope contract table for every
// `<resource>.changed` type the backend publishes. Every new PublishChanged
// domain MUST add a row here (P4.5): cluster-scoped payloads missing
// `cluster_id` are dropped fail-closed for restricted users (SEC-R07), so a
// publisher forgetting it silently breaks liveness for exactly the users
// least able to debug it.
var changedTypeContract = []struct {
	resource      string
	want          Type
	clusterScoped bool
}{
	{"backup", TypeBackupChanged, true},
	{"fleet_operation", TypeFleetOperationChanged, true},
	{"logging_operation", TypeLoggingOperationChanged, true},
	{"tool_operation", TypeToolOperationChanged, true},
	{"cis_scan", TypeCISScanChanged, true},
	{"image_scan", TypeImageScanChanged, true},
	{"argocd", TypeArgoCDChanged, true},
	{"admin_queue", TypeAdminQueueChanged, false},
	{"siem_forwarder", TypeSIEMForwarderChanged, false},
	{"agent_fleet", TypeAgentFleetChanged, true},
	{"template_binding", TypeTemplateBindingChanged, true},
	{"registry", TypeRegistryChanged, true},
	{"snapshot", TypeSnapshotChanged, true},
}

func TestPublishChangedEnvelopeContract(t *testing.T) {
	for _, tc := range changedTypeContract {
		t.Run(string(tc.want), func(t *testing.T) {
			if got := Type(tc.resource + ".changed"); got != tc.want {
				t.Fatalf("type constant mismatch: resource %q builds %q, constant is %q", tc.resource, got, tc.want)
			}

			bus := NewBus()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch := bus.Subscribe(ctx)

			clusterID := ""
			if tc.clusterScoped {
				clusterID = "cluster-123"
			}
			PublishChanged(bus, tc.resource, clusterID, "entity-1", map[string]any{"kind": "example"})

			e := receiveEvent(t, ch)
			if e.Type != tc.want {
				t.Fatalf("event type = %q, want %q", e.Type, tc.want)
			}
			payload := payloadMap(t, e)

			if tc.clusterScoped {
				if payload["cluster_id"] != "cluster-123" {
					t.Fatalf("cluster-scoped %q payload cluster_id = %v, want %q (SEC-R07 drops it fail-closed)", tc.want, payload["cluster_id"], "cluster-123")
				}
			} else if _, ok := payload["cluster_id"]; ok {
				t.Fatalf("unscoped %q payload unexpectedly carries cluster_id: %v", tc.want, payload["cluster_id"])
			}
			if payload["id"] != "entity-1" {
				t.Fatalf("payload id = %v, want %q", payload["id"], "entity-1")
			}
			if payload["kind"] != "example" {
				t.Fatalf("extra field kind = %v, want %q", payload["kind"], "example")
			}
		})
	}
}

func TestChangedTypeConstantsAllInContractTable(t *testing.T) {
	// Every `.changed` constant must have a contract row so nobody adds a
	// type without deciding its cluster scoping.
	all := []Type{
		TypeBackupChanged,
		TypeFleetOperationChanged,
		TypeLoggingOperationChanged,
		TypeToolOperationChanged,
		TypeCISScanChanged,
		TypeImageScanChanged,
		TypeArgoCDChanged,
		TypeAdminQueueChanged,
		TypeSIEMForwarderChanged,
		TypeAgentFleetChanged,
		TypeTemplateBindingChanged,
		TypeRegistryChanged,
		TypeSnapshotChanged,
	}
	inTable := make(map[Type]bool, len(changedTypeContract))
	for _, tc := range changedTypeContract {
		inTable[tc.want] = true
	}
	for _, typ := range all {
		if !strings.HasSuffix(string(typ), ".changed") {
			t.Errorf("constant %q does not use the .changed verb", typ)
		}
		if !inTable[typ] {
			t.Errorf("constant %q missing from changedTypeContract table", typ)
		}
	}
	if len(changedTypeContract) != len(all) {
		t.Errorf("contract table has %d rows, constants list has %d", len(changedTypeContract), len(all))
	}
}

func TestPublishChangedExtraCannotOverrideReservedKeys(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx)

	PublishChanged(bus, "backup", "cluster-123", "entity-1", map[string]any{
		"cluster_id": "spoofed",
		"id":         "spoofed",
	})

	payload := payloadMap(t, receiveEvent(t, ch))
	if payload["cluster_id"] != "cluster-123" {
		t.Fatalf("extra overrode cluster_id: %v", payload["cluster_id"])
	}
	if payload["id"] != "entity-1" {
		t.Fatalf("extra overrode id: %v", payload["id"])
	}
}

func TestPublishChangedOmitsEmptyIDs(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx)

	PublishChanged(bus, "admin_queue", "", "", nil)

	e := receiveEvent(t, ch)
	if e.Type != TypeAdminQueueChanged {
		t.Fatalf("event type = %q, want %q", e.Type, TypeAdminQueueChanged)
	}
	payload := payloadMap(t, e)
	if _, ok := payload["cluster_id"]; ok {
		t.Fatalf("empty clusterID should be omitted, got %v", payload["cluster_id"])
	}
	if _, ok := payload["id"]; ok {
		t.Fatalf("empty entityID should be omitted, got %v", payload["id"])
	}
}

func TestPublishChangedNilBusAndEmptyResourceAreNoOps(t *testing.T) {
	// Must not panic: publishers are fire-and-forget and may run before the
	// bus is wired.
	PublishChanged(nil, "backup", "cluster-123", "entity-1", nil)

	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx)
	PublishChanged(bus, "", "cluster-123", "entity-1", nil)
	select {
	case e := <-ch:
		t.Fatalf("empty resource published event %q", e.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

func receiveEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func payloadMap(t *testing.T, e Event) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e.Data)
	if err != nil {
		t.Fatalf("marshal event data: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal event data: %v", err)
	}
	return payload
}
