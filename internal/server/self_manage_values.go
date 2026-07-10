package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	chartdeploy "github.com/alphabravocompany/astronomer-go/deploy"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
)

type selfManagedValuesSource struct {
	ValuesYAML       string
	AdoptLiveUpgrade bool
}

func buildSelfManagedAstronomerValues(ctx context.Context, cfg *config.Config, k8s kubernetes.Interface, serverURL string, referenceOnlySource ...selfManagedValuesSource) (string, error) {
	// A takeover must begin from Helm's complete operator-supplied values. Live
	// workload discovery alone cannot safely reconstruct storage, air-gap,
	// scheduling, TLS, backup, network-policy, observability, or bundled Argo
	// settings. Refuse to create a pruning Application if that source is absent.
	var values map[string]any
	var deployedReleaseValues map[string]any
	initialTakeover := len(referenceOnlySource) == 0 || strings.TrimSpace(referenceOnlySource[0].ValuesYAML) == ""
	liveUpgradeAdoption := !initialTakeover && referenceOnlySource[0].AdoptLiveUpgrade
	adoptRuntime := initialTakeover || liveUpgradeAdoption
	if liveUpgradeAdoption {
		if err := verifyLocalArgoApplicationControllerStopped(ctx, k8s); err != nil {
			return "", fmt.Errorf("adopt live upgrade only while the Argo application controller is quiesced: %w", err)
		}
		if err := verifySelfManagedServerRolloutComplete(ctx, k8s); err != nil {
			return "", fmt.Errorf("adopt live upgrade only after a complete server rollout: %w", err)
		}
	}
	if !initialTakeover {
		if err := yaml.Unmarshal([]byte(referenceOnlySource[0].ValuesYAML), &values); err != nil {
			return "", fmt.Errorf("parse current reference-only self-managed values: %w", err)
		}
		if liveUpgradeAdoption {
			var err error
			deployedReleaseValues, err = currentHelmReleaseValues(ctx, k8s)
			if err != nil {
				return "", fmt.Errorf("load highest deployed external Helm release values for bounded topology/runtime adoption: %w", err)
			}
		}
	} else {
		var err error
		values, err = currentHelmReleaseValues(ctx, k8s)
		if err != nil {
			return "", fmt.Errorf("load current Helm release values for safe takeover: %w", err)
		}
		deployedReleaseValues = values
	}
	if err := stripKnownInlineSelfManagedCredentials(values); err != nil {
		return "", err
	}
	shape, err := chartdeploy.AstronomerDefaultValuesShape()
	if err != nil {
		return "", fmt.Errorf("load audited chart values vocabulary: %w", err)
	}
	if err := validateSelfManagedValuesShape(values, shape, ""); err != nil {
		return "", fmt.Errorf("Helm release values are not safe for self-management takeover: %w", err)
	}
	parsedURL, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil {
		return "", err
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("server_url must use http or https")
	}
	if parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return "", fmt.Errorf("server_url must not contain user info, query parameters, or a fragment")
	}
	if parsedURL.Hostname() == "" {
		return "", fmt.Errorf("server_url host is empty")
	}
	serverURL = parsedURL.Scheme + "://" + parsedURL.Host
	serverDeployment, err := k8s.AppsV1().Deployments(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName+"-server", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	coreSecret, coreSecretKeyRef, encryptionKeyRef, err := discoverCoreCredentialSecret(ctx, k8s, serverDeployment)
	if err != nil {
		return "", err
	}
	coreSecretName := coreSecret.Name
	runtimeOverlay := map[string]any{}
	if adoptRuntime {
		globalRegistry, _, _ := unstructured.NestedString(values, "image", "registry")
		serverRef, serverReplicas, migrateRef, err := deploymentImages(ctx, k8s, localAstronomerNamespace, localAstronomerReleaseName+"-server")
		if err != nil {
			return "", err
		}
		if migrateRef == "" {
			return "", fmt.Errorf("server Deployment has no migrate init-container image for safe takeover")
		}
		workerRef, workerReplicas, _, err := deploymentImages(ctx, k8s, localAstronomerNamespace, localAstronomerReleaseName+"-worker")
		if err != nil {
			return "", err
		}
		serverImage, err := parseSelfManagedFirstPartyImageRef(serverRef, globalRegistry)
		if err != nil {
			return "", fmt.Errorf("adopt server image: %w", err)
		}
		workerImage, err := parseSelfManagedFirstPartyImageRef(workerRef, globalRegistry)
		if err != nil {
			return "", fmt.Errorf("adopt worker image: %w", err)
		}
		migrateImage, err := parseSelfManagedFirstPartyImageRef(migrateRef, globalRegistry)
		if err != nil {
			return "", fmt.Errorf("adopt migrate image: %w", err)
		}
		agentImage, err := parseSelfManagedFirstPartyImageRef(strings.TrimSpace(cfg.AgentImageRepository)+":"+strings.TrimSpace(cfg.AgentImageTag), globalRegistry)
		if err != nil {
			return "", fmt.Errorf("parse configured agent image: %w", err)
		}
		images := map[string]any{"server": serverImage, "worker": workerImage, "migrate": migrateImage, "agent": agentImage}
		pullSecrets := make([]any, 0, len(serverDeployment.Spec.Template.Spec.ImagePullSecrets))
		for _, ref := range serverDeployment.Spec.Template.Spec.ImagePullSecrets {
			pullSecrets = append(pullSecrets, map[string]any{"name": ref.Name})
		}
		images["pullSecrets"] = pullSecrets
		runtimeOverlay["image"] = images
		runtimeOverlay["config"] = map[string]any{
			"agentImageRepository": cfg.AgentImageRepository,
			"agentImageTag":        cfg.AgentImageTag,
		}
		runtimeOverlay["server"] = map[string]any{"replicaCount": serverReplicas}
		runtimeOverlay["worker"] = map[string]any{"replicaCount": workerReplicas}
		frontendEnabled, frontendIntentFound, err := deployedHelmFrontendIntent(deployedReleaseValues)
		if err != nil {
			return "", err
		}
		frontendRef, frontendReplicas, _, err := deploymentImages(ctx, k8s, localAstronomerNamespace, localAstronomerReleaseName+"-frontend")
		switch {
		case err == nil:
			if frontendIntentFound && !frontendEnabled {
				return "", fmt.Errorf("highest deployed Helm release disables frontend but Deployment %s still exists; wait for deletion to converge before self-management adoption", localAstronomerReleaseName+"-frontend")
			}
			frontendImage, parseErr := parseImageRef(frontendRef)
			if parseErr != nil {
				return "", fmt.Errorf("adopt frontend image: %w", parseErr)
			}
			runtimeOverlay["frontend"] = map[string]any{"enabled": true, "replicaCount": frontendReplicas, "image": frontendImage}
		case !apierrors.IsNotFound(err):
			return "", fmt.Errorf("read frontend Deployment for self-management adoption: %w", err)
		case liveUpgradeAdoption:
			if !frontendIntentFound || frontendEnabled {
				return "", fmt.Errorf("frontend Deployment is absent but the highest deployed Helm release does not explicitly set frontend.enabled=false; refusing bounded upgrade adoption")
			}
			runtimeOverlay["frontend"] = map[string]any{"enabled": false}
		case initialTakeover:
			// Initial takeover preserves the already validated Helm release intent
			// when the optional frontend Deployment is absent.
		}
	}
	bootstrapRef, ok := deploymentEnvSecretRef(serverDeployment, "server", "ASTRONOMER_BOOTSTRAP_PASSWORD")
	if !ok {
		return "", fmt.Errorf("server Deployment does not reference ASTRONOMER_BOOTSTRAP_PASSWORD from a Secret")
	}
	intentValues := values
	if deployedReleaseValues != nil {
		intentValues = deployedReleaseValues
	}
	postgresIntent, err := effectiveSelfManagedChartBool(shape, intentValues, "postgres", "bundled", "enabled")
	if err != nil {
		return "", err
	}
	redisIntent, err := effectiveSelfManagedChartBool(shape, intentValues, "redis", "bundled", "enabled")
	if err != nil {
		return "", err
	}
	dexIntent, err := effectiveSelfManagedChartBool(shape, intentValues, "dex", "enabled")
	if err != nil {
		return "", err
	}
	postgresStatefulSet, err := selfManagedStatefulSetForIntent(ctx, k8s, localAstronomerReleaseName+"-postgres", "bundled Postgres", postgresIntent)
	if err != nil {
		return "", err
	}
	redisStatefulSet, err := selfManagedStatefulSetForIntent(ctx, k8s, localAstronomerReleaseName+"-redis", "bundled Redis", redisIntent)
	if err != nil {
		return "", err
	}
	dexDeployment, err := selfManagedDeploymentForIntent(ctx, k8s, localAstronomerReleaseName+"-dex", "bundled Dex", dexIntent)
	if err != nil {
		return "", err
	}
	postgresBundled := postgresStatefulSet != nil
	databaseRef, postgresPasswordRef, err := selfManagedDatabaseSecretRefs(ctx, k8s, serverDeployment, coreSecret, postgresStatefulSet)
	if err != nil {
		return "", err
	}
	redisBundled := redisStatefulSet != nil
	redisValues := map[string]any{"bundled": map[string]any{"enabled": redisBundled}}
	if !redisBundled {
		externalRedis, err := selfManagedExternalRedisValues(ctx, k8s, serverDeployment)
		if err != nil {
			return "", err
		}
		redisValues["external"] = externalRedis
	}
	dexValues, err := selfManagedDexValues(ctx, k8s, dexDeployment)
	if err != nil {
		return "", err
	}
	if adoptRuntime {
		if postgresStatefulSet != nil {
			postgresRuntime, err := selfManagedPostgresRuntimeValues(postgresStatefulSet)
			if err != nil {
				return "", err
			}
			runtimeOverlay["postgres"] = postgresRuntime
		}
		if redisStatefulSet != nil {
			redisRuntime, err := selfManagedRedisRuntimeValues(redisStatefulSet)
			if err != nil {
				return "", err
			}
			runtimeOverlay["redis"] = redisRuntime
		}
	}
	protectedSecrets := []string{coreSecretName, bootstrapRef.Name, databaseRef.Name}
	if !redisBundled {
		if external, ok := redisValues["external"].(map[string]any); ok {
			protectedSecrets = append(protectedSecrets, referencedSecretNames(external)...)
		}
	}
	protectedSecrets = append(protectedSecrets, referencedSecretNames(dexValues)...)
	seenProtected := map[string]struct{}{}
	for _, name := range protectedSecrets {
		if name == "" {
			continue
		}
		if _, seen := seenProtected[name]; seen {
			continue
		}
		seenProtected[name] = struct{}{}
		if err := protectSelfManagedSecret(ctx, k8s, name); err != nil {
			return "", fmt.Errorf("protect referenced Secret %s from Argo prune: %w", name, err)
		}
	}
	discovered := map[string]any{
		"config": map[string]any{
			"corsAllowedOrigins": serverURL,
			"serverURL":          serverURL,
		},
		"secrets": map[string]any{
			"existingSecret":      coreSecretName,
			"secretKeyKey":        coreSecretKeyRef.Key,
			"encryptionKeyKey":    encryptionKeyRef.Key,
			"postgresPasswordKey": "POSTGRES_PASSWORD",
		},
		"bootstrap": map[string]any{
			"existingSecret":    bootstrapRef.Name,
			"existingSecretKey": bootstrapRef.Key,
			"username":          strutil.FirstNonBlankTrimmed(os.Getenv("ASTRONOMER_BOOTSTRAP_USERNAME"), "admin"),
			"email":             strutil.FirstNonBlankTrimmed(os.Getenv("ASTRONOMER_BOOTSTRAP_EMAIL"), "admin@astronomer.local"),
		},
		"postgres": map[string]any{
			"bundled":  map[string]any{"enabled": postgresBundled},
			"external": map[string]any{"dsnSecretRef": secretRefValues(databaseRef)},
		},
		"redis": redisValues,
		"dex":   dexValues,
	}
	if postgresBundled {
		discovered["postgres"].(map[string]any)["passwordSecretRef"] = secretRefValues(postgresPasswordRef)
	}
	deepMergeSelfManagedValues(values, discovered)
	deepMergeSelfManagedValues(values, runtimeOverlay)
	if err := validateSelfManagedValuesShape(values, shape, ""); err != nil {
		return "", fmt.Errorf("generated self-managed values violate the audited chart vocabulary: %w", err)
	}
	valuesYAML := string(yamlOrPanic(values))
	if err := validateSelfManagedHelmValues(valuesYAML); err != nil {
		return "", err
	}
	return valuesYAML, nil
}

func deployedHelmFrontendIntent(values map[string]any) (bool, bool, error) {
	if values == nil {
		return false, false, fmt.Errorf("highest deployed Helm release values are unavailable for frontend adoption")
	}
	enabled, found, err := unstructured.NestedBool(values, "frontend", "enabled")
	if err != nil {
		return false, false, fmt.Errorf("highest deployed Helm release frontend.enabled is not a boolean: %w", err)
	}
	return enabled, found, nil
}

func effectiveSelfManagedChartBool(defaults, overrides map[string]any, path ...string) (bool, error) {
	defaultValue, found, err := unstructured.NestedBool(defaults, path...)
	if err != nil || !found {
		return false, fmt.Errorf("embedded chart default %s is not a boolean", strings.Join(path, "."))
	}
	value, found, err := unstructured.NestedBool(overrides, path...)
	if err != nil {
		return false, fmt.Errorf("highest deployed Helm release %s is not a boolean: %w", strings.Join(path, "."), err)
	}
	if found {
		return value, nil
	}
	return defaultValue, nil
}

func selfManagedStatefulSetForIntent(ctx context.Context, k8s kubernetes.Interface, name, component string, enabled bool) (*appsv1.StatefulSet, error) {
	statefulSet, err := k8s.AppsV1().StatefulSets(localAstronomerNamespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if enabled {
			return nil, fmt.Errorf("%s is enabled by the highest deployed Helm release but StatefulSet %s is absent; wait for creation to converge", component, name)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s StatefulSet %s: %w", component, name, err)
	}
	if !enabled {
		return nil, fmt.Errorf("%s is disabled by the highest deployed Helm release but StatefulSet %s still exists; wait for deletion to converge", component, name)
	}
	if statefulSet.DeletionTimestamp != nil {
		return nil, fmt.Errorf("%s is enabled but StatefulSet %s is terminating; wait for creation to converge", component, name)
	}
	return statefulSet, nil
}

func selfManagedDeploymentForIntent(ctx context.Context, k8s kubernetes.Interface, name, component string, enabled bool) (*appsv1.Deployment, error) {
	deployment, err := k8s.AppsV1().Deployments(localAstronomerNamespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if enabled {
			return nil, fmt.Errorf("%s is enabled by the highest deployed Helm release but Deployment %s is absent; wait for creation to converge", component, name)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s Deployment %s: %w", component, name, err)
	}
	if !enabled {
		return nil, fmt.Errorf("%s is disabled by the highest deployed Helm release but Deployment %s still exists; wait for deletion to converge", component, name)
	}
	if deployment.DeletionTimestamp != nil {
		return nil, fmt.Errorf("%s is enabled but Deployment %s is terminating; wait for creation to converge", component, name)
	}
	return deployment, nil
}

func selfManagedPostgresRuntimeValues(statefulSet *appsv1.StatefulSet) (map[string]any, error) {
	container, err := selfManagedStatefulSetContainer(statefulSet, "postgres")
	if err != nil {
		return nil, err
	}
	image, err := parseImageRef(container.Image)
	if err != nil {
		return nil, fmt.Errorf("adopt bundled Postgres image: %w", err)
	}
	user, err := selfManagedLiteralEnv(container, "POSTGRES_USER")
	if err != nil {
		return nil, fmt.Errorf("adopt bundled Postgres user: %w", err)
	}
	database, err := selfManagedLiteralEnv(container, "POSTGRES_DB")
	if err != nil {
		return nil, fmt.Errorf("adopt bundled Postgres database: %w", err)
	}
	storage, err := selfManagedStatefulSetStorage(statefulSet)
	if err != nil {
		return nil, fmt.Errorf("adopt bundled Postgres storage: %w", err)
	}
	return map[string]any{"image": image, "user": user, "database": database, "storage": storage}, nil
}

func selfManagedRedisRuntimeValues(statefulSet *appsv1.StatefulSet) (map[string]any, error) {
	container, err := selfManagedStatefulSetContainer(statefulSet, "redis")
	if err != nil {
		return nil, err
	}
	image, err := parseImageRef(container.Image)
	if err != nil {
		return nil, fmt.Errorf("adopt bundled Redis image: %w", err)
	}
	storage, err := selfManagedStatefulSetStorage(statefulSet)
	if err != nil {
		return nil, fmt.Errorf("adopt bundled Redis storage: %w", err)
	}
	return map[string]any{"image": image, "storage": storage}, nil
}

func selfManagedStatefulSetContainer(statefulSet *appsv1.StatefulSet, name string) (*corev1.Container, error) {
	if statefulSet.Spec.Replicas == nil || *statefulSet.Spec.Replicas != 1 {
		return nil, fmt.Errorf("StatefulSet %s replicas cannot be represented by the bundled chart; want exactly 1", statefulSet.Name)
	}
	for i := range statefulSet.Spec.Template.Spec.Containers {
		if statefulSet.Spec.Template.Spec.Containers[i].Name == name {
			return &statefulSet.Spec.Template.Spec.Containers[i], nil
		}
	}
	return nil, fmt.Errorf("StatefulSet %s has no %s container", statefulSet.Name, name)
}

func selfManagedLiteralEnv(container *corev1.Container, name string) (string, error) {
	for _, env := range container.Env {
		if env.Name == name && env.ValueFrom == nil && strings.TrimSpace(env.Value) != "" {
			return env.Value, nil
		}
	}
	return "", fmt.Errorf("container %s has no non-empty literal %s", container.Name, name)
}

func selfManagedStatefulSetStorage(statefulSet *appsv1.StatefulSet) (map[string]any, error) {
	for _, claim := range statefulSet.Spec.VolumeClaimTemplates {
		if claim.Name != "data" {
			continue
		}
		quantity, found := claim.Spec.Resources.Requests[corev1.ResourceStorage]
		if !found || quantity.IsZero() {
			return nil, fmt.Errorf("StatefulSet %s data claim has no storage request", statefulSet.Name)
		}
		storageClass := ""
		if claim.Spec.StorageClassName != nil {
			storageClass = *claim.Spec.StorageClassName
		}
		return map[string]any{"size": quantity.String(), "storageClassName": storageClass}, nil
	}
	return nil, fmt.Errorf("StatefulSet %s has no data volumeClaimTemplate", statefulSet.Name)
}

func parseSelfManagedFirstPartyImageRef(ref, globalRegistry string) (map[string]any, error) {
	image, err := parseImageRef(ref)
	if err != nil {
		return nil, err
	}
	globalRegistry = strings.TrimSpace(globalRegistry)
	if globalRegistry == "" {
		return image, nil
	}
	if strings.Trim(globalRegistry, "/") != globalRegistry || strings.Contains(globalRegistry, "://") {
		return nil, fmt.Errorf("global image registry %q is malformed", globalRegistry)
	}
	registry, _ := image["registry"].(string)
	repository, _ := image["repository"].(string)
	fullName := repository
	if registry != "" {
		fullName = registry + "/" + repository
	}
	prefix := globalRegistry + "/"
	if !strings.HasPrefix(fullName, prefix) {
		return nil, fmt.Errorf("image %q cannot be represented under global registry %q", ref, globalRegistry)
	}
	relativeRepository := strings.TrimPrefix(fullName, prefix)
	if relativeRepository == "" || strings.HasPrefix(relativeRepository, "/") || strings.HasSuffix(relativeRepository, "/") {
		return nil, fmt.Errorf("image %q has no repository relative to global registry %q", ref, globalRegistry)
	}
	image["registry"] = ""
	image["repository"] = relativeRepository
	return image, nil
}

func currentReferenceOnlySelfManagedValues(ctx context.Context, dyn dynamic.Interface, expectedDestinationServer string) (selfManagedValuesSource, error) {
	current, err := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace).Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return selfManagedValuesSource{}, nil
		}
		return selfManagedValuesSource{}, err
	}
	if current.GetLabels()["astronomer.io/platform-owned"] != "true" {
		return selfManagedValuesSource{}, nil
	}
	if err := validateSelfManagedApplicationSource(current.Object); err != nil {
		return selfManagedValuesSource{}, nil
	}
	values, found, err := unstructured.NestedString(current.Object, "spec", "source", "helm", "values")
	if err != nil || !found || validateReferenceOnlySelfManagedHelmValues(values) != nil {
		return selfManagedValuesSource{}, nil
	}
	targetRevision, found, err := unstructured.NestedString(current.Object, "spec", "source", "targetRevision")
	if err != nil || !found {
		return selfManagedValuesSource{}, fmt.Errorf("self-managed Application has no targetRevision")
	}
	repo, err := chartdeploy.AstronomerChartRepo()
	if err != nil {
		return selfManagedValuesSource{}, err
	}
	currentVersion, err := semver.NewVersion(targetRevision)
	if err != nil {
		return selfManagedValuesSource{}, fmt.Errorf("self-managed chart revision %q is not semantic; refusing upgrade because only exact revision equality or a bounded active upgrade may reuse its values", targetRevision)
	}
	embeddedVersion, err := semver.NewVersion(repo.Version())
	if err != nil {
		return selfManagedValuesSource{}, fmt.Errorf("embedded chart version %q is not semantic", repo.Version())
	}
	if currentVersion.GreaterThan(embeddedVersion) {
		return selfManagedValuesSource{}, fmt.Errorf("refuse self-managed chart downgrade from %s to %s", currentVersion, embeddedVersion)
	}
	if currentVersion.Equal(embeddedVersion) {
		return selfManagedValuesSource{ValuesYAML: values}, nil
	}
	if currentVersion.Major() != embeddedVersion.Major() {
		return selfManagedValuesSource{}, fmt.Errorf("self-managed chart upgrade from %s to %s crosses a major-version boundary; keep the current Application intact and follow the version-specific upgrade procedure", currentVersion, embeddedVersion)
	}
	if current.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseActive {
		return selfManagedValuesSource{}, fmt.Errorf("self-managed chart revision %s is older than embedded revision %s but the Application is not active; restore the exact old active Application identity before the bounded quiesced-controller upgrade procedure", currentVersion, embeddedVersion)
	}
	if currentVersion.LessThan(embeddedVersion) {
		if err := validateActiveSelfManagedUpgradeIdentity(current, expectedDestinationServer); err != nil {
			return selfManagedValuesSource{}, err
		}
		return selfManagedValuesSource{ValuesYAML: values, AdoptLiveUpgrade: true}, nil
	}
	return selfManagedValuesSource{}, fmt.Errorf("refuse self-managed chart revision transition from %s to %s", currentVersion, embeddedVersion)
}

func validateActiveSelfManagedUpgradeIdentity(current *unstructured.Unstructured, expectedDestinationServer string) error {
	project, _, _ := unstructured.NestedString(current.Object, "spec", "project")
	repoURL, _, _ := unstructured.NestedString(current.Object, "spec", "source", "repoURL")
	chart, _, _ := unstructured.NestedString(current.Object, "spec", "source", "chart")
	releaseName, _, _ := unstructured.NestedString(current.Object, "spec", "source", "helm", "releaseName")
	destinationServer, _, _ := unstructured.NestedString(current.Object, "spec", "destination", "server")
	destinationNamespace, _, _ := unstructured.NestedString(current.Object, "spec", "destination", "namespace")
	prune, _, _ := unstructured.NestedBool(current.Object, "spec", "syncPolicy", "automated", "prune")
	selfHeal, _, _ := unstructured.NestedBool(current.Object, "spec", "syncPolicy", "automated", "selfHeal")
	if project != "default" || repoURL != localArgoRepoURL || chart != "astronomer" || releaseName != localAstronomerReleaseName || strings.TrimSpace(expectedDestinationServer) == "" || destinationServer != expectedDestinationServer || destinationNamespace != localAstronomerNamespace || !prune || !selfHeal {
		return fmt.Errorf("claimed active self-managed Application identity is inconsistent; refusing live upgrade adoption")
	}
	spec, _, _ := unstructured.NestedMap(current.Object, "spec")
	hash, err := selfManagedSpecHash(spec)
	if err != nil {
		return err
	}
	if !selfManagedApplicationMetadataClean(current, selfManagedPhaseActive, hash) {
		return fmt.Errorf("claimed active self-managed Application metadata/hash is inconsistent; refusing live upgrade adoption")
	}
	return nil
}

// currentHelmReleaseValues reads the deployed release's operator-supplied
// values. Kubernetes Secret data is never logged or returned to an API; callers
// must rewrite known credentials and run validateSelfManagedHelmValues before
// putting the result in an Argo Application.
func currentHelmReleaseValues(ctx context.Context, k8s kubernetes.Interface) (map[string]any, error) {
	secrets, err := k8s.CoreV1().Secrets(localAstronomerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "owner=helm,name=" + localAstronomerReleaseName + ",status=deployed",
	})
	if err != nil {
		return nil, err
	}
	var selected *corev1.Secret
	selectedVersion := -1
	for i := range secrets.Items {
		candidate := &secrets.Items[i]
		if candidate.Type != corev1.SecretType("helm.sh/release.v1") || candidate.Labels["owner"] != "helm" || candidate.Labels["name"] != localAstronomerReleaseName || candidate.Labels["status"] != "deployed" {
			continue
		}
		version, err := strconv.Atoi(candidate.Labels["version"])
		if err != nil || len(candidate.Data["release"]) == 0 {
			continue
		}
		if version > selectedVersion {
			selected, selectedVersion = candidate, version
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("no deployed Helm release Secret found; refusing partial self-management takeover")
	}
	encoded, err := base64.StdEncoding.DecodeString(string(selected.Data["release"]))
	if err != nil {
		return nil, fmt.Errorf("decode Helm release payload: %w", err)
	}
	reader := io.Reader(bytes.NewReader(encoded))
	if len(encoded) >= 3 && bytes.Equal(encoded[:3], []byte{0x1f, 0x8b, 0x08}) {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("open Helm release payload: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	var release struct {
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(reader).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse Helm release payload: %w", err)
	}
	if release.Config == nil {
		return nil, fmt.Errorf("deployed Helm release has no recoverable values")
	}
	return release.Config, nil
}

func stripKnownInlineSelfManagedCredentials(values map[string]any) error {
	// These are the chart's legacy inline credential inputs. Each is replaced by
	// a native Secret reference later in the build. Unknown secret-shaped fields
	// remain in the tree so validation fails closed instead of guessing.
	for _, path := range [][]string{
		{"secrets", "secretKey"},
		{"secrets", "encryptionKey"},
		{"secrets", "postgresPassword"},
		{"secrets", "githubClientSecret"},
		{"secrets", "googleClientSecret"},
		{"secrets", "oidcClientSecret"},
		{"bootstrap", "password"},
		{"postgres", "password"},
		{"postgres", "external", "dsn"},
		{"config", "databaseURL"},
		{"config", "redisURL"},
		{"redis", "external", "password"},
		{"redis", "external", "url"},
		{"dex", "clientSecret"},
	} {
		unstructured.RemoveNestedField(values, path...)
	}
	for _, path := range [][]string{{"server", "env"}, {"worker", "env"}} {
		if raw, found, _ := unstructured.NestedFieldNoCopy(values, path...); found && !isEmptySelfManagedValue(raw) {
			return fmt.Errorf("%s is not reference-backable and must be empty before self-management takeover", strings.Join(path, "."))
		}
	}
	if raw, found, _ := unstructured.NestedFieldNoCopy(values, "observability", "tracing", "headers"); found && !isEmptySelfManagedValue(raw) {
		return fmt.Errorf("observability.tracing.headers is not reference-backable and must be empty before self-management takeover")
	}
	return nil
}

func validateSelfManagedValuesShape(values, allowed map[string]any, path string) error {
	for key, value := range values {
		childPath := key
		if path != "" {
			childPath = path + "." + key
		}
		normalizedKey := normalizedSensitiveKey(key)
		if ((normalizedKey == "env" && childPath != "config.env") || forbiddenSelfManagedFreeformKey(normalizedKey)) && !isEmptySelfManagedValue(value) {
			return fmt.Errorf("free-form values path %s is not reference-backable", childPath)
		}
		allowedValue, known := allowed[key]
		if !known {
			return fmt.Errorf("unknown values path %s", childPath)
		}
		schedulingKey := normalizedKey
		if normalizedKey == "affinity" || normalizedKey == "resources" {
			if allowedMap, ok := allowedValue.(map[string]any); ok && len(allowedMap) > 0 {
				schedulingKey = ""
			}
		}
		if handled, err := validateSelfManagedSchedulingValue(schedulingKey, value); handled {
			if err != nil {
				return fmt.Errorf("%s: %w", childPath, err)
			}
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			allowedMap, ok := allowedValue.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must not be an object", childPath)
			}
			if err := validateSelfManagedValuesShape(typed, allowedMap, childPath); err != nil {
				return err
			}
		case []any:
			if _, ok := allowedValue.([]any); !ok {
				return fmt.Errorf("%s must not be an array", childPath)
			}
			if err := validateSelfManagedArray(childPath, typed); err != nil {
				return err
			}
		default:
			if !sameSelfManagedScalarType(value, allowedValue) {
				return fmt.Errorf("%s has a different type than the audited chart value", childPath)
			}
			if text, ok := value.(string); ok {
				if err := validateSelfManagedScalarURL(childPath, text); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateSelfManagedScalarURL(path, value string) error {
	if !strings.Contains(value, "://") {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s is not a valid absolute URL", path)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s URL must not contain user info, query parameters, or a fragment", path)
	}
	for _, segment := range strings.Split(strings.ToLower(parsed.EscapedPath()), "/") {
		if strings.Contains(segment, "token=") || strings.Contains(segment, "apikey=") || strings.Contains(segment, "secret=") {
			return fmt.Errorf("%s URL contains a credential-shaped path segment", path)
		}
	}
	return nil
}

func validateSelfManagedSchedulingValue(key string, value any) (bool, error) {
	var target any
	switch key {
	case "nodeselector":
		target = &map[string]string{}
	case "affinity":
		target = &corev1.Affinity{}
	case "tolerations":
		target = &[]corev1.Toleration{}
	case "topologyspreadconstraints":
		target = &[]corev1.TopologySpreadConstraint{}
	case "hostaliases":
		target = &[]corev1.HostAlias{}
	case "resources":
		target = &corev1.ResourceRequirements{}
	default:
		return false, nil
	}
	if isEmptySelfManagedValue(value) {
		return true, nil
	}
	raw, err := yaml.Marshal(value)
	if err != nil {
		return true, err
	}
	if err := yaml.UnmarshalStrict(raw, target); err != nil {
		return true, fmt.Errorf("invalid typed Kubernetes scheduling value: %w", err)
	}
	return true, validateSelfManagedValueNode(value, "scheduling")
}

func validateSelfManagedArray(path string, values []any) error {
	if len(values) == 0 {
		return nil
	}
	parts := strings.Split(path, ".")
	lastKey := normalizedSensitiveKey(parts[len(parts)-1])
	if path == "image.pullSecrets" || lastKey == "imagepullsecrets" {
		for i, item := range values {
			entry, ok := item.(map[string]any)
			if !ok || len(entry) != 1 {
				return fmt.Errorf("image.pullSecrets[%d] must contain only name", i)
			}
			name, ok := entry["name"].(string)
			if !ok || len(kvalidation.IsDNS1123Subdomain(name)) > 0 {
				return fmt.Errorf("image.pullSecrets[%d].name must be non-empty", i)
			}
		}
		return nil
	}
	if path == "gateway.hosts" {
		for i, item := range values {
			host, ok := item.(string)
			if !ok || host == "" || strings.ContainsAny(host, "@/?# ") {
				return fmt.Errorf("gateway.hosts[%d] is not a hostname", i)
			}
		}
		return nil
	}
	if strings.HasSuffix(path, "EgressCIDRs") {
		for i, item := range values {
			cidr, ok := item.(string)
			if !ok {
				return fmt.Errorf("%s[%d] must be a CIDR", path, i)
			}
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("%s[%d] must be a CIDR", path, i)
			}
		}
		return nil
	}
	return fmt.Errorf("non-empty array %s is not reference-backable", path)
}

func forbiddenSelfManagedFreeformKey(key string) bool {
	switch key {
	case "envs", "extraenv", "extraenvs", "extraobjects", "extracontainers",
		"initcontainers", "annotations", "podannotations", "deploymentannotations",
		"statefulsetannotations", "headers", "volumes", "volumemounts":
		return true
	}
	return false
}

func sameSelfManagedScalarType(value, allowed any) bool {
	if value == nil || allowed == nil {
		return value == nil && allowed == nil
	}
	switch value.(type) {
	case string:
		_, ok := allowed.(string)
		return ok
	case bool:
		_, ok := allowed.(bool)
		return ok
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		switch allowed.(type) {
		case float64, float32, int, int32, int64, uint, uint32, uint64:
			return true
		}
	}
	return false
}

func isEmptySelfManagedValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case map[string]any:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}

func deepMergeSelfManagedValues(destination, overlay map[string]any) {
	for key, value := range overlay {
		if overlayMap, ok := value.(map[string]any); ok {
			if destinationMap, ok := destination[key].(map[string]any); ok {
				deepMergeSelfManagedValues(destinationMap, overlayMap)
				continue
			}
		}
		destination[key] = value
	}
}

func discoverCoreCredentialSecret(ctx context.Context, k8s kubernetes.Interface, server *appsv1.Deployment) (*corev1.Secret, selfManagedSecretRef, selfManagedSecretRef, error) {
	secretKeyRef, secretOK := deploymentEnvSecretRef(server, "server", "SECRET_KEY")
	encryptionKeyRef, encryptionOK := deploymentEnvSecretRef(server, "server", "ASTRONOMER_ENCRYPTION_KEY")
	if secretOK || encryptionOK {
		if !secretOK || !encryptionOK || secretKeyRef.Name != encryptionKeyRef.Name {
			return nil, selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("server core credential references must use one Secret and include both required keys")
		}
		secret, err := k8s.CoreV1().Secrets(localAstronomerNamespace).Get(ctx, secretKeyRef.Name, metav1.GetOptions{})
		if err != nil {
			return nil, selfManagedSecretRef{}, selfManagedSecretRef{}, err
		}
		if len(secret.Data[secretKeyRef.Key]) == 0 || len(secret.Data[encryptionKeyRef.Key]) == 0 {
			return nil, selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("core credential Secret %s is missing a required referenced key", secret.Name)
		}
		return secret, secretKeyRef, encryptionKeyRef, nil
	}
	// Legacy chart revisions exposed the core Secret only through envFrom.
	for _, container := range server.Spec.Template.Spec.Containers {
		if container.Name != "server" {
			continue
		}
		for _, source := range container.EnvFrom {
			if source.SecretRef == nil || strings.TrimSpace(source.SecretRef.Name) == "" {
				continue
			}
			secret, err := k8s.CoreV1().Secrets(localAstronomerNamespace).Get(ctx, source.SecretRef.Name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			if len(secret.Data["SECRET_KEY"]) > 0 && len(secret.Data["ASTRONOMER_ENCRYPTION_KEY"]) > 0 {
				return secret,
					selfManagedSecretRef{Name: secret.Name, Key: "SECRET_KEY"},
					selfManagedSecretRef{Name: secret.Name, Key: "ASTRONOMER_ENCRYPTION_KEY"}, nil
			}
		}
	}
	return nil, selfManagedSecretRef{}, selfManagedSecretRef{}, fmt.Errorf("server Deployment has no complete core credential Secret reference")
}
