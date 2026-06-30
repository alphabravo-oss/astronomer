package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestRBACSyncOutOfBounds is the H5 safety guardrail: the RBAC syncer must
// REFUSE any cluster-scoped RBAC and any namespaced RBAC outside the
// astronomer-owned namespaces, and ALLOW namespaced RBAC within them.
func TestRBACSyncOutOfBounds(t *testing.T) {
	role := func(ns, name string) json.RawMessage {
		return mustRaw(t, rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}})
	}
	rb := func(ns, name string) json.RawMessage {
		return mustRaw(t, rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}})
	}
	crb := func(name string) json.RawMessage {
		return mustRaw(t, rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}

	cases := []struct {
		name    string
		payload protocol.RBACSyncRequestPayload
		refused bool
	}{
		{"cluster-role-binding refused", protocol.RBACSyncRequestPayload{ClusterRoleBindings: []json.RawMessage{crb("rogue-admin")}}, true},
		{"cluster-role refused", protocol.RBACSyncRequestPayload{ClusterRoles: []json.RawMessage{mustRaw(t, rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "x"}})}}, true},
		{"role in kube-system refused", protocol.RBACSyncRequestPayload{Roles: []json.RawMessage{role("kube-system", "x")}}, true},
		{"rolebinding in default refused", protocol.RBACSyncRequestPayload{RoleBindings: []json.RawMessage{rb("default", "x")}}, true},
		{"role in owned ns allowed", protocol.RBACSyncRequestPayload{Roles: []json.RawMessage{role("astronomer-monitoring", "ok")}}, false},
		{"rolebinding in owned ns allowed", protocol.RBACSyncRequestPayload{RoleBindings: []json.RawMessage{rb("astronomer-system", "ok")}}, false},
		{"empty allowed", protocol.RBACSyncRequestPayload{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason := rbacSyncOutOfBounds(c.payload)
			if c.refused && reason == "" {
				t.Fatalf("expected REFUSAL, got allowed")
			}
			if !c.refused && reason != "" {
				t.Fatalf("expected allowed, got refused: %s", reason)
			}
		})
	}
}

// TestRBACSyncRefusalShortCircuits proves the refusal happens BEFORE any k8s
// client call: a syncer with a nil client refuses a cluster-scoped payload and
// reports the error, without panicking on the (unused) client.
func TestRBACSyncRefusalShortCircuits(t *testing.T) {
	s := &RBACSyncer{client: nil, log: testLogger()}
	var got *protocol.RBACSyncResultPayload
	sendFn := func(m *protocol.Message) error {
		var r protocol.RBACSyncResultPayload
		if err := json.Unmarshal(m.Payload, &r); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		got = &r
		return nil
	}
	payload := protocol.RBACSyncRequestPayload{
		ClusterRoleBindings: []json.RawMessage{mustRaw(t, rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "rogue-admin"}})},
	}
	msg := &protocol.Message{RequestID: "req-1", Payload: mustRaw(t, payload)}
	if err := s.HandleSyncRequest(context.Background(), msg, sendFn); err != nil {
		t.Fatalf("HandleSyncRequest returned error: %v", err)
	}
	if got == nil || got.Applied != 0 || len(got.Errors) == 0 || !strings.Contains(got.Errors[0], "refused") {
		t.Fatalf("expected a refusal result with 0 applied, got %+v", got)
	}
}
