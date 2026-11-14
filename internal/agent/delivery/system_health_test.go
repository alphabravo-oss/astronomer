package delivery

import (
	"encoding/json"
	"testing"

	"github.com/google/cel-go/cel"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestSystemDeploymentHealth(t *testing.T) {
	t.Parallel()
	checks := systemDeploymentHealthChecks()
	expressions := checks[0].(map[string]any)
	programs := compileDeploymentHealth(t, expressions)
	fixtures := []map[string]any{}
	for _, tc := range []struct {
		name   string
		mutate func(*appsv1.Deployment)
		state  string
		health string
	}{
		{"leader and standby beyond deadline", func(*appsv1.Deployment) {}, "current", "healthy"},
		{"leader lost", func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 0; d.Status.ReadyReplicas = 0 }, "failed", "unavailable"},
		{"artifact endpoint unavailable", func(d *appsv1.Deployment) { d.Status.ReadyReplicas = 0 }, "failed", "unavailable"},
		{"leader recovering", func(d *appsv1.Deployment) {
			d.Status.AvailableReplicas = 0
			d.Status.ReadyReplicas = 0
			d.Status.Conditions = nil
		}, "inProgress", "unavailable"},
		{"unobserved generation", func(d *appsv1.Deployment) { d.Generation++ }, "inProgress", "degraded"},
		{"missing observed generation", func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 0 }, "inProgress", "degraded"},
		{"old leader while new revision fails", func(d *appsv1.Deployment) { d.Status.UpdatedReplicas = 1 }, "failed", "degraded"},
		{"extra old replica", func(d *appsv1.Deployment) { d.Status.Replicas = 3 }, "failed", "degraded"},
		{"missing standby replica", func(d *appsv1.Deployment) { d.Status.Replicas = 1 }, "failed", "degraded"},
		{"replica creation failure", func(d *appsv1.Deployment) {
			d.Status.Conditions = append(d.Status.Conditions, appsv1.DeploymentCondition{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue})
		}, "failed", "degraded"},
		{"scaled to zero", func(d *appsv1.Deployment) {
			*d.Spec.Replicas = 0
			d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation}
		}, "inProgress", "unavailable"},
		{"deleting", func(d *appsv1.Deployment) { now := metav1.Now(); d.DeletionTimestamp = &now }, "failed", "degraded"},
		{"ordinary controller unavailable", func(d *appsv1.Deployment) { d.Name = "helm-controller" }, "failed", "degraded"},
		{"same name outside system", func(d *appsv1.Deployment) { d.Namespace = "other" }, "failed", "degraded"},
		{"ordinary controller complete", func(d *appsv1.Deployment) {
			d.Name = "helm-controller"
			d.Status.AvailableReplicas = 2
			d.Status.ReadyReplicas = 2
			d.Status.Conditions = nil
		}, "current", "healthy"},
	} {
		item := readyController(t, "source-controller", false)
		item.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
		replicas := int32(2)
		item.Spec.Replicas = &replicas
		item.Status.Replicas = 2
		item.Status.UpdatedReplicas = 2
		item.Status.Conditions = []appsv1.DeploymentCondition{
			{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse},
			{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"},
		}
		tc.mutate(item)
		object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(item)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, map[string]any{"name": tc.name, "object": object, "expected": tc.state})
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := evaluateDeploymentHealth(t, programs, object); got != tc.state {
				t.Fatalf("Flux health = %s, want %s", got, tc.state)
			}
			identity := componentIdentity{category: "delivery", owner: "astronomer", method: "flux"}
			component := deploymentComponent(item, identity)
			if component.Health != tc.health {
				t.Fatalf("inventory health = %s, want %s", component.Health, tc.health)
			}
			if component.DesiredReplicas != *item.Spec.Replicas || component.ReadyReplicas != item.Status.ReadyReplicas {
				t.Fatal("inventory changed actual replica counts")
			}
			if item.Name == "source-controller" && item.Namespace == DeliverySystemNamespace {
				if got := deploymentReady(item, item.Name); got != (tc.state == "current") {
					t.Fatalf("probe readiness = %v, want %v", got, tc.state == "current")
				}
			}
		})
	}
	// Verbose output also supplies sanitized fixtures to the pinned Flux runtime
	// compatibility check, without exposing cluster data or modifying source.
	evidence, err := json.Marshal(map[string]any{"checks": checks, "fixtures": fixtures})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FLUX_HEALTH_FIXTURES=%s", evidence)
}

func compileDeploymentHealth(t *testing.T, expressions map[string]any) map[string]cel.Program {
	t.Helper()
	env, err := cel.NewEnv(cel.Variable("metadata", cel.DynType), cel.Variable("spec", cel.DynType), cel.Variable("status", cel.DynType))
	if err != nil {
		t.Fatal(err)
	}
	programs := map[string]cel.Program{}
	for _, name := range []string{"inProgress", "failed", "current"} {
		ast, issues := env.Compile(expressions[name].(string))
		if issues.Err() != nil {
			t.Fatalf("invalid %s: %v", name, issues.Err())
		}
		program, err := env.Program(ast)
		if err != nil {
			t.Fatal(err)
		}
		programs[name] = program
	}
	return programs
}

func evaluateDeploymentHealth(t *testing.T, programs map[string]cel.Program, object map[string]any) string {
	t.Helper()
	for _, name := range []string{"inProgress", "failed", "current"} {
		result, _, err := programs[name].Eval(object)
		if err != nil {
			t.Fatalf("evaluate %s: %v", name, err)
		}
		if result.Value() == true {
			return name
		}
	}
	return "inProgress"
}

func TestSystemObjectsRetainWaitAndDeploymentHealthChecks(t *testing.T) {
	t.Parallel()
	objects := systemObjects(systemReleaseFixture())
	kustomization := objects[len(objects)-1]
	wait, _, err := unstructured.NestedBool(kustomization.Object, "spec", "wait")
	if err != nil || !wait {
		t.Fatal("system rollout must wait for resource health")
	}
	checks, found, err := unstructured.NestedSlice(kustomization.Object, "spec", "healthCheckExprs")
	if err != nil || !found || len(checks) != 1 {
		t.Fatal("missing Deployment health expressions")
	}
	compileDeploymentHealth(t, checks[0].(map[string]any))
}
