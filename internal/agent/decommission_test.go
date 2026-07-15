package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	tManagedBy = "app.kubernetes.io/managed-by"
	tPartOf    = "app.kubernetes.io/part-of"
)

func managedNS(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{tPartOf: "astronomer", tManagedBy: "astronomer-server"},
	}}
}

func partOfMeta(name, ns string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{tPartOf: "astronomer"}}
}

// seedFullFootprint returns a fake clientset populated with the complete
// astronomer-managed footprint exactly as deploy/agent/install.yaml.template
// stamps it.
func seedFullFootprint() *fake.Clientset {
	objs := []runtime.Object{
		managedNS("astronomer-system"),
		managedNS("astronomer-monitoring"),
		managedNS("astronomer-trivy-system"),
		managedNS("astronomer-logging"),
		managedNS("astronomer-ingress-nginx"),
		managedNS("astronomer-cert-manager"),
		managedNS("astronomer-gatekeeper-system"),
		&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-agent", Labels: map[string]string{tPartOf: "astronomer"}}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-agent", Labels: map[string]string{tPartOf: "astronomer"}}},
		&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-kube-state-metrics", Labels: map[string]string{tPartOf: "astronomer", tManagedBy: "astronomer-server"}}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-kube-state-metrics", Labels: map[string]string{tPartOf: "astronomer", tManagedBy: "astronomer-server"}}},
		&rbacv1.Role{ObjectMeta: partOfMeta("astronomer-agent-identity", "astronomer-system")},
		&rbacv1.RoleBinding{ObjectMeta: partOfMeta("astronomer-agent-identity", "astronomer-system")},
		&rbacv1.Role{ObjectMeta: partOfMeta("astronomer-agent-token", "astronomer-system")},
		&rbacv1.RoleBinding{ObjectMeta: partOfMeta("astronomer-agent-token", "astronomer-system")},
		&corev1.Secret{ObjectMeta: partOfMeta("astronomer-agent-registration-token", "astronomer-system")},
		&corev1.Secret{ObjectMeta: partOfMeta("astronomer-agent-identity", "astronomer-system")},
		&corev1.Secret{ObjectMeta: partOfMeta("astronomer-agent-token", "astronomer-system")},
		&corev1.Secret{ObjectMeta: partOfMeta("astronomer-agent-ca", "astronomer-system")},
		&corev1.ConfigMap{ObjectMeta: partOfMeta("astronomer-agent-config", "astronomer-system")},
		&corev1.Service{ObjectMeta: partOfMeta("astronomer-agent", "astronomer-system")},
		&networkingv1.NetworkPolicy{ObjectMeta: partOfMeta("astronomer-agent", "astronomer-system")},
		&policyv1.PodDisruptionBudget{ObjectMeta: partOfMeta("astronomer-agent", "astronomer-system")},
		&corev1.ServiceAccount{ObjectMeta: partOfMeta("astronomer-agent", "astronomer-system")},
		&appsv1.Deployment{ObjectMeta: partOfMeta("astronomer-agent", "astronomer-system")},
	}
	return fake.NewClientset(objs...)
}

func veleroListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		{Group: "velero.io", Version: "v1", Resource: "backups"}:                "BackupList",
		{Group: "velero.io", Version: "v1", Resource: "schedules"}:              "ScheduleList",
		{Group: "velero.io", Version: "v1", Resource: "restores"}:               "RestoreList",
		{Group: "velero.io", Version: "v1", Resource: "backupstoragelocations"}: "BackupStorageLocationList",
	}
}

func veleroCR(kind, name string, labels map[string]string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v1",
		"kind":       kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": "velero",
			"labels":    toAnyMap(labels),
		},
	}}
}

func toAnyMap(m map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func newHandler(t *testing.T, cs *fake.Clientset, dyn dynamic.Interface) *DecommissionHandler {
	t.Helper()
	return &DecommissionHandler{
		clientset:        cs,
		dynamicClient:    dyn,
		log:              slog.Default(),
		agentDeleteDelay: 5 * time.Millisecond,
	}
}

func fullFootprintPayload() protocol.DecommissionPayload {
	return protocol.DecommissionPayload{
		ClusterID:             "cid",
		RemoveVeleroManaged:   true,
		RemoveAgentDeployment: true,
		RemoveFullFootprint:   true,
		VeleroLabel:           defaultVeleroSelector,
		ManagedByLabel:        defaultManagedBySelector,
		RBACLabel:             defaultRBACSelector,
	}
}

func runDecommission(t *testing.T, h *DecommissionHandler, req protocol.DecommissionPayload) protocol.DecommissionAckPayload {
	t.Helper()
	body, _ := json.Marshal(req)
	resp, err := h.HandleDecommission(context.Background(), &protocol.Message{Type: protocol.MsgDecommission, Payload: body})
	if err != nil {
		t.Fatalf("HandleDecommission: %v", err)
	}
	var ack protocol.DecommissionAckPayload
	if err := json.Unmarshal(resp.Payload, &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	// Let the deferred Deployment + astronomer-system teardown run.
	time.Sleep(80 * time.Millisecond)
	return ack
}

// deleteActions returns the ordered (resource, name) of every delete the fake
// clientset observed.
func deleteActions(cs *fake.Clientset) []string {
	out := []string{}
	for _, a := range cs.Actions() {
		if a.GetVerb() != "delete" {
			continue
		}
		da, ok := a.(k8stesting.DeleteActionImpl)
		if !ok {
			continue
		}
		out = append(out, a.GetResource().Resource+"/"+da.GetName())
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// TestFullCleanup_DeletesExactlyManagedSet is the load-bearing positive test:
// the entire managed footprint is deleted, astronomer-system is deleted LAST,
// and velero CRs are removed while BSLs are reported as orphans (not deleted).
// TestHasLabel_FailsClosedOnEmptyKey locks the over-deletion landmine fix: a
// malformed/empty selector (splitSelector returns "","") must NEVER match — else
// labels[""]=="" is true for every object and every hardcoded-name target would
// be deleted unguarded.
func TestHasLabel_FailsClosedOnEmptyKey(t *testing.T) {
	if hasLabel(map[string]string{"a": "b"}, "", "") {
		t.Fatal("empty-key selector must NOT match (must fail closed)")
	}
	if hasLabel(nil, "", "") {
		t.Fatal("empty-key selector on nil labels must NOT match")
	}
	if hasLabel(map[string]string{"app.kubernetes.io/part-of": "astronomer"}, "", "") {
		t.Fatal("empty-key selector must NOT match even a labeled object")
	}
	if !hasLabel(map[string]string{"app.kubernetes.io/part-of": "astronomer"}, "app.kubernetes.io/part-of", "astronomer") {
		t.Fatal("a valid matching selector must match")
	}
	if hasLabel(map[string]string{"app.kubernetes.io/part-of": "other"}, "app.kubernetes.io/part-of", "astronomer") {
		t.Fatal("a non-matching value must not match")
	}
}

func TestFullCleanup_DeletesExactlyManagedSet(t *testing.T) {
	cs := seedFullFootprint()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), veleroListKinds(),
		veleroCR("Backup", "nightly", map[string]string{tManagedBy: "astronomer-go"}),
		veleroCR("BackupStorageLocation", "default-bsl", map[string]string{tManagedBy: "astronomer-go"}),
	)
	h := newHandler(t, cs, dyn)

	ack := runDecommission(t, h, fullFootprintPayload())

	dels := deleteActions(cs)
	want := []string{
		"namespaces/astronomer-monitoring", "namespaces/astronomer-trivy-system",
		"namespaces/astronomer-logging", "namespaces/astronomer-ingress-nginx",
		"namespaces/astronomer-cert-manager", "namespaces/astronomer-gatekeeper-system",
		"clusterroles/astronomer-kube-state-metrics", "clusterrolebindings/astronomer-kube-state-metrics",
		"clusterroles/astronomer-agent", "clusterrolebindings/astronomer-agent",
		"secrets/astronomer-agent-identity", "secrets/astronomer-agent-registration-token",
		"secrets/astronomer-agent-token", "secrets/astronomer-agent-ca",
		"configmaps/astronomer-agent-config", "services/astronomer-agent",
		"networkpolicies/astronomer-agent", "poddisruptionbudgets/astronomer-agent",
		"rolebindings/astronomer-agent-identity", "rolebindings/astronomer-agent-token",
		"roles/astronomer-agent-identity", "roles/astronomer-agent-token",
		"deployments/astronomer-agent",
		"namespaces/astronomer-system",
		// NOTE: serviceaccounts/astronomer-agent is intentionally NOT in this set
		// — it is the agent's own local-API identity, so it is left to cascade
		// with the astronomer-system namespace delete (deleting it synchronously
		// would revoke the agent mid-teardown). See removeAgentSingletons.
	}
	if contains(dels, "serviceaccounts/astronomer-agent") {
		t.Errorf("agent ServiceAccount must NOT be deleted synchronously (it cascades with the namespace); got %v", dels)
	}
	for _, w := range want {
		if !contains(dels, w) {
			t.Errorf("expected delete of %s, not found in %v", w, dels)
		}
	}
	// Teardown order: the agent's own Deployment, then astronomer-system (which
	// terminates the pod), then the agent's OWN cluster RBAC LAST — the
	// astronomer-agent ClusterRole/Binding grant the namespace-delete permission,
	// so they must outlive the namespace delete (deleting them sooner self-revokes
	// it). The pod's termination grace window lets the deferred goroutine finish.
	idxDep := indexOf(dels, "deployments/astronomer-agent")
	idxSys := indexOf(dels, "namespaces/astronomer-system")
	idxCRB := indexOf(dels, "clusterrolebindings/astronomer-agent")
	if idxDep < 0 || idxSys < 0 || idxCRB < 0 || !(idxDep < idxSys && idxSys < idxCRB) {
		t.Errorf("expected order deployment(%d) < astronomer-system(%d) < agent-clusterrolebinding(%d): %v", idxDep, idxSys, idxCRB, dels)
	}
	// Every credential Secret is deleted before the agent Deployment, with the
	// active durable identity first.
	idxDep = indexOf(dels, "deployments/astronomer-agent")
	for _, credential := range []string{
		"secrets/astronomer-agent-identity",
		"secrets/astronomer-agent-registration-token",
		"secrets/astronomer-agent-token",
	} {
		if idxCredential := indexOf(dels, credential); idxCredential < 0 || idxCredential > idxDep {
			t.Errorf("credential Secret %s must be removed before the agent Deployment: %v", credential, dels)
		}
	}
	if idxIdentity, idxBootstrap := indexOf(dels, "secrets/astronomer-agent-identity"), indexOf(dels, "secrets/astronomer-agent-registration-token"); idxIdentity > idxBootstrap {
		t.Errorf("active identity must be removed before bootstrap material: %v", dels)
	}

	// Velero: Backup deleted, BSL listed as orphan (NOT deleted).
	if _, err := dyn.Resource(veleroGVRs[0]).Namespace("velero").Get(context.Background(), "nightly", metav1.GetOptions{}); err == nil {
		t.Errorf("expected velero Backup to be deleted")
	}
	if _, err := dyn.Resource(veleroBSLGVR).Namespace("velero").Get(context.Background(), "default-bsl", metav1.GetOptions{}); err != nil {
		t.Errorf("BSL must NOT be deleted (orphan-only), got err=%v", err)
	}
	veleroStep := stepByName(ack, "remove_velero_managed")
	if veleroStep == nil || len(veleroStep.Orphans) != 1 || veleroStep.Orphans[0] != "default-bsl" {
		t.Errorf("expected BSL orphan reported, got %+v", veleroStep)
	}
	// logging-stack subsumed → reported Skipped.
	if ls := stepByName(ack, "remove_logging_stack"); ls == nil || !ls.Skipped {
		t.Errorf("remove_logging_stack should be Skipped under full footprint, got %+v", ls)
	}
}

// TestFullCleanup_NeverDeletesUnmanaged is the airtight over-deletion guard:
// resources lacking the managed labels are NEVER deleted, and kube-system is
// never even touched.
func TestFullCleanup_NeverDeletesUnmanaged(t *testing.T) {
	cs := fake.NewClientset(
		// astronomer-logging WITHOUT the managed-by label (operator-precreated).
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-logging", Labels: map[string]string{tPartOf: "astronomer"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
		// astronomer-agent ClusterRole WITHOUT part-of.
		&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-agent"}},
		// current credential Role WITHOUT part-of.
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-agent-identity", Namespace: "astronomer-system"}},
		// active identity Secret WITHOUT part-of.
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-agent-identity", Namespace: "astronomer-system"}},
		// astronomer-system WITHOUT managed-by (operator-precreated).
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-system", Labels: map[string]string{tPartOf: "astronomer"}}},
	)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), veleroListKinds())
	h := newHandler(t, cs, dyn)

	ack := runDecommission(t, h, fullFootprintPayload())

	dels := deleteActions(cs)
	for _, forbidden := range []string{
		"namespaces/astronomer-logging",
		"namespaces/kube-system",
		"namespaces/astronomer-system",
		"clusterroles/astronomer-agent",
		"roles/astronomer-agent-identity",
		"secrets/astronomer-agent-identity",
	} {
		if contains(dels, forbidden) {
			t.Errorf("OVER-DELETION: %s was deleted despite missing managed label; actions=%v", forbidden, dels)
		}
	}
	// The guard fired → the step that targets a seeded unmanaged resource notes
	// the label guard. The unmanaged identity Secret is handled by
	// remove_agent_singletons (the unmanaged astronomer-agent ClusterRole is
	// guarded in the deferred self-delete path, which can't add an ACK step; its
	// safety is proven by the not-deleted assertions above).
	singStep := stepByName(ack, "remove_agent_singletons")
	if singStep == nil || singStep.Error == "" {
		t.Errorf("expected remove_agent_singletons step to note the label guard, got %+v", singStep)
	}
}

// TestFullCleanup_IdempotentReRun: everything already absent → all steps
// succeed (NotFound==success), no error.
func TestFullCleanup_IdempotentReRun(t *testing.T) {
	cs := fake.NewClientset() // empty cluster
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), veleroListKinds())
	h := newHandler(t, cs, dyn)

	ack := runDecommission(t, h, fullFootprintPayload())
	for _, s := range ack.Steps {
		if s.Error != "" {
			t.Errorf("step %s reported error on empty cluster: %s", s.Name, s.Error)
		}
	}
	if contains(deleteActions(cs), "namespaces/astronomer-system") {
		t.Errorf("nothing should be deleted on an empty cluster")
	}
}

// TestFullCleanup_ForbiddenProfile: a non-admin profile gets 403 on namespace +
// ClusterRole deletes but can still delete credential Secrets and its own
// Deployment. Cleanup must not panic; the ACK is still produced.
func TestFullCleanup_ForbiddenProfile(t *testing.T) {
	cs := seedFullFootprint()
	forbid := func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrorsNewForbidden(action.GetResource().Resource)
	}
	cs.PrependReactor("delete", "namespaces", forbid)
	cs.PrependReactor("delete", "clusterroles", forbid)
	cs.PrependReactor("delete", "clusterrolebindings", forbid)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), veleroListKinds())
	h := newHandler(t, cs, dyn)

	ack := runDecommission(t, h, fullFootprintPayload())

	// Active identity + bootstrap + legacy Secrets and Deployment still removed.
	for _, credential := range []string{"astronomer-agent-identity", "astronomer-agent-registration-token", "astronomer-agent-token"} {
		if _, err := cs.CoreV1().Secrets("astronomer-system").Get(context.Background(), credential, metav1.GetOptions{}); err == nil {
			t.Errorf("credential Secret %s should have been deleted even on a forbidden profile", credential)
		}
	}
	if _, err := cs.AppsV1().Deployments("astronomer-system").Get(context.Background(), "astronomer-agent", metav1.GetOptions{}); err == nil {
		t.Errorf("agent Deployment should have been deleted even on a forbidden profile")
	}
	// Namespace + RBAC steps captured the forbidden outcome, not fatal.
	nsStep := stepByName(ack, "remove_baseline_namespaces")
	if nsStep == nil || nsStep.Error == "" {
		t.Errorf("expected baseline_namespaces step to capture forbidden, got %+v", nsStep)
	}
}

// TestLegacyPayload_BackCompat: a payload WITHOUT RemoveFullFootprint runs only
// the legacy three steps (logging + velero + agent deployment) — an old agent's
// behavior, no full-footprint deletes.
func TestLegacyPayload_BackCompat(t *testing.T) {
	cs := seedFullFootprint()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), veleroListKinds())
	h := newHandler(t, cs, dyn)

	ack := runDecommission(t, h, protocol.DecommissionPayload{
		ClusterID:             "cid",
		RemoveLoggingStack:    true,
		RemoveVeleroManaged:   true,
		RemoveAgentDeployment: true,
		// RemoveFullFootprint deliberately absent.
	})

	dels := deleteActions(cs)
	// Only the logging namespace + agent deployment (legacy). NO baseline ns,
	// NO cluster RBAC, NO singletons, NO astronomer-system.
	for _, forbidden := range []string{
		"namespaces/astronomer-monitoring",
		"namespaces/astronomer-system",
		"clusterroles/astronomer-agent",
		"roles/astronomer-agent-identity",
		"rolebindings/astronomer-agent-identity",
		"roles/astronomer-agent-token",
		"rolebindings/astronomer-agent-token",
		"secrets/astronomer-agent-identity",
		"secrets/astronomer-agent-registration-token",
		"secrets/astronomer-agent-token",
	} {
		if contains(dels, forbidden) {
			t.Errorf("legacy payload must NOT delete %s; actions=%v", forbidden, dels)
		}
	}
	if !contains(dels, "namespaces/astronomer-logging") {
		t.Errorf("legacy payload should still delete astronomer-logging; actions=%v", dels)
	}
	if stepByName(ack, "remove_baseline_namespaces") != nil {
		t.Errorf("legacy payload must not emit full-footprint steps")
	}
}

// TestVeleroLabelSelector: only managed-by=astronomer-go CRs are deleted; a CR
// labeled with the legacy astronomer.io/managed=true is NOT touched.
func TestVeleroLabelSelector(t *testing.T) {
	cs := fake.NewClientset()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), veleroListKinds(),
		veleroCR("Backup", "managed", map[string]string{tManagedBy: "astronomer-go"}),
		veleroCR("Backup", "legacy", map[string]string{"astronomer.io/managed": "true"}),
	)
	h := newHandler(t, cs, dyn)

	_ = runDecommission(t, h, fullFootprintPayload())

	if _, err := dyn.Resource(veleroGVRs[0]).Namespace("velero").Get(context.Background(), "managed", metav1.GetOptions{}); err == nil {
		t.Errorf("managed-by=astronomer-go Backup should be deleted")
	}
	if _, err := dyn.Resource(veleroGVRs[0]).Namespace("velero").Get(context.Background(), "legacy", metav1.GetOptions{}); err != nil {
		t.Errorf("legacy-labeled Backup must NOT be deleted, got err=%v", err)
	}
}

// TestPayloadRoundTrip asserts the additive fields survive a marshal/unmarshal
// and that a legacy payload decodes with RemoveFullFootprint=false.
func TestPayloadRoundTrip(t *testing.T) {
	in := protocol.DecommissionPayload{
		ClusterID:           "cid",
		RemoveFullFootprint: true,
		VeleroLabel:         "a=b",
		ManagedByLabel:      "c=d",
		RBACLabel:           "e=f",
	}
	body, _ := json.Marshal(in)
	var out protocol.DecommissionPayload
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch: %+v vs %+v", out, in)
	}
	// Legacy payload (no new fields) → defaults.
	var legacy protocol.DecommissionPayload
	if err := json.Unmarshal([]byte(`{"cluster_id":"x","remove_logging_stack":true}`), &legacy); err != nil {
		t.Fatalf("legacy decode: %v", err)
	}
	if legacy.RemoveFullFootprint {
		t.Errorf("legacy payload should decode RemoveFullFootprint=false")
	}
}

// --- helpers ---------------------------------------------------------------

func stepByName(ack protocol.DecommissionAckPayload, name string) *protocol.DecommissionStepResult {
	for i := range ack.Steps {
		if ack.Steps[i].Name == name {
			return &ack.Steps[i]
		}
	}
	return nil
}

func apierrorsNewForbidden(resource string) error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "", nil)
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
