package events

import (
	"context"
	"testing"
	"time"
)

func TestPublishRemoteMarksRemoteTrue(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx)

	bus.PublishRemote(TypeClusterConnected, map[string]any{"cluster_id": "c1"})

	select {
	case ev := <-ch:
		if !ev.Remote {
			t.Fatal("expected Remote=true for PublishRemote")
		}
		if ev.Type != TypeClusterConnected {
			t.Fatalf("type=%s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for remote event")
	}
}

func TestPublishLocalIsNotRemote(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx)

	bus.Publish(TypeClusterConnected, map[string]any{"cluster_id": "c1"})

	select {
	case ev := <-ch:
		if ev.Remote {
			t.Fatal("local Publish must set Remote=false")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for local event")
	}
}
