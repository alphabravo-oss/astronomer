package agent

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestStateMetricProjectionIncludesMaintainedInformers(t *testing.T) {
	for _, kind := range append(append([]metadataKind{}, metadataInformerKinds...), crdInformerKinds...) {
		if got := observability.StateMetricKind(kind.kind); got != kind.kind {
			t.Fatalf("maintained informer %s projected as %s", kind.kind, got)
		}
	}
	// Read actual core/app/Event/Constraint registrations so adding a new
	// maintained producer requires a corresponding bounded metric projection.
	file, err := parser.ParseFile(token.NewFileSet(), "state_subscriber.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "s" {
			return true
		}
		index := 1
		switch selector.Sel.Name {
		case "attach", "dispatch":
		case "handlers":
			index = 0
		default:
			return true
		}
		if len(call.Args) <= index {
			return true
		}
		literal, ok := call.Args[index].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		kind, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		count++
		if got := observability.StateMetricKind(kind); got != kind {
			t.Errorf("maintained literal producer %s projected as %s", kind, got)
		}
		return true
	})
	if count < 12 {
		t.Fatalf("producer kind discovery unexpectedly found only%d literal callsites", count)
	}
}

func TestStateMetricProjectionPreservesUnknownWireKinds(t *testing.T) {
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() { observability.SetInstanceID(previous) })
	sender := &recordingSender{}
	subscriber := NewStateSubscriber(nil, sender, slog.Default())
	kinds := []string{"SyntheticCustomOne", "SyntheticCustomTwo", "SyntheticCustomThree"}
	for _, kind := range kinds {
		subscriber.dispatch(protocol.StateUpdateOpAdded, kind, "synthetic.example", "v1", &metav1.ObjectMeta{Name: "owned", Namespace: "synthetic"})
	}
	messages := sender.Snapshot()
	if len(messages) != len(kinds) {
		t.Fatalf("messages=%d want%d", len(messages), len(kinds))
	}
	for i, message := range messages {
		var payload protocol.StateUpdatePayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Kind != kinds[i] || payload.CoalesceKey != kinds[i]+"|synthetic|owned" {
			t.Fatal("metric projection changed payload or coalescing key")
		}
	}
	if got := counterValue(t, agentStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues("other")...)); got != 3 {
		t.Fatalf("received other=%v want3", got)
	}
	if got := counterValue(t, agentStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("queued", "other")...)); got != 3 {
		t.Fatalf("queued other=%v want3", got)
	}
}
