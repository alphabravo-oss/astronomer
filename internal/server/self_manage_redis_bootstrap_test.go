package server

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRemoveHelmOwnedArgoRedisSecretInitHooks(t *testing.T) {
	ctx := context.Background()
	objects := redisSecretInitHookFixtures()
	client := fake.NewSimpleClientset(objects...)

	if err := removeHelmOwnedArgoRedisSecretInitHooks(ctx, client); err != nil {
		t.Fatalf("remove Helm-owned hooks: %v", err)
	}
	if err := verifyHelmOwnedArgoRedisSecretInitHooksAbsent(ctx, client); err != nil {
		t.Fatalf("verify hooks absent: %v", err)
	}
	// Cleanup is idempotent for retries after a partial or completed handoff.
	if err := removeHelmOwnedArgoRedisSecretInitHooks(ctx, client); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
	if _, err := client.CoreV1().ServiceAccounts(localAstronomerNamespace).Get(ctx, argoRedisSecretInitName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("ServiceAccount still exists or get failed unexpectedly: %v", err)
	}
}

func TestRemoveHelmOwnedArgoRedisSecretInitHooksRefusesLookalike(t *testing.T) {
	ctx := context.Background()
	objects := redisSecretInitHookFixtures()
	role := objects[2].(*rbacv1.Role)
	role.Labels["app.kubernetes.io/managed-by"] = "some-other-controller"
	client := fake.NewSimpleClientset(objects...)

	err := removeHelmOwnedArgoRedisSecretInitHooks(ctx, client)
	if err == nil || !strings.Contains(err.Error(), "refuse to delete Role") {
		t.Fatalf("lookalike error = %v", err)
	}
	if _, getErr := client.RbacV1().Roles(localAstronomerNamespace).Get(ctx, argoRedisSecretInitName, metav1.GetOptions{}); getErr != nil {
		t.Fatalf("lookalike Role was mutated: %v", getErr)
	}
	if _, getErr := client.BatchV1().Jobs(localAstronomerNamespace).Get(ctx, argoRedisSecretInitName, metav1.GetOptions{}); getErr != nil {
		t.Fatalf("valid sibling Job was mutated before lookalike refusal: %v", getErr)
	}
}

func redisSecretInitHookFixtures() []runtime.Object {
	metadata := func() metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Name:      argoRedisSecretInitName,
			Namespace: localAstronomerNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/component":  "redis-secret-init",
				"app.kubernetes.io/instance":   localAstronomerReleaseName,
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/name":       "argocd-redis-secret-init",
				"app.kubernetes.io/part-of":    "argocd",
			},
			Annotations: map[string]string{
				"helm.sh/hook":               "pre-install,pre-upgrade",
				"helm.sh/hook-delete-policy": "before-hook-creation",
			},
		}
	}
	return []runtime.Object{
		&batchv1.Job{ObjectMeta: metadata()},
		&rbacv1.RoleBinding{ObjectMeta: metadata()},
		&rbacv1.Role{ObjectMeta: metadata()},
		&corev1.ServiceAccount{ObjectMeta: metadata()},
	}
}
