package server

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"
)

func secretRefValues(ref selfManagedSecretRef) map[string]any {
	return map[string]any{"name": ref.Name, "key": ref.Key}
}

func referencedSecretNames(values map[string]any) []string {
	var names []string
	for key, raw := range values {
		if strings.HasSuffix(strings.ToLower(key), "secretref") {
			if ref, ok := raw.(map[string]any); ok {
				if name, _ := ref["name"].(string); strings.TrimSpace(name) != "" {
					names = append(names, name)
				}
			}
		}
		if nested, ok := raw.(map[string]any); ok {
			names = append(names, referencedSecretNames(nested)...)
		}
	}
	return names
}

func protectSelfManagedSecret(ctx context.Context, k8s kubernetes.Interface, name string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := k8s.CoreV1().Secrets(localAstronomerNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		ownedByLegacyHelm := secret.Annotations["meta.helm.sh/release-name"] == localAstronomerReleaseName
		ownedBySelfManager := secret.Labels[selfManagedSecretOwnerLabel] == "true"
		if !ownedByLegacyHelm && !ownedBySelfManager {
			return nil
		}
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		currentSync := secret.Annotations["argocd.argoproj.io/sync-options"]
		currentCompare := secret.Annotations["argocd.argoproj.io/compare-options"]
		desiredSync := mergeCommaOptions(currentSync, "Prune=false", "Delete=false")
		desiredCompare := mergeCommaOptions(currentCompare, "IgnoreExtraneous")
		if currentSync == desiredSync && currentCompare == desiredCompare {
			return nil
		}
		secret.Annotations["argocd.argoproj.io/sync-options"] = desiredSync
		secret.Annotations["argocd.argoproj.io/compare-options"] = desiredCompare
		_, err = k8s.CoreV1().Secrets(localAstronomerNamespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}

func mergeCommaOptions(current string, required ...string) string {
	parts := strings.Split(current, ",")
	seen := map[string]struct{}{}
	result := make([]string, 0, len(parts)+len(required))
	for _, part := range append(parts, required...) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return strings.Join(result, ",")
}

func deploymentEnvSecretRef(deploy *appsv1.Deployment, containerName, envName string) (selfManagedSecretRef, bool) {
	if deploy == nil {
		return selfManagedSecretRef{}, false
	}
	for _, container := range deploy.Spec.Template.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		for _, env := range container.Env {
			if env.Name == envName && env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				ref := env.ValueFrom.SecretKeyRef
				if strings.TrimSpace(ref.Name) != "" && strings.TrimSpace(ref.Key) != "" {
					return selfManagedSecretRef{Name: ref.Name, Key: ref.Key}, true
				}
			}
		}
	}
	return selfManagedSecretRef{}, false
}

func statefulSetEnvSecretRef(statefulSet *appsv1.StatefulSet, containerName, envName string) (selfManagedSecretRef, bool) {
	if statefulSet == nil {
		return selfManagedSecretRef{}, false
	}
	for _, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		for _, env := range container.Env {
			if env.Name == envName && env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				ref := env.ValueFrom.SecretKeyRef
				if strings.TrimSpace(ref.Name) != "" && strings.TrimSpace(ref.Key) != "" {
					return selfManagedSecretRef{Name: ref.Name, Key: ref.Key}, true
				}
			}
		}
	}
	return selfManagedSecretRef{}, false
}

func statefulSetExists(ctx context.Context, k8s kubernetes.Interface, name string) (bool, error) {
	_, err := k8s.AppsV1().StatefulSets(localAstronomerNamespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func selfManagedDatabaseSecretRefs(ctx context.Context, k8s kubernetes.Interface, server *appsv1.Deployment, coreSecret *corev1.Secret, bundled bool) (selfManagedSecretRef, selfManagedSecretRef, error) {
	if ref, ok := deploymentEnvSecretRef(server, "server", "DATABASE_URL"); ok {
		if bundled {
			postgres, err := k8s.AppsV1().StatefulSets(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName+"-postgres", metav1.GetOptions{})
			if err != nil {
				return selfManagedSecretRef{}, selfManagedSecretRef{}, err
			}
			passwordRef, ok := statefulSetEnvSecretRef(postgres, "postgres", "POSTGRES_PASSWORD")
			if !ok {
				return selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("bundled Postgres StatefulSet has no POSTGRES_PASSWORD Secret reference")
			}
			return ref, passwordRef, nil
		}
		return ref, selfManagedSecretRef{}, nil
	}
	configMap, err := k8s.CoreV1().ConfigMaps(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName+"-config", metav1.GetOptions{})
	if err != nil {
		return selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("read current DATABASE_URL source: %w", err)
	}
	dsn := strings.TrimSpace(configMap.Data["DATABASE_URL"])
	if dsn == "" {
		return selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("current DATABASE_URL is empty and has no Secret reference")
	}
	data := map[string][]byte{"dsn": []byte(dsn)}
	passwordRef := selfManagedSecretRef{}
	if bundled {
		password := coreSecret.Data["POSTGRES_PASSWORD"]
		if len(password) == 0 {
			return selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("core credential Secret is missing POSTGRES_PASSWORD for bundled Postgres")
		}
		data["password"] = password
		passwordRef = selfManagedSecretRef{Name: selfManagedDatabaseSecret, Key: "password"}
	}
	ref := selfManagedSecretRef{Name: selfManagedDatabaseSecret, Key: "dsn"}
	if err := ensureSelfManagedCredentialSecret(ctx, k8s, selfManagedDatabaseSecret, data); err != nil {
		return selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("migrate DATABASE_URL to Secret: %w", err)
	}
	return ref, passwordRef, nil
}

func selfManagedExternalRedisValues(ctx context.Context, k8s kubernetes.Interface, server *appsv1.Deployment) (map[string]any, error) {
	if ref, ok := deploymentEnvSecretRef(server, "server", "REDIS_URL"); ok {
		return map[string]any{"urlSecretRef": secretRefValues(ref)}, nil
	}
	configMap, err := k8s.CoreV1().ConfigMaps(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName+"-config", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read current REDIS_URL source: %w", err)
	}
	redisURL := strings.TrimSpace(configMap.Data["REDIS_URL"])
	if redisURL == "" {
		return nil, fmt.Errorf("current REDIS_URL is empty and has no Secret reference")
	}
	passwordRef, hasPasswordRef := deploymentEnvSecretRef(server, "server", "REDIS_PASSWORD")
	parseValue := redisURL
	if strings.Contains(parseValue, "$(REDIS_PASSWORD)") && hasPasswordRef {
		parseValue = strings.ReplaceAll(parseValue, "$(REDIS_PASSWORD)", "reference-backed-placeholder")
		parsed, err := url.Parse(parseValue)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("parse reference-backed REDIS_URL")
		}
		database := 0
		if rawDB := strings.TrimPrefix(parsed.EscapedPath(), "/"); rawDB != "" {
			database, err = strconv.Atoi(rawDB)
			if err != nil {
				return nil, fmt.Errorf("parse REDIS_URL database: %w", err)
			}
		}
		return map[string]any{
			"address":           parsed.Host,
			"tls":               parsed.Scheme == "rediss",
			"database":          database,
			"passwordSecretRef": secretRefValues(passwordRef),
		}, nil
	}
	parsed, parseErr := url.Parse(redisURL)
	if parseErr == nil && parsed.Host != "" && parsed.User == nil {
		database := 0
		if rawDB := strings.TrimPrefix(parsed.EscapedPath(), "/"); rawDB != "" {
			database, parseErr = strconv.Atoi(rawDB)
		}
		if parseErr == nil {
			return map[string]any{"address": parsed.Host, "tls": parsed.Scheme == "rediss", "database": database}, nil
		}
	}
	ref := selfManagedSecretRef{Name: selfManagedRedisSecret, Key: "url"}
	if err := ensureSelfManagedCredentialSecret(ctx, k8s, ref.Name, map[string][]byte{ref.Key: []byte(redisURL)}); err != nil {
		return nil, fmt.Errorf("migrate REDIS_URL to Secret: %w", err)
	}
	return map[string]any{"urlSecretRef": secretRefValues(ref)}, nil
}

func selfManagedDexValues(ctx context.Context, k8s kubernetes.Interface) (map[string]any, error) {
	deploy, err := k8s.AppsV1().Deployments(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName+"-dex", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return map[string]any{"enabled": false}, nil
	}
	if err != nil {
		return nil, err
	}
	ref, ok := deploymentEnvSecretRef(deploy, "dex", "ASTRONOMER_DEX_CLIENT_SECRET")
	if !ok {
		configMap, err := k8s.CoreV1().ConfigMaps(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName+"-dex-config", metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("read Dex client credential source: %w", err)
		}
		secret, err := dexAstronomerStaticClientSecret(configMap.Data["config.yaml"])
		if err != nil {
			return nil, err
		}
		ref = selfManagedSecretRef{Name: selfManagedDexSecret, Key: "clientSecret"}
		if err := ensureSelfManagedCredentialSecret(ctx, k8s, ref.Name, map[string][]byte{ref.Key: []byte(secret)}); err != nil {
			return nil, fmt.Errorf("migrate Dex client secret: %w", err)
		}
	}
	replicas := int32(1)
	if deploy.Spec.Replicas != nil {
		replicas = *deploy.Spec.Replicas
	}
	values := map[string]any{
		"enabled":         true,
		"replicaCount":    replicas,
		"clientSecretRef": secretRefValues(ref),
	}
	for _, container := range deploy.Spec.Template.Spec.Containers {
		if container.Name == "dex" {
			image, err := parseImageRef(container.Image)
			if err != nil {
				return nil, fmt.Errorf("parse Dex image: %w", err)
			}
			values["image"] = image
			break
		}
	}
	return values, nil
}

func dexAstronomerStaticClientSecret(raw string) (string, error) {
	config := map[string]any{}
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		return "", fmt.Errorf("parse Dex ConfigMap: %w", err)
	}
	clients, _ := config["staticClients"].([]any)
	for _, rawClient := range clients {
		client, _ := rawClient.(map[string]any)
		if strings.TrimSpace(fmt.Sprint(client["id"])) != "astronomer" {
			continue
		}
		secret := strings.TrimSpace(fmt.Sprint(client["secret"]))
		if secret == "" || secret == "<nil>" {
			return "", fmt.Errorf("Dex static client astronomer has no migratable secret")
		}
		return secret, nil
	}
	return "", fmt.Errorf("Dex ConfigMap has no astronomer static client")
}

func ensureSelfManagedCredentialSecret(ctx context.Context, k8s kubernetes.Interface, name string, data map[string][]byte) error {
	if name == "" || len(data) == 0 {
		return fmt.Errorf("self-managed credential Secret name and data must be non-empty")
	}
	for key, value := range data {
		if key == "" || len(value) == 0 {
			return fmt.Errorf("self-managed credential Secret keys and values must be non-empty")
		}
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secrets := k8s.CoreV1().Secrets(localAstronomerNamespace)
		current, err := secrets.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = secrets.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: name, Namespace: localAstronomerNamespace,
					Labels: map[string]string{selfManagedSecretOwnerLabel: "true"},
					Annotations: map[string]string{
						"helm.sh/resource-policy":            "keep",
						"argocd.argoproj.io/sync-options":    "Prune=false,Delete=false",
						"argocd.argoproj.io/compare-options": "IgnoreExtraneous",
					},
				},
				Type: corev1.SecretTypeOpaque, Data: data,
			}, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if current.Labels[selfManagedSecretOwnerLabel] != "true" {
			return fmt.Errorf("refuse to overwrite existing non-self-managed Secret %s", name)
		}
		labels := copyStringMap(current.Labels)
		labels[selfManagedSecretOwnerLabel] = "true"
		annotations := copyStringMap(current.Annotations)
		annotations["helm.sh/resource-policy"] = "keep"
		annotations["argocd.argoproj.io/sync-options"] = mergeCommaOptions(annotations["argocd.argoproj.io/sync-options"], "Prune=false", "Delete=false")
		annotations["argocd.argoproj.io/compare-options"] = mergeCommaOptions(annotations["argocd.argoproj.io/compare-options"], "IgnoreExtraneous")
		if current.Type == corev1.SecretTypeOpaque && reflect.DeepEqual(current.Data, data) && reflect.DeepEqual(current.Labels, labels) && reflect.DeepEqual(current.Annotations, annotations) && len(current.OwnerReferences) == 0 {
			return nil
		}
		current.Labels, current.Annotations = labels, annotations
		current.Type, current.Data = corev1.SecretTypeOpaque, data
		current.OwnerReferences = nil
		_, err = secrets.Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
}

func copyStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}
