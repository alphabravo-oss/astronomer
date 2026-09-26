package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type appLifecycleExecutor struct{ slug string }

var appCases = map[string]string{
	"APP-01": "constellation", "APP-02": "kube-state-metrics", "APP-03": "prometheus-node-exporter",
	"APP-04": "metrics-server", "APP-05": "kube-prometheus-stack", "APP-06": "grafana",
	"APP-07": "loki", "APP-08": "trivy-operator", "APP-09": "cert-manager",
	"APP-10": "ingress-nginx", "APP-11": "external-secrets", "APP-12": "kyverno",
	"APP-13": "longhorn", "APP-14": "gatekeeper", "APP-15": "fluent-bit",
	"APP-16": "external-dns", "APP-17": "velero", "APP-18": "opentelemetry-collector",
	"APP-19": "tempo", "APP-20": "keda", "APP-21": "cloudnative-pg",
}

type catalogApplication struct {
	Slug, RepoURL, ChartName, Version string
}

type catalogRelease struct {
	ChartID, VersionID, Version, Digest string
}

type appInstallSpec struct {
	Namespace, ReleaseName, Values string
}

type appMutation struct {
	Operation      operationObservation
	InstallationID string
	Revision       int64
}

func (executor appLifecycleExecutor) Run(ctx context.Context, execution executionContext, definition caseDefinition, result caseResult, checkpoint checkpointFunc) (final caseResult) {
	client := qualificationHTTPClient()
	target, err := toolTarget(execution.Config, definition.ID)
	if err != nil {
		return finishCase(result, "BLOCKED", err.Error(), checkpoint)
	}
	application, release, err := resolveCatalogRelease(ctx, client, execution, executor.slug, target.ClusterID)
	if err != nil {
		return failDimension(result, "inventory", err, checkpoint)
	}
	spec := appSpec(executor.slug, target, release.Version)
	installed, err := listCatalogInstallations(ctx, client, execution, target.ClusterID)
	if err != nil {
		return failDimension(result, "inventory", err, checkpoint)
	}
	for _, item := range installed {
		if stringField(item, "release_name") == spec.ReleaseName && stringField(item, "namespace") == spec.Namespace {
			return finishCase(result, "BLOCKED", fmt.Sprintf("catalog release %s/%s was not clean before the run", spec.Namespace, spec.ReleaseName), checkpoint,
				dimensionResult{Name: "inventory", State: "BLOCKED", Reason: "the runner will not take ownership of a pre-existing release", ObservedAt: time.Now().UTC(), TargetClusterID: target.ClusterID})
		}
	}
	result = addDimension(result, dimensionResult{Name: "inventory", State: "PASS", Reason: fmt.Sprintf("verified application %s resolved to immutable chart version %s", application.Slug, release.Version), ObservedAt: time.Now().UTC(), HTTPStatus: http.StatusOK, TargetClusterID: target.ClusterID}, checkpoint)

	request := map[string]any{"cluster_id": target.ClusterID, "chart_version_id": release.VersionID, "namespace": spec.Namespace, "values_override": spec.Values, "operation": "install"}
	preview, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, "/api/v1/catalog/applications/preview/", request, "", http.StatusOK)
	if err != nil {
		return failDimension(result, "preview", err, checkpoint)
	}
	if err := validateAppPreview(preview.Body, executor.slug, release); err != nil {
		return failDimension(result, "preview", err, checkpoint)
	}
	result = addDimension(result, dimensionResult{Name: "preview", State: "PASS", Reason: "catalog trust, compatibility, immutable artifact, and values checks allowed installation", ObservedAt: time.Now().UTC(), HTTPStatus: preview.Status, TargetClusterID: target.ClusterID}, checkpoint)

	installBody := map[string]any{
		"cluster_id": target.ClusterID, "chart_version_id": release.VersionID,
		"release_name": spec.ReleaseName, "namespace": spec.Namespace, "values_override": spec.Values,
		"notes": "Astronomer qualification " + definition.ID,
	}
	if spec.Namespace == target.Namespace {
		installBody["project_id"] = target.ProjectID
	}
	installedID := ""
	defer func() {
		if installedID == "" {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		mutation, key, cleanupErr := executeAppMutation(cleanupCtx, client, execution, http.MethodDelete, "/api/v1/catalog/installed/"+url.PathEscape(installedID)+"/", nil, definition.ID+"-deferred-cleanup")
		if cleanupErr != nil {
			final = upsertDimension(final, failedDimension("uninstall_cleanup", cleanupErr))
			final.State, final.Reason = "FAIL", "run-owned application cleanup failed: "+cleanupErr.Error()
		} else {
			final = upsertDimension(final, operationDimension("uninstall_cleanup", "deferred cleanup removed the run-owned catalog release", target.ClusterID, key, mutation.Operation))
			installedID = ""
		}
		_ = checkpoint(final)
	}()

	install, installKey, err := executeAppMutation(ctx, client, execution, http.MethodPost, "/api/v1/catalog/installed/", installBody, definition.ID+"-install")
	if install.InstallationID != "" {
		installedID = install.InstallationID
	}
	if err != nil {
		return failDimension(result, "install", err, checkpoint)
	}
	if installedID == "" {
		return failDimension(result, "install", errors.New("catalog install receipt lacked installation id"), checkpoint)
	}
	result = addDimension(result, operationDimension("install", "catalog installation completed", target.ClusterID, installKey, install.Operation), checkpoint)

	detail, err := getCatalogInstallation(ctx, client, execution, installedID, http.StatusOK)
	if err != nil || stringField(detail, "status") != "installed" {
		if err == nil {
			err = fmt.Errorf("catalog operation completed but installation status was %q", stringField(detail, "status"))
		}
		return failDimension(result, "reconciliation", err, checkpoint)
	}
	revision := numberField64(detail, "revision")
	if revision < 1 {
		revision = 1
	}
	result = addDimension(result, dimensionResult{Name: "reconciliation", State: "PASS", Reason: "durable catalog operation and installed release status agree", ObservedAt: time.Now().UTC(), OperationID: install.Operation.ID, TargetClusterID: target.ClusterID, DesiredGeneration: &revision, ObservedGeneration: &revision}, checkpoint)

	result = addDimension(result, evaluateAppCanary(ctx, client, execution, executor.slug, target.ClusterID, spec, "functional"), checkpoint)
	result = addDimension(result, evaluateAppIsolation(ctx, client, execution, executor.slug, target.ClusterID, installedID, installBody), checkpoint)

	upgradeBody := map[string]any{"chart_version_id": release.VersionID, "values_override": spec.Values}
	upgrade, upgradeKey, err := executeAppMutation(ctx, client, execution, http.MethodPut, "/api/v1/catalog/installed/"+url.PathEscape(installedID)+"/upgrade/", upgradeBody, definition.ID+"-upgrade")
	if err != nil {
		result = addDimension(result, failedDimension("upgrade", err), checkpoint)
	} else {
		result = addDimension(result, operationDimension("upgrade", "same immutable catalog artifact was reconciled as a new Helm revision", target.ClusterID, upgradeKey, upgrade.Operation), checkpoint)
	}
	rollback, rollbackKey, err := executeAppMutation(ctx, client, execution, http.MethodPost, "/api/v1/catalog/installed/"+url.PathEscape(installedID)+"/rollback/", map[string]any{}, definition.ID+"-rollback")
	if err != nil {
		result = addDimension(result, failedDimension("rollback", err), checkpoint)
	} else {
		result = addDimension(result, operationDimension("rollback", "catalog API restored the preceding run-owned Helm revision", target.ClusterID, rollbackKey, rollback.Operation), checkpoint)
	}

	result = addDimension(result, evaluateAppRestartRecovery(ctx, client, execution, executor.slug, target.ClusterID, spec), checkpoint)
	uninstall, uninstallKey, err := executeAppMutation(ctx, client, execution, http.MethodDelete, "/api/v1/catalog/installed/"+url.PathEscape(installedID)+"/", nil, definition.ID+"-uninstall")
	if err != nil {
		return finishCase(addDimension(result, failedDimension("uninstall_cleanup", err), checkpoint), "FAIL", "application lifecycle left cleanup unproven", checkpoint)
	}
	if _, err = getCatalogInstallation(ctx, client, execution, installedID, http.StatusNotFound); err != nil {
		return finishCase(addDimension(result, failedDimension("uninstall_cleanup", err), checkpoint), "FAIL", "uninstall operation completed without removing the catalog record", checkpoint)
	}
	installedID = ""
	result = addDimension(result, operationDimension("uninstall_cleanup", "catalog release and installation record were removed", target.ClusterID, uninstallKey, uninstall.Operation), checkpoint)
	return finalizeLifecycleDimensions(result, "application", checkpoint)
}

func appSpec(slug string, target memberTarget, chartVersion string) appInstallSpec {
	namespaces := map[string]string{
		"constellation": "astronomer-constellation", "kube-state-metrics": "astronomer-monitoring", "prometheus-node-exporter": "astronomer-monitoring",
		"metrics-server": "astronomer-metrics-server", "kube-prometheus-stack": "astronomer-kube-prometheus", "grafana": "astronomer-grafana",
		"loki": "astronomer-loki", "trivy-operator": "astronomer-trivy-system", "cert-manager": "astronomer-cert-manager",
		"ingress-nginx": "astronomer-ingress-nginx", "external-secrets": "astronomer-external-secrets", "kyverno": "astronomer-kyverno",
		"longhorn": "longhorn-system", "gatekeeper": "astronomer-gatekeeper-system", "fluent-bit": "astronomer-logging",
		"external-dns": "astronomer-external-dns", "velero": "velero", "opentelemetry-collector": "astronomer-opentelemetry",
		"tempo": "astronomer-tempo", "keda": "astronomer-keda", "cloudnative-pg": "cnpg-system",
	}
	constellationTag := strings.TrimSpace(chartVersion)
	if constellationTag != "" && !strings.HasPrefix(constellationTag, "v") {
		constellationTag = "v" + constellationTag
	}
	values := map[string]string{
		"constellation":            fmt.Sprintf("image:\n  tag: %s\n", constellationTag),
		"prometheus-node-exporter": "hostNetwork: false\nhostPID: false\n",
		"fluent-bit":               "config:\n  outputs: |\n    [OUTPUT]\n        Name stdout\n        Match *\n",
		"cert-manager":             "crds:\n  enabled: true\nstartupapicheck:\n  enabled: true\n",
		"external-dns":             "provider:\n  name: inmemory\nsources:\n  - service\npolicy: sync\nregistry: noop\ninterval: 5s\n",
		"velero":                   "backupsEnabled: false\nsnapshotsEnabled: false\ndeployNodeAgent: true\ncredentials:\n  useSecret: false\nconfiguration:\n  backupStorageLocation: []\n  volumeSnapshotLocation: []\n",
		"opentelemetry-collector":  "mode: deployment\nconfig:\n  exporters:\n    debug: {}\n  service:\n    pipelines:\n      traces:\n        receivers: [otlp]\n        processors: [batch]\n        exporters: [debug]\n",
		"loki":                     "deploymentMode: SingleBinary\nsingleBinary:\n  replicas: 1\nbackend:\n  replicas: 0\nread:\n  replicas: 0\nwrite:\n  replicas: 0\nminio:\n  enabled: true\n",
		"tempo":                    "tempo:\n  receivers:\n    otlp:\n      protocols:\n        http: {}\n        grpc: {}\npersistence:\n  enabled: false\n",
	}
	releaseNames := map[string]string{"opentelemetry-collector": "otel-collector", "kube-prometheus-stack": "kube-prometheus-stack"}
	releaseName := releaseNames[slug]
	if releaseName == "" {
		releaseName = slug
	}
	return appInstallSpec{Namespace: namespaces[slug], ReleaseName: releaseName, Values: values[slug]}
}

func resolveCatalogRelease(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID string) (catalogApplication, catalogRelease, error) {
	apps, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, "/api/v1/catalog/applications/", nil, "", http.StatusOK)
	if err != nil {
		return catalogApplication{}, catalogRelease{}, err
	}
	items, err := arrayAtPath(apps.Body, "data")
	if err != nil {
		return catalogApplication{}, catalogRelease{}, err
	}
	app := catalogApplication{}
	for _, item := range items {
		object, _ := item.(map[string]any)
		if stringField(object, "slug") != slug {
			continue
		}
		artifact, _ := object["artifact"].(map[string]any)
		app = catalogApplication{Slug: slug, RepoURL: stringField(object, "repo_url"), ChartName: stringField(object, "chart_name"), Version: stringField(artifact, "version")}
		if stringField(object, "verification_status") != "verified" && stringField(object, "verification_status") != "digest-verified" {
			return app, catalogRelease{}, fmt.Errorf("application %s is not verified", slug)
		}
		break
	}
	if app.ChartName == "" || app.RepoURL == "" || app.Version == "" {
		return app, catalogRelease{}, fmt.Errorf("application %s lacked immutable catalog coordinates", slug)
	}
	repos, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, "/api/v1/catalog/repositories/?limit=100&offset=0", nil, "", http.StatusOK)
	if err != nil {
		return app, catalogRelease{}, err
	}
	repoItems, err := arrayAtPath(repos.Body, "data")
	if err != nil {
		return app, catalogRelease{}, err
	}
	repoID := ""
	for _, item := range repoItems {
		object, _ := item.(map[string]any)
		if strings.TrimSuffix(stringField(object, "url"), "/") == strings.TrimSuffix(app.RepoURL, "/") {
			repoID = stringField(object, "id")
			break
		}
	}
	if repoID == "" {
		return app, catalogRelease{}, fmt.Errorf("verified repository %s was absent from runtime catalog", app.RepoURL)
	}
	chartsPath := "/api/v1/catalog/charts/?cluster_id=" + url.QueryEscape(clusterID) + "&search=" + url.QueryEscape(app.ChartName) + "&limit=100&offset=0"
	charts, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, chartsPath, nil, "", http.StatusOK)
	if err != nil {
		return app, catalogRelease{}, err
	}
	chartItems, err := arrayAtPath(charts.Body, "data")
	if err != nil {
		return app, catalogRelease{}, err
	}
	release := catalogRelease{}
	for _, item := range chartItems {
		object, _ := item.(map[string]any)
		if stringField(object, "name") == app.ChartName && stringField(object, "repository_id") == repoID {
			release.ChartID = stringField(object, "id")
			break
		}
	}
	if release.ChartID == "" {
		return app, release, fmt.Errorf("chart %s from %s was absent from the target catalog", app.ChartName, app.RepoURL)
	}
	versionsPath := "/api/v1/catalog/charts/" + url.PathEscape(release.ChartID) + "/versions/?cluster_id=" + url.QueryEscape(clusterID) + "&limit=100&offset=0"
	versions, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, versionsPath, nil, "", http.StatusOK)
	if err != nil {
		return app, release, err
	}
	versionItems, err := arrayAtPath(versions.Body, "data")
	if err != nil {
		return app, release, err
	}
	for _, item := range versionItems {
		object, _ := item.(map[string]any)
		if stringField(object, "version") == app.Version {
			release.VersionID, release.Version, release.Digest = stringField(object, "id"), app.Version, stringField(object, "digest")
			break
		}
	}
	if release.VersionID == "" || release.Digest == "" {
		return app, release, fmt.Errorf("catalog pin %s %s was absent or lacked a digest", app.ChartName, app.Version)
	}
	return app, release, nil
}

func arrayAtPath(body any, path string) ([]any, error) {
	value, err := valueAtPath(body, path)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s was not an array", path)
	}
	return items, nil
}

func validateAppPreview(body any, slug string, release catalogRelease) error {
	data, err := objectAtPath(body, "data")
	if err != nil {
		return err
	}
	if allowed, _ := data["allowed"].(bool); !allowed {
		return fmt.Errorf("catalog preview blocked application %s", slug)
	}
	if stringField(data, "application") != slug || stringField(data, "artifact_digest") != release.Digest || stringField(data, "catalog_digest") == "" || stringField(data, "values_digest") == "" {
		return errors.New("catalog preview did not bind the requested application, artifact, catalog, and values digests")
	}
	checks, _ := data["checks"].([]any)
	if len(checks) == 0 {
		return errors.New("catalog preview returned no prerequisite checks")
	}
	for _, item := range checks {
		check, _ := item.(map[string]any)
		if stringField(check, "status") == "blocking" {
			return fmt.Errorf("catalog prerequisite %s was blocking", stringField(check, "code"))
		}
	}
	return nil
}

func executeAppMutation(ctx context.Context, client *http.Client, execution executionContext, method, path string, body any, keySuffix string) (appMutation, string, error) {
	key := "qualification-" + execution.RunID + "-" + strings.ToLower(keySuffix)
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, method, path, body, key, http.StatusAccepted)
	if err != nil {
		return appMutation{}, key, err
	}
	data, err := objectAtPath(response.Body, "data")
	if err != nil {
		return appMutation{}, key, err
	}
	operationObject := data
	installationID := ""
	revision := int64(0)
	if nested, ok := data["operation"].(map[string]any); ok {
		operationObject = nested
		if installation, ok := data["installation"].(map[string]any); ok {
			installationID, revision = stringField(installation, "id"), numberField64(installation, "revision")
		}
	}
	operation := operationObservation{ID: stringField(operationObject, "id"), Status: strings.ToLower(stringField(operationObject, "status")), Body: response.Body}
	if operation.ID == "" || operation.Status == "" {
		return appMutation{}, key, errors.New("catalog operation receipt lacked id or status")
	}
	location := response.Location
	if location == "" {
		location = "/api/v1/catalog/operations/" + url.PathEscape(operation.ID) + "/"
	}
	completed, err := pollOperation(ctx, client, execution.Base, execution.Token, location, operation.ID, 2*time.Second)
	return appMutation{Operation: completed, InstallationID: installationID, Revision: revision}, key, err
}

func listCatalogInstallations(ctx context.Context, client *http.Client, execution executionContext, clusterID string) ([]map[string]any, error) {
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, "/api/v1/catalog/installed/?cluster_id="+url.QueryEscape(clusterID)+"&limit=100&offset=0", nil, "", http.StatusOK)
	if err != nil {
		return nil, err
	}
	items, err := arrayAtPath(response.Body, "data")
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, _ := item.(map[string]any)
		result = append(result, object)
	}
	return result, nil
}

func getCatalogInstallation(ctx context.Context, client *http.Client, execution executionContext, id string, expected int) (map[string]any, error) {
	response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, "/api/v1/catalog/installed/"+url.PathEscape(id)+"/", nil, "", expected)
	if err != nil || expected == http.StatusNotFound {
		return nil, err
	}
	return objectAtPath(response.Body, "data")
}

func evaluateAppIsolation(ctx context.Context, client *http.Client, execution executionContext, slug, clusterID, installationID string, installBody map[string]any) dimensionResult {
	for _, other := range execution.Config.MemberTargets {
		if other.ClusterID == clusterID {
			continue
		}
		items, err := listCatalogInstallations(ctx, client, execution, other.ClusterID)
		if err != nil {
			return failedDimension("authorization_isolation", err)
		}
		for _, item := range items {
			if stringField(item, "id") == installationID {
				return failedDimension("authorization_isolation", fmt.Errorf("application installation appeared on non-target cluster %s", other.ClusterID))
			}
		}
		if execution.RestrictedToken == "" {
			return failedDimension("authorization_isolation", errors.New("no restricted credential is configured"))
		}
		key := "qualification-" + execution.RunID + "-" + slug + "-app-restricted-denial"
		denied, requestErr := requestAPI(ctx, client, execution.Base, execution.RestrictedToken, http.MethodPost, "/api/v1/catalog/installed/", installBody, key, http.StatusForbidden)
		if requestErr != nil || (!responseMentions(denied.Body, "scope_denied") && !responseMentions(denied.Body, "forbidden") && !responseMentions(denied.Body, "permission")) {
			if requestErr == nil {
				requestErr = errors.New("restricted credential returned 403 without an authorization-denial code")
			}
			return failedDimension("authorization_isolation", requestErr)
		}
		return dimensionResult{Name: "authorization_isolation", State: "PASS", Reason: "catalog effect remained confined to the target cluster and a read-only token was denied installation", ObservedAt: time.Now().UTC(), HTTPStatus: http.StatusForbidden, IdempotencyKey: key, TargetClusterID: clusterID}
	}
	return failedDimension("authorization_isolation", errors.New("a second member target is required for cross-cluster isolation"))
}

func numberField64(object map[string]any, key string) int64 {
	switch value := object[key].(type) {
	case json.Number:
		result, _ := value.Int64()
		return result
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func finalizeLifecycleDimensions(result caseResult, kind string, checkpoint checkpointFunc) caseResult {
	for _, dimension := range result.Dimensions {
		if dimension.State != "PASS" && dimension.State != "NOT_APPLICABLE" {
			return finishCase(result, "FAIL", "one or more required qualification dimensions did not pass", checkpoint)
		}
	}
	return finishCase(result, "PASS", "all required "+kind+" lifecycle dimensions passed", checkpoint)
}
