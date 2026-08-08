package charliequalification

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestKubernetesLeaderFailoverDeletesOnlyObservedUIDAndWaitsForReplacement(t *testing.T) {
	client := fake.NewSimpleClientset(leaderStatefulSet(2), leaderPod(0, "pod-old", true), leaderPod(1, "pod-standby", true))
	deletedName, deletedUID := "", types.UID("")
	client.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deletion := action.(k8stesting.DeleteAction)
		deletedName = deletion.GetName()
		if deletion.GetDeleteOptions().Preconditions != nil && deletion.GetDeleteOptions().Preconditions.UID != nil {
			deletedUID = *deletion.GetDeleteOptions().Preconditions.UID
		}
		if err := client.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "qualification", deletedName); err != nil {
			return true, nil, err
		}
		replacement := leaderPod(0, "pod-new", true)
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), replacement, "qualification"); err != nil {
			return true, nil, err
		}
		return true, nil, nil
	})
	target := &KubernetesLeaderFailoverTarget{client: client, namespace: "qualification", statefulSet: "charlie-agent", poll: time.Millisecond}
	uid, replicas, err := target.Snapshot(t.Context(), 0)
	if err != nil || uid != "pod-old" || replicas != 2 {
		t.Fatalf("snapshot uid=%q replicas=%d err=%v", uid, replicas, err)
	}
	readyAt, err := target.DeleteAndWaitReplacement(t.Context(), 0, uid)
	if err != nil || readyAt.IsZero() || deletedName != "charlie-agent-0" || deletedUID != "pod-old" {
		t.Fatalf("replacement ready=%s name=%q uid=%q err=%v", readyAt, deletedName, deletedUID, err)
	}
	if err := target.WaitReady(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
}

func TestKubernetesLeaderFailoverRejectsUnsafeTargetState(t *testing.T) {
	tests := []struct {
		name    string
		objects []runtime.Object
		ordinal int
	}{
		{name: "not redundant", objects: []runtime.Object{leaderStatefulSet(1), leaderPod(0, "pod-a", true)}, ordinal: 0},
		{name: "wrong owner", objects: []runtime.Object{leaderStatefulSet(2), leaderPodWithOwner(0, "pod-a", true, "other", "other-uid"), leaderPod(1, "pod-b", true)}, ordinal: 0},
		{name: "not ready", objects: []runtime.Object{leaderStatefulSet(2), leaderPod(0, "pod-a", false), leaderPod(1, "pod-b", true)}, ordinal: 0},
		{name: "ordinal outside target", objects: []runtime.Object{leaderStatefulSet(2), leaderPod(0, "pod-a", true), leaderPod(1, "pod-b", true)}, ordinal: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &KubernetesLeaderFailoverTarget{client: fake.NewSimpleClientset(test.objects...), namespace: "qualification", statefulSet: "charlie-agent", poll: time.Millisecond}
			if _, _, err := target.Snapshot(t.Context(), test.ordinal); err == nil {
				t.Fatal("unsafe leader target was accepted")
			}
		})
	}
}

func TestKubernetesLeaderFailoverConstructorRequiresOwnerOnlyFixedTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKubernetesLeaderFailoverTarget(KubernetesLeaderFailoverConfig{Kubeconfig: path, Namespace: "qualification", StatefulSet: "charlie-agent"}); err == nil {
		t.Fatal("group-readable kubeconfig was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKubernetesLeaderFailoverTarget(KubernetesLeaderFailoverConfig{Kubeconfig: path, Namespace: "../other", StatefulSet: "charlie-agent"}); err == nil {
		t.Fatal("unsafe namespace was accepted")
	}
	if _, err := NewKubernetesLeaderFailoverTarget(KubernetesLeaderFailoverConfig{Kubeconfig: path, Namespace: "qualification", StatefulSet: "charlie-agent"}); err == nil {
		t.Fatal("invalid owner-only kubeconfig content was accepted")
	}
	execConfig := "apiVersion: v1\nkind: Config\ncurrent-context: q\nclusters:\n- name: q\n  cluster:\n    server: https://127.0.0.1:6443\n    insecure-skip-tls-verify: true\ncontexts:\n- name: q\n  context:\n    cluster: q\n    user: q\nusers:\n- name: q\n  user:\n    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: credential-helper\n      interactiveMode: Never\n"
	if err := os.WriteFile(path, []byte(execConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKubernetesLeaderFailoverTarget(KubernetesLeaderFailoverConfig{Kubeconfig: path, Namespace: "qualification", StatefulSet: "charlie-agent"}); err == nil {
		t.Fatal("exec credential plugin was accepted")
	}
	staticConfig := "apiVersion: v1\nkind: Config\ncurrent-context: q\nclusters:\n- name: q\n  cluster:\n    server: https://127.0.0.1:6443\n    insecure-skip-tls-verify: true\ncontexts:\n- name: q\n  context:\n    cluster: q\n    user: q\nusers:\n- name: q\n  user:\n    token: bounded-qualification-token\n"
	if err := os.WriteFile(path, []byte(staticConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKubernetesLeaderFailoverTarget(KubernetesLeaderFailoverConfig{Kubeconfig: path, Namespace: "qualification", StatefulSet: "charlie-agent", PollInterval: time.Second}); err != nil {
		t.Fatalf("bounded static kubeconfig rejected: %v", err)
	}
}

func TestFailoverEventStreamRequiresConnectedBoundaryAndOneAction(t *testing.T) {
	body := ": connected\n\n" +
		"id: event-1\nevent: action.proposed\ndata: {\"turn_id\":\"turn-1\",\"action_id\":\"action-1\",\"type\":\"action.proposed\",\"data\":{\"data\":{\"capability\":\"astronomer.queue.retry_task\"}}}\n\n" +
		"id: event-2\nevent: turn.completed\ndata: {\"turn_id\":\"turn-1\",\"type\":\"turn.completed\"}\n\n"
	stream := testFailoverStream(body)
	defer stream.Close()
	if err := stream.AwaitConnected(t.Context()); err != nil {
		t.Fatal(err)
	}
	actionID, firstEventID, err := stream.AwaitAction(t.Context(), "turn-1", "astronomer.queue.retry_task")
	if err != nil || actionID != "action-1" || firstEventID != "event-1" {
		t.Fatalf("action=%q first=%q err=%v", actionID, firstEventID, err)
	}

	duplicate := testFailoverStream(stringsForDuplicateActions())
	defer duplicate.Close()
	if err := duplicate.AwaitConnected(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := duplicate.AwaitAction(t.Context(), "turn-1", "astronomer.queue.retry_task"); err == nil {
		t.Fatal("two action identities passed the failover stream proof")
	}
}

func testFailoverStream(body string) *failoverEventStream {
	readCloser := io.NopCloser(strings.NewReader(body))
	scanner := bufio.NewScanner(readCloser)
	return &failoverEventStream{cancel: func() {}, body: readCloser, scanner: scanner}
}

func stringsForDuplicateActions() string {
	return ": connected\n\n" +
		"id: event-1\nevent: action.proposed\ndata: {\"turn_id\":\"turn-1\",\"action_id\":\"action-1\",\"type\":\"action.proposed\",\"data\":{\"data\":{\"capability\":\"astronomer.queue.retry_task\"}}}\n\n" +
		"id: event-2\nevent: action.proposed\ndata: {\"turn_id\":\"turn-1\",\"action_id\":\"action-2\",\"type\":\"action.proposed\",\"data\":{\"data\":{\"capability\":\"astronomer.queue.retry_task\"}}}\n\n"
}

func leaderStatefulSet(replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "charlie-agent", Namespace: "qualification", UID: "statefulset-uid"}, Spec: appsv1.StatefulSetSpec{Replicas: &replicas}}
}

func leaderPod(ordinal int, uid string, ready bool) *corev1.Pod {
	return leaderPodWithOwner(ordinal, uid, ready, "charlie-agent", "statefulset-uid")
}

func leaderPodWithOwner(ordinal int, uid string, ready bool, ownerName, ownerUID string) *corev1.Pod {
	controller := true
	condition := corev1.ConditionFalse
	if ready {
		condition = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("charlie-agent-%d", ordinal), Namespace: "qualification", UID: types.UID(uid), OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: ownerName, UID: types.UID(ownerUID), Controller: &controller}}},
		Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: condition}}},
	}
}
