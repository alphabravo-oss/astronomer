package charliequalification

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeIsolationRunner struct {
	mu      sync.Mutex
	outputs [][]byte
	err     error
}

func (r *fakeIsolationRunner) Output(context.Context, string, ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	if len(r.outputs) == 0 {
		return nil, errors.New("unexpected fixed collector call")
	}
	result := r.outputs[0]
	r.outputs = r.outputs[1:]
	return result, nil
}

type fakePacketCounter struct {
	counts IsolationPacketCounts
	err    error
}

func (f fakePacketCounter) Count(ctx context.Context, dwell time.Duration, _ []string) (IsolationPacketCounts, error) {
	if err := waitContext(ctx, dwell); err != nil {
		return IsolationPacketCounts{}, err
	}
	return f.counts, f.err
}

func TestColdIsolationRejectsEveryFalseProof(t *testing.T) {
	valid := IsolationObservation{State: IsolationColdFeatureDisabled, Duration: time.Second}
	mutations := []struct {
		name   string
		mutate func(*IsolationObservation)
	}{
		{"wrong state", func(v *IsolationObservation) { v.State = IsolationColdConnectionDisabled }},
		{"zero duration", func(v *IsolationObservation) { v.Duration = 0 }},
		{"process", func(v *IsolationObservation) { v.Inventory.Processes = 1 }},
		{"listener", func(v *IsolationObservation) { v.Inventory.Listeners = 1 }},
		{"timer", func(v *IsolationObservation) { v.Inventory.Timers = 1 }},
		{"dns ingress", func(v *IsolationObservation) { v.Packets.DNS.Ingress = 1 }},
		{"dns egress", func(v *IsolationObservation) { v.Packets.DNS.Egress = 1 }},
		{"tcp ingress", func(v *IsolationObservation) { v.Packets.TCP.Ingress = 1 }},
		{"tcp egress", func(v *IsolationObservation) { v.Packets.TCP.Egress = 1 }},
		{"udp ingress", func(v *IsolationObservation) { v.Packets.UDP.Ingress = 1 }},
		{"udp egress", func(v *IsolationObservation) { v.Packets.UDP.Egress = 1 }},
	}
	if !coldIsolationProved(valid, IsolationColdFeatureDisabled) {
		t.Fatal("valid content-free cold observation was rejected")
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if coldIsolationProved(value, IsolationColdFeatureDisabled) {
				t.Fatal("false cold-isolation proof was accepted")
			}
		})
	}
}

func TestControlProtocolOnlyRejectsRuntimeWorkAndUnverifiedTraffic(t *testing.T) {
	valid := IsolationObservation{State: IsolationOperationalWireDisabled, Duration: time.Second}
	valid.Runtime.HeartbeatAttempts = 1
	valid.Runtime.HeartbeatSuccesses = 1
	valid.Downstream.CentralControl.ConnectionAttempts = 1
	valid.Downstream.CentralControl.Requests = 1
	valid.Downstream.CentralControl.Responses = 1
	valid.Control.VerifiedSignedHeartbeat = 1
	if !controlProtocolOnly(valid) {
		t.Fatal("verified signed heartbeat-only observation was rejected")
	}
	mutations := []func(*IsolationObservation){
		func(v *IsolationObservation) { v.Runtime.WorkClaims = 1 },
		func(v *IsolationObservation) { v.Runtime.SessionsStarted = 1 },
		func(v *IsolationObservation) { v.Runtime.ModelRequests = 1 },
		func(v *IsolationObservation) { v.Runtime.CapabilityCalls = 1 },
		func(v *IsolationObservation) { v.Downstream.CentralWork.Requests = 1 },
		func(v *IsolationObservation) { v.Downstream.ProductMCP.ConnectionAttempts = 1 },
		func(v *IsolationObservation) { v.Control.RejectedAuth = 1 },
		func(v *IsolationObservation) { v.Control.NonControl = 1 },
		func(v *IsolationObservation) { v.Runtime.HeartbeatAttempts = 2 },
		func(v *IsolationObservation) { v.Downstream.CentralControl.Requests = 2 },
	}
	for index, mutate := range mutations {
		value := valid
		mutate(&value)
		if controlProtocolOnly(value) {
			t.Fatalf("false operational proof %d was accepted", index)
		}
	}
}

func TestOperationalObserverRejectsDelayedRuntimeWork(t *testing.T) {
	for _, workDelta := range []uint64{0, 1} {
		t.Run(fmt.Sprintf("work_%d", workDelta), func(t *testing.T) {
			var mu sync.Mutex
			scrapes := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				scrapes++
				values := make(map[string]uint64, len(isolationMetricNames))
				if scrapes > 1 {
					values["charlie_agent_heartbeat_attempts_total"] = 1
					values["charlie_agent_heartbeat_successes_total"] = 1
					values["charlie_agent_control_egress_verified_signed_heartbeat_total"] = 1
					values["charlie_agent_downstream_central_control_connection_attempts_total"] = 1
					values["charlie_agent_downstream_central_control_requests_total"] = 1
					values["charlie_agent_downstream_central_control_responses_total"] = 1
					values["charlie_agent_work_claims_total"] = workDelta
				}
				for name := range isolationMetricNames {
					_, _ = fmt.Fprintf(w, "%s %d\n", name, values[name])
				}
			}))
			defer server.Close()
			endpoint, err := safeOperatorURL(server.URL, false)
			if err != nil {
				t.Fatal(err)
			}
			observer := &kubectlTCPDumpIsolationObserver{metricSources: []metricEndpoint{{url: endpoint}}, client: server.Client()}
			prepared, err := observer.Prepare(t.Context(), IsolationOperationalWireDisabled)
			if err != nil {
				t.Fatal(err)
			}
			observation, err := prepared.Observe(t.Context(), time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if got := controlProtocolOnly(observation); got != (workDelta == 0) {
				t.Fatalf("control-only verdict = %t for delayed work delta %d: %#v", got, workDelta, observation)
			}
		})
	}
}

func TestOperationalObserverRejectsLabeledOrMalformedFixedMetric(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for name := range isolationMetricNames {
			if name == "charlie_agent_work_claims_total" {
				_, _ = fmt.Fprintf(w, "%s{arbitrary=%q} 0\n", name, "forbidden")
				continue
			}
			_, _ = fmt.Fprintf(w, "%s 0\n", name)
		}
	}))
	defer server.Close()
	endpoint, err := safeOperatorURL(server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	observer := &kubectlTCPDumpIsolationObserver{metricSources: []metricEndpoint{{url: endpoint}}, client: server.Client()}
	prepared, err := observer.Prepare(t.Context(), IsolationOperationalWireDisabled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepared.Observe(t.Context(), time.Millisecond); err == nil {
		t.Fatal("labeled fixed isolation metric was accepted")
	}
}

func TestColdObserverSamplesEntireDwellAndRejectsDelayedTraffic(t *testing.T) {
	runner := &fakeIsolationRunner{outputs: [][]byte{
		nil, nil, nil,
		nil, nil, nil,
		nil, nil, nil,
	}}
	observer := &kubectlTCPDumpIsolationObserver{
		runner: runner, kubectl: "kubectl", kubeconfig: "/owner-only", namespace: "astronomer-charlie",
		statefulSet: "charlie-agent", service: "charlie-agent", poll: time.Millisecond,
		packets: fakePacketCounter{counts: IsolationPacketCounts{TCP: DirectionalPacketCounts{Egress: 1}}},
	}
	prepared := &preparedKubectlTCPDumpIsolationObserver{parent: observer, state: IsolationColdFeatureDisabled, targetIPs: []string{"192.0.2.1"}}
	observation, err := prepared.Observe(t.Context(), 2*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if coldIsolationProved(observation, IsolationColdFeatureDisabled) {
		t.Fatal("traffic arriving during the bounded dwell was accepted as zero")
	}
}

func TestInventoryRejectsMalformedFixedKubectlOutput(t *testing.T) {
	for _, malformed := range []string{"0 pod-name", "-1", "1.0", "0 0", strings.Repeat("9", 80)} {
		t.Run(strings.ReplaceAll(malformed, " ", "_"), func(t *testing.T) {
			observer := &kubectlTCPDumpIsolationObserver{
				runner: &fakeIsolationRunner{outputs: [][]byte{[]byte(malformed), nil, nil}}, kubectl: "kubectl", kubeconfig: "/owner-only",
				namespace: "astronomer-charlie", statefulSet: "charlie-agent", service: "charlie-agent",
			}
			if _, err := observer.inventory(t.Context()); err == nil {
				t.Fatal("malformed collector output was accepted")
			}
		})
	}
}

func TestPrepareCapturesOnlyBoundedValidatedAgentTargets(t *testing.T) {
	observer := &kubectlTCPDumpIsolationObserver{
		runner:  &fakeIsolationRunner{outputs: [][]byte{[]byte("1"), []byte("192.0.2.10\n2001:db8::10\n192.0.2.10\n")}},
		kubectl: "kubectl", kubeconfig: "/owner-only", namespace: "astronomer-charlie", release: "astronomer-charlie",
		statefulSet: "charlie-agent", service: "charlie-agent",
	}
	preparedValue, err := observer.Prepare(t.Context(), IsolationColdFeatureDisabled)
	if err != nil {
		t.Fatal(err)
	}
	prepared, ok := preparedValue.(*preparedKubectlTCPDumpIsolationObserver)
	if !ok || !reflect.DeepEqual(prepared.targetIPs, []string{"192.0.2.10", "2001:db8::10"}) {
		t.Fatalf("capture targets were not validated and deduplicated: %#v", preparedValue)
	}

	observer.runner = &fakeIsolationRunner{outputs: [][]byte{[]byte("1"), []byte("192.0.2.10\nnot-an-address\n")}}
	if _, err = observer.Prepare(t.Context(), IsolationColdFeatureDisabled); err == nil {
		t.Fatal("malformed capture target was accepted")
	}
	observer.runner = &fakeIsolationRunner{outputs: [][]byte{[]byte("1"), nil}}
	if _, err = observer.Prepare(t.Context(), IsolationColdFeatureDisabled); err == nil {
		t.Fatal("empty capture target set was accepted")
	}
}

func TestIsolationObserverRejectsUnsafeConfiguration(t *testing.T) {
	directory := t.TempDir()
	kubeconfig := filepath.Join(directory, "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte("bounded"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := IsolationObserverConfig{Kubeconfig: kubeconfig, Namespace: "astronomer-charlie", Release: "astronomer-charlie", StatefulSet: "charlie-agent", Service: "charlie-agent", CaptureInterface: "cni0"}
	for _, mutate := range []func(*IsolationObserverConfig){
		func(v *IsolationObserverConfig) { v.KubectlBinary = "kubectl;id" },
		func(v *IsolationObserverConfig) { v.TCPDumpBinary = "tcpdump --help" },
		func(v *IsolationObserverConfig) { v.Namespace = "../../secret" },
		func(v *IsolationObserverConfig) { v.StatefulSet = "*" },
		func(v *IsolationObserverConfig) { v.CaptureInterface = "any;cat" },
	} {
		value := base
		mutate(&value)
		if _, err := NewKubectlTCPDumpIsolationObserver(value); err == nil {
			t.Fatal("unsafe isolation configuration was accepted")
		}
	}
	if err := os.Chmod(kubeconfig, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKubectlTCPDumpIsolationObserver(base); err == nil {
		t.Fatal("group/world-readable isolation kubeconfig was accepted")
	}
}

func TestZeroRuntimeAssertionNamesExactlyMatchPinnedCanonicalProfile(t *testing.T) {
	want := map[string][]string{
		"feature_false":      {"state_applied", "process_absent", "listener_absent", "timer_absent", "dns_packets_zero", "tcp_packets_zero", "udp_packets_zero", "runtime_counters_unchanged", "downstream_counters_unchanged"},
		"unactivated":        {"state_applied", "process_absent", "listener_absent", "timer_absent", "dns_packets_zero", "tcp_packets_zero", "udp_packets_zero", "runtime_counters_unchanged", "downstream_counters_unchanged"},
		"central_disabled":   {"state_applied", "control_protocol_only", "runtime_counters_unchanged", "downstream_counters_unchanged"},
		"emergency_disabled": {"state_applied", "control_protocol_only", "runtime_counters_unchanged", "downstream_counters_unchanged"},
	}
	for scenario, assertions := range want {
		if !reflect.DeepEqual(requiredAssertions[scenario], assertions) {
			t.Fatalf("%s assertions drifted from the canonical profile: got=%v want=%v", scenario, requiredAssertions[scenario], assertions)
		}
	}
}
