package charlie

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type recordingModeCeilingInstaller struct{ desired []Mode }

func (i *recordingModeCeilingInstaller) ReconcileModeCeiling(_ context.Context, _ AgentInstallSpec, desired Mode) error {
	i.desired = append(i.desired, desired)
	return nil
}

func TestModeCeilingRolloutSeparatesExpectedDBSnapshotFromDesiredCeiling(t *testing.T) {
	connectionID := uuid.MustParse("3c608d44-848c-45d6-bd86-246be0b880af")
	for _, test := range []struct {
		name       string
		connection sqlc.CharlieConnection
		target     ModeCeilingTarget
		wantErr    bool
	}{
		{
			name: "auto central downgrade to read only",
			connection: sqlc.CharlieConnection{ID: connectionID, InstallationID: connectionID, Active: true,
				RequestedMode: string(ModeAuto), VerifiedMode: string(ModeReadOnly), VerifiedModeRevision: 5},
			target: ModeCeilingTarget{ConnectionID: connectionID.String(), ExpectedRequested: ModeAuto, ExpectedRevision: 5, Desired: ModeReadOnly},
		},
		{
			name: "central suspension to disabled",
			connection: sqlc.CharlieConnection{ID: connectionID, InstallationID: connectionID, Active: true,
				RequestedMode: string(ModeAuto), VerifiedMode: string(ModeDisabled), VerifiedModeRevision: 6},
			target: ModeCeilingTarget{ConnectionID: connectionID.String(), ExpectedRequested: ModeAuto, ExpectedRevision: 6, Desired: ModeDisabled},
		},
		{
			name: "upward restoration from prior snapshot",
			connection: sqlc.CharlieConnection{ID: connectionID, InstallationID: connectionID, Active: true,
				RequestedMode: string(ModeAuto), VerifiedMode: string(ModeApproval), VerifiedModeRevision: 4},
			target: ModeCeilingTarget{ConnectionID: connectionID.String(), ExpectedRequested: ModeAuto, ExpectedRevision: 4, Desired: ModeAuto},
		},
		{
			name: "second replica won revision CAS",
			connection: sqlc.CharlieConnection{ID: connectionID, InstallationID: connectionID, Active: true,
				RequestedMode: string(ModeAuto), VerifiedMode: string(ModeAuto), VerifiedModeRevision: 5},
			target:  ModeCeilingTarget{ConnectionID: connectionID.String(), ExpectedRequested: ModeAuto, ExpectedRevision: 4, Desired: ModeAuto},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			installer := &recordingModeCeilingInstaller{}
			rollout := modeCeilingRollout{load: func(context.Context) (sqlc.CharlieConnection, error) {
				return test.connection, nil
			}, installer: installer}
			err := rollout.Reconcile(t.Context(), test.target)
			if (err != nil) != test.wantErr {
				t.Fatalf("rollout error=%v want_error=%t", err, test.wantErr)
			}
			if test.wantErr && len(installer.desired) != 0 {
				t.Fatal("stale HA snapshot reached the installer")
			}
			if !test.wantErr && (len(installer.desired) != 1 || installer.desired[0] != test.target.Desired) {
				t.Fatalf("desired ceiling was not separated from DB snapshot: %v", installer.desired)
			}
		})
	}
}

func TestModeCeilingRolloutRejectsSecondReplicaCASDuringReadback(t *testing.T) {
	connectionID := uuid.MustParse("3c608d44-848c-45d6-bd86-246be0b880af")
	connection := sqlc.CharlieConnection{ID: connectionID, InstallationID: connectionID, Active: true,
		RequestedMode: string(ModeAuto), VerifiedMode: string(ModeApproval), VerifiedModeRevision: 4}
	loads := 0
	installer := &recordingModeCeilingInstaller{}
	rollout := modeCeilingRollout{load: func(context.Context) (sqlc.CharlieConnection, error) {
		loads++
		if loads == 2 {
			connection.VerifiedModeRevision = 5
		}
		return connection, nil
	}, installer: installer}
	err := rollout.Reconcile(t.Context(), ModeCeilingTarget{
		ConnectionID: connectionID.String(), ExpectedRequested: ModeAuto, ExpectedRevision: 4, Desired: ModeAuto,
	})
	if err == nil || loads != 2 || len(installer.desired) != 1 {
		t.Fatalf("second-replica CAS was not detected after rollout: loads=%d desired=%v err=%v", loads, installer.desired, err)
	}
}

func TestModeCeilingRolloutRequiresNonPruningAllReplicaReadback(t *testing.T) {
	installer, kube, _, _ := testAgentInstaller(t)
	spec := testAgentInstallSpec(t)
	receipt, err := installer.Install(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	applicationResource := installer.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace("astronomer")
	application, err := applicationResource.Get(t.Context(), receipt.Names.Application, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(application.Object, "Synced", "status", "sync", "status")
	_ = unstructured.SetNestedField(application.Object, "Healthy", "status", "health", "status")
	if _, err = applicationResource.Update(t.Context(), application, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	createReadyModeCeilingWorkload(t, kube, ModeApproval, true)

	if err = installer.ReconcileModeCeiling(t.Context(), spec, ModeApproval); err != nil {
		t.Fatal(err)
	}
	if err = installer.ReconcileModeCeiling(t.Context(), spec, ModeApproval); err != nil {
		t.Fatalf("idempotent rollout retry failed: %v", err)
	}
	application, _ = applicationResource.Get(t.Context(), receipt.Names.Application, metav1.GetOptions{})
	valuesText, _, _ := unstructured.NestedString(application.Object, "spec", "source", "helm", "values")
	var values map[string]any
	if json.Unmarshal([]byte(valuesText), &values) != nil || values["runtime"].(map[string]any)["modeCeiling"] != string(ModeApproval) {
		t.Fatalf("Argo values did not persist the exact ceiling")
	}
	if prune, found, _ := unstructured.NestedBool(application.Object, "spec", "syncPolicy", "automated", "prune"); !found || prune {
		t.Fatal("mode transition did not use a non-pruning Argo sync")
	}
}

func TestModeCeilingRolloutFailsClosedOnPartialOrMismatchedReplicas(t *testing.T) {
	for name, configure := range map[string]func(*testing.T, *AgentInstaller, AgentInstallSpec){
		"missing workload": func(*testing.T, *AgentInstaller, AgentInstallSpec) {},
		"one ready replica": func(t *testing.T, installer *AgentInstaller, _ AgentInstallSpec) {
			createReadyModeCeilingWorkload(t, installer.kube, ModeAuto, false)
		},
		"stale replica ceiling": func(t *testing.T, installer *AgentInstaller, _ AgentInstallSpec) {
			createReadyModeCeilingWorkload(t, installer.kube, ModeReadOnly, true)
		},
	} {
		t.Run(name, func(t *testing.T) {
			installer, _, _, _ := testAgentInstaller(t)
			spec := testAgentInstallSpec(t)
			receipt, err := installer.Install(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			applicationResource := installer.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace("astronomer")
			application, _ := applicationResource.Get(t.Context(), receipt.Names.Application, metav1.GetOptions{})
			_ = unstructured.SetNestedField(application.Object, "Synced", "status", "sync", "status")
			_ = unstructured.SetNestedField(application.Object, "Healthy", "status", "health", "status")
			_, _ = applicationResource.Update(t.Context(), application, metav1.UpdateOptions{})
			configure(t, installer, spec)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			if err := installer.ReconcileModeCeiling(ctx, spec, ModeAuto); err == nil {
				t.Fatal("incomplete all-replica readback was accepted")
			}
		})
	}
}

func createReadyModeCeilingWorkload(t *testing.T, kube kubernetes.Interface, mode Mode, twoPods bool) {
	t.Helper()
	ctx := t.Context()
	replicas := int32(2)
	uid := types.UID("charlie-agent-statefulset")
	container := modeCeilingContainer(mode)
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: charlieAgentWorkloadName, Namespace: DefaultCharlieAgentNamespace, UID: uid, Generation: 2},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{container}}}},
		// Kubernetes may omit the optional currentReplicas counter even after a
		// StatefulSet is fully observed. The aggregate, updated, ready, and
		// revision fields are the authoritative rollout proof.
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2,
			CurrentRevision: "revision-2", UpdateRevision: "revision-2"},
	}
	if !twoPods {
		statefulSet.Status.ReadyReplicas = 1
	}
	if _, err := kube.AppsV1().StatefulSets(DefaultCharlieAgentNamespace).Create(ctx, statefulSet, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	count := 2
	if !twoPods {
		count = 1
	}
	for ordinal := 0; ordinal < count; ordinal++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: charlieAgentWorkloadName + "-" + string(rune('0'+ordinal)), Namespace: DefaultCharlieAgentNamespace,
				Labels:          map[string]string{"app.kubernetes.io/name": charlieAgentWorkloadName, "app.kubernetes.io/component": "product-agent"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: charlieAgentWorkloadName, UID: uid}}},
			Spec:   corev1.PodSpec{Containers: []corev1.Container{container}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		}
		if _, err := kube.CoreV1().Pods(DefaultCharlieAgentNamespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func modeCeilingContainer(mode Mode) corev1.Container {
	return corev1.Container{Name: "agent", Env: []corev1.EnvVar{{Name: "CHARLIE_MODE", Value: string(mode)}}}
}
