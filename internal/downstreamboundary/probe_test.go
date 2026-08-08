package downstreamboundary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestBoundaryEnumsAreCompleteBoundedAndInstrumented(t *testing.T) {
	entrypoints := KnownEntrypoints()
	if len(entrypoints) != int(entrypointCount) {
		t.Fatalf("known entrypoints=%d want=%d", len(entrypoints), entrypointCount)
	}
	operations := KnownOperations()
	if len(operations) != int(operationCount) {
		t.Fatalf("known operations=%d want=%d", len(operations), operationCount)
	}
	seen := map[string]bool{}
	for _, entrypoint := range entrypoints {
		if name := entrypoint.String(); name == "unknown" || seen[name] {
			t.Fatalf("invalid or duplicate entrypoint label %q", name)
		} else {
			seen[name] = true
		}
	}
	seen = map[string]bool{}
	for _, operation := range operations {
		if name := operation.String(); name == "unknown" || seen[name] {
			t.Fatalf("invalid or duplicate operation label %q", name)
		} else {
			seen[name] = true
		}
	}

	_, file, _, _ := runtime.Caller(0)
	internal := filepath.Dir(filepath.Dir(file))
	required := map[string][]string{
		filepath.Join(internal, "tunnel", "proxy.go"): {
			"RecordContext(r.Context(), downstreamboundary.EntrypointKubernetesProxy, downstreamboundary.OperationKubernetes)",
		},
		filepath.Join(internal, "tunnel", "server.go"): {
			"Record(downstreamboundary.EntrypointTunnelMessage, operation)",
			"Record(downstreamboundary.EntrypointTunnelBroadcast, operation)",
		},
		filepath.Join(internal, "tunnel2", "server.go"): {
			"RecordContext(ctx, downstreamboundary.EntrypointRemoteDialer, downstreamboundary.OperationKubernetes)",
		},
	}
	for path, markers := range required {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range markers {
			if !strings.Contains(string(source), marker) {
				t.Errorf("downstream boundary entrypoint %s lacks %s", path, marker)
			}
		}
	}

	// Pin every direct legacy send-channel write. Downstream requests must pass
	// through instrumented SendToAgent/BroadcastToAll; the sole handler.go
	// exception is the response-only APISERVER_AUDIT_ACK to its originating
	// connection. A new bypass makes this contract fail until it is classified
	// and instrumented explicitly.
	allowedDirectSends := map[string]int{"handler.go": 1, "server.go": 2}
	tunnelFiles, err := filepath.Glob(filepath.Join(internal, "tunnel", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range tunnelFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		count := strings.Count(string(source), "sendCh <-")
		if count != allowedDirectSends[filepath.Base(path)] {
			t.Errorf("unclassified direct downstream send in %s: got=%d want=%d", path, count, allowedDirectSends[filepath.Base(path)])
		}
	}
}

func TestCharlieAttributionIsTrustedAndIndependentFromFleetTraffic(t *testing.T) {
	allBefore := TakeSnapshot()
	charlieBefore := TakeCharlieSnapshot()
	RecordContext(context.Background(), EntrypointKubernetesProxy, OperationKubernetes)
	if delta := TakeSnapshot().DeltaTotal(allBefore); delta != 1 {
		t.Fatalf("fleet boundary delta=%d want=1", delta)
	}
	if delta := TakeCharlieSnapshot().DeltaTotal(charlieBefore); delta != 0 {
		t.Fatalf("unmarked boundary was attributed to Charlie: %d", delta)
	}

	allBefore = TakeSnapshot()
	charlieBefore = TakeCharlieSnapshot()
	RecordContext(WithCharlieOrigin(context.Background()), EntrypointRemoteDialer, OperationKubernetes)
	if delta := TakeSnapshot().DeltaTotal(allBefore); delta != 1 {
		t.Fatalf("fleet boundary delta=%d want=1", delta)
	}
	if delta := TakeCharlieSnapshot().DeltaTotal(charlieBefore); delta != 1 {
		t.Fatalf("Charlie boundary delta=%d want=1", delta)
	}
}

func TestCharlieOriginCannotBeForgedByRequestHeader(t *testing.T) {
	before := TakeCharlieSnapshot()
	plain := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		RecordContext(r.Context(), EntrypointKubernetesProxy, OperationKubernetes)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Charlie-Origin", "true")
	plain.ServeHTTP(httptest.NewRecorder(), request)
	if delta := TakeCharlieSnapshot().DeltaTotal(before); delta != 0 {
		t.Fatalf("request header forged Charlie attribution: %d", delta)
	}

	before = TakeCharlieSnapshot()
	MarkCharlieOrigin(plain).ServeHTTP(httptest.NewRecorder(), request)
	if delta := TakeCharlieSnapshot().DeltaTotal(before); delta != 1 {
		t.Fatalf("trusted Charlie middleware delta=%d want=1", delta)
	}
}

func TestSnapshotCountsOnlyFixedValidDimensions(t *testing.T) {
	before := TakeSnapshot()
	metric := boundaryCalls.WithLabelValues(EntrypointTunnelMessage.String(), OperationExec.String())
	metricBefore := testutil.ToFloat64(metric)
	Record(EntrypointTunnelMessage, OperationExec)
	Record(entrypointCount, OperationExec)
	Record(EntrypointTunnelMessage, operationCount)
	if delta := TakeSnapshot().DeltaTotal(before); delta != 1 {
		t.Fatalf("boundary delta=%d want=1", delta)
	}
	if delta := testutil.ToFloat64(metric) - metricBefore; delta != 1 {
		t.Fatalf("Prometheus boundary delta=%v want=1", delta)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	foundCharlie := false
	for _, family := range families {
		if family.GetName() == "astronomer_downstream_boundary_calls_total" ||
			family.GetName() == "astronomer_charlie_downstream_boundary_calls_total" {
			found = true
			if family.GetName() == "astronomer_charlie_downstream_boundary_calls_total" {
				foundCharlie = true
				if got, want := len(family.Metric), int(entrypointCount)*int(operationCount); got != want {
					t.Fatalf("Charlie boundary series=%d want=%d", got, want)
				}
			}
			for _, sample := range family.Metric {
				for _, label := range sample.Label {
					if label.GetName() != "entrypoint" && label.GetName() != "operation" {
						t.Fatalf("unbounded downstream metric label %q", label.GetName())
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("downstream boundary metric is not registered")
	}
	if !foundCharlie {
		t.Fatal("Charlie-attributed downstream boundary metric is not registered")
	}
}
