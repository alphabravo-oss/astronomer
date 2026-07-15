package server

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const argoRedisSecretInitName = "astro-argocd-redis-secret-init"

type argoRedisSecretInitHook struct {
	kind   string
	get    func(context.Context) (metav1.Object, error)
	delete func(context.Context, metav1.DeleteOptions) error
}

func argoRedisSecretInitHooks(k8s kubernetes.Interface) []argoRedisSecretInitHook {
	ns := localAstronomerNamespace
	name := argoRedisSecretInitName
	return []argoRedisSecretInitHook{
		{kind: "Job", get: func(ctx context.Context) (metav1.Object, error) {
			return k8s.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
		}, delete: func(ctx context.Context, options metav1.DeleteOptions) error {
			return k8s.BatchV1().Jobs(ns).Delete(ctx, name, options)
		}},
		{kind: "RoleBinding", get: func(ctx context.Context) (metav1.Object, error) {
			return k8s.RbacV1().RoleBindings(ns).Get(ctx, name, metav1.GetOptions{})
		}, delete: func(ctx context.Context, options metav1.DeleteOptions) error {
			return k8s.RbacV1().RoleBindings(ns).Delete(ctx, name, options)
		}},
		{kind: "Role", get: func(ctx context.Context) (metav1.Object, error) {
			return k8s.RbacV1().Roles(ns).Get(ctx, name, metav1.GetOptions{})
		}, delete: func(ctx context.Context, options metav1.DeleteOptions) error {
			return k8s.RbacV1().Roles(ns).Delete(ctx, name, options)
		}},
		{kind: "ServiceAccount", get: func(ctx context.Context) (metav1.Object, error) {
			return k8s.CoreV1().ServiceAccounts(ns).Get(ctx, name, metav1.GetOptions{})
		}, delete: func(ctx context.Context, options metav1.DeleteOptions) error {
			return k8s.CoreV1().ServiceAccounts(ns).Delete(ctx, name, options)
		}},
	}
}

func removeHelmOwnedArgoRedisSecretInitHooks(ctx context.Context, k8s kubernetes.Interface) error {
	type validatedHook struct {
		hook argoRedisSecretInitHook
		uid  *types.UID
	}
	present := make([]validatedHook, 0, 4)
	for _, hook := range argoRedisSecretInitHooks(k8s) {
		object, err := hook.get(ctx)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s %s: %w", hook.kind, argoRedisSecretInitName, err)
		}
		if err := validateHelmOwnedArgoRedisSecretInitHook(hook.kind, object); err != nil {
			return err
		}
		uid := object.GetUID()
		present = append(present, validatedHook{hook: hook, uid: &uid})
	}
	// Validate every same-named object before deleting any of them so a
	// lookalike causes a completely non-mutating, fail-closed refusal.
	for _, validated := range present {
		if err := validated.hook.delete(ctx, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: validated.uid}}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete %s %s: %w", validated.hook.kind, argoRedisSecretInitName, err)
		}
	}
	return verifyHelmOwnedArgoRedisSecretInitHooksAbsent(ctx, k8s)
}

func verifyHelmOwnedArgoRedisSecretInitHooksAbsent(ctx context.Context, k8s kubernetes.Interface) error {
	for _, hook := range argoRedisSecretInitHooks(k8s) {
		_, err := hook.get(ctx)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("recheck %s %s absence: %w", hook.kind, argoRedisSecretInitName, err)
		}
		return fmt.Errorf("Helm-owned Argo Redis bootstrap %s %s reappeared", hook.kind, argoRedisSecretInitName)
	}
	return nil
}

func validateHelmOwnedArgoRedisSecretInitHook(kind string, object metav1.Object) error {
	labels := object.GetLabels()
	annotations := object.GetAnnotations()
	requiredLabels := map[string]string{
		"app.kubernetes.io/component":  "redis-secret-init",
		"app.kubernetes.io/instance":   localAstronomerReleaseName,
		"app.kubernetes.io/managed-by": "Helm",
		"app.kubernetes.io/name":       "argocd-redis-secret-init",
		"app.kubernetes.io/part-of":    "argocd",
	}
	for key, value := range requiredLabels {
		if labels[key] != value {
			return fmt.Errorf("refuse to delete %s %s: label %s=%q, want %q", kind, object.GetName(), key, labels[key], value)
		}
	}
	if annotations["helm.sh/hook"] != "pre-install,pre-upgrade" || annotations["helm.sh/hook-delete-policy"] != "before-hook-creation" {
		return fmt.Errorf("refuse to delete %s %s: Helm hook identity does not match the Argo Redis bootstrap contract", kind, object.GetName())
	}
	return nil
}
