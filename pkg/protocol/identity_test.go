package protocol

import (
	"encoding/json"
	"testing"
)

// TestCallerIdentityWireShape pins the JSON keys. They are prefixed (caller_*)
// because CallerIdentity is EMBEDDED — its fields flatten into the enclosing
// payload — and generic names like "user" would collide with a future field on
// K8sRequestPayload. A rename here is a wire break between server and agent, so
// it should have to be deliberate.
func TestCallerIdentityWireShape(t *testing.T) {
	raw, err := json.Marshal(K8sRequestPayload{
		Method: "GET",
		Path:   "/api/v1/pods",
		CallerIdentity: CallerIdentity{
			User:      UserSubjectPrefix + "abc",
			Groups:    []string{RoleGroupPrefix + "cluster-operator"},
			RequestID: "req-1",
			Origin:    OriginUser,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"caller_user", "caller_groups", "caller_request_id", "caller_origin"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing wire key %q in %s", key, raw)
		}
	}
}

// TestUnpopulatedIdentityAddsNoWireBytes matters for the machine and internal
// paths that have not been taught to stamp an origin: their payloads must
// serialize exactly as they did before the field existed, so an older agent
// sees byte-identical input.
func TestUnpopulatedIdentityAddsNoWireBytes(t *testing.T) {
	raw, err := json.Marshal(K8sRequestPayload{Method: "GET", Path: "/api/v1/pods"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(raw), `{"method":"GET","path":"/api/v1/pods"}`; got != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
}

// TestIdentityRoundTripsAcrossAllThreePayloads guards the embed on each of the
// three payload types named in design §8 item 1.
func TestIdentityRoundTripsAcrossAllThreePayloads(t *testing.T) {
	want := CallerIdentity{User: UserSubjectPrefix + "abc", Origin: OriginUser, RequestID: "r"}

	k8s := K8sRequestPayload{CallerIdentity: want}
	exec := ExecStartPayload{CallerIdentity: want}
	logs := LogStartPayload{CallerIdentity: want}

	var gotK8s K8sRequestPayload
	var gotExec ExecStartPayload
	var gotLogs LogStartPayload
	roundTrip(t, k8s, &gotK8s)
	roundTrip(t, exec, &gotExec)
	roundTrip(t, logs, &gotLogs)

	for name, got := range map[string]CallerIdentity{
		"k8s":  gotK8s.CallerIdentity,
		"exec": gotExec.CallerIdentity,
		"logs": gotLogs.CallerIdentity,
	} {
		if got.User != want.User || got.Origin != want.Origin || got.RequestID != want.RequestID {
			t.Errorf("%s payload identity = %+v, want %+v", name, got, want)
		}
		if !got.IsUser() || got.IsMachine() {
			t.Errorf("%s payload origin predicates wrong for %+v", name, got)
		}
	}
}

func roundTrip(t *testing.T, in, out any) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
