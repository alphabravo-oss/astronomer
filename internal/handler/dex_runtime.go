package handler

// Phase B4 — Dex shim for enterprise auth.
//
// Astronomer-go itself only ever speaks generic OIDC (see internal/auth/oauth.go's
// RegisterOIDCProvider path that landed in Phase A1). Dex brokers the messy IdPs
// — Azure AD, LDAP, SAML, Okta, GitLab, etc. — and exposes a single OIDC issuer
// our SSO manager can register against.
//
// This file owns:
//   * CRUD for `dex_connectors` (one row per upstream IdP connector).
//   * Singleton settings for the running Dex deployment (issuer URL, namespace,
//     retained runtime Secret name, static clients, expiry).
//   * A connector-type registry mapping each connector kind to its required +
//     optional + secret config fields. Validation runs against this registry on
//     every write.
//   * Rendering settings + connectors into a Dex-shaped YAML document stored
//     only in a retained Kubernetes Secret mounted read-only by Dex.
//   * `register-as-sso` ergonomics: one-click row in `sso_configurations` so the
//     A1 OIDC discovery path can register Dex as a normal OIDC provider.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
)

func (h *DexHandler) connectorResponse(row sqlc.DexConnector) (map[string]any, error) {
	cfg := decodeJSONMap(row.Config)
	if err := validateConnectorConfig(row.Type, cfg); err != nil {
		return nil, err
	}
	out := map[string]any{
		"id":           row.ID.String(),
		"name":         row.Name,
		"type":         row.Type,
		"display_name": row.DisplayName,
		"enabled":      row.Enabled,
		"config":       redactSecretFields(row.Type, cfg),
		"created_at":   row.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":   row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	return out, nil
}

func defaultSettingsResponse() map[string]any {
	return map[string]any{
		"issuer_url":          "",
		"cluster_id":          "",
		"namespace":           "dex",
		"release_name":        "dex",
		"chart_release_name":  "",
		"deployment_name":     "dex",
		"service_name":        "dex",
		"runtime_secret_name": "astronomer-dex-runtime",
		"public_clients":      []any{},
		"expiry":              map[string]any{},
		"extra":               map[string]any{},
		"configured":          false,
	}
}

func settingsResponse(row sqlc.DexSetting, clients []map[string]any) (map[string]any, error) {
	expiry, extra, err := validatedDexExtensions(row.Expiry, row.Extra)
	if err != nil {
		return nil, err
	}
	clusterID := ""
	if row.ClusterID.Valid {
		clusterID = uuid.UUID(row.ClusterID.Bytes).String()
	}
	return map[string]any{
		"issuer_url":                 row.IssuerUrl,
		"cluster_id":                 clusterID,
		"namespace":                  row.Namespace,
		"release_name":               row.ReleaseName,
		"chart_release_name":         row.ChartReleaseName,
		"deployment_name":            row.DeploymentName,
		"service_name":               row.ServiceName,
		"runtime_secret_name":        row.RuntimeSecretName,
		"public_clients":             redactPublicClients(clients),
		"expiry":                     expiry,
		"extra":                      extra,
		"configured":                 true,
		"runtime_generation":         row.RuntimeGeneration,
		"runtime_phase":              row.RuntimePhase,
		"runtime_staged_generation":  row.RuntimeStagedGeneration,
		"runtime_applied_generation": row.RuntimeAppliedGeneration,
		"updated_at":                 row.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

// renderDexConfig builds a Dex-shaped config document and serializes it to
// YAML. The output goes verbatim into the runtime Secret's `config.yaml` key. We
// keep this in handler-package code (rather than a sub-package) so tests can
// exercise it without exporting helpers.
func (h *DexHandler) renderDexConfig(settings sqlc.DexSetting, publicClients []map[string]any, connectors []sqlc.DexConnector) ([]byte, error) {
	if err := validateCanonicalDexURL(settings.IssuerUrl, true); err != nil {
		return nil, fmt.Errorf("stored Dex issuer URL is invalid")
	}
	if err := validatePublicClients(publicClients); err != nil && len(publicClients) > 0 {
		return nil, fmt.Errorf("stored Dex static clients are invalid")
	}
	expiry, extra, err := validatedDexExtensions(settings.Expiry, settings.Extra)
	if err != nil {
		return nil, fmt.Errorf("stored Dex extension settings are invalid")
	}
	doc := map[string]any{
		"issuer": settings.IssuerUrl,
		"storage": map[string]any{
			"type": "kubernetes",
			"config": map[string]any{
				"inCluster": true,
			},
		},
		"web": map[string]any{
			"http": "0.0.0.0:5556",
		},
		"oauth2": map[string]any{
			"skipApprovalScreen": true,
		},
	}
	// Public + static clients: the operator can supply both from settings
	// (public_clients is a list of {id, name, redirectURIs, secret?, public?}).
	clients := make([]any, 0, len(publicClients))
	for _, client := range publicClients {
		clients = append(clients, client)
	}
	if len(clients) > 0 {
		doc["staticClients"] = clients
	}
	if len(expiry) > 0 {
		doc["expiry"] = expiry
	}
	for k, v := range extra {
		doc[k] = v
	}
	out := make([]map[string]any, 0, len(connectors))
	for _, c := range connectors {
		raw := decodeJSONMap(c.Config)
		if err := validateConnectorConfig(c.Type, raw); err != nil {
			return nil, fmt.Errorf("stored connector %q is invalid", c.Name)
		}
		if err := h.decryptSecretFields(c.Type, raw); err != nil {
			return nil, fmt.Errorf("connector %q: %w", c.Name, err)
		}
		canonical, canonicalErr := dexconfig.CanonicalConnectorType(c.Type)
		if canonicalErr != nil {
			return nil, canonicalErr
		}
		if spec, ok := dexConnectorRegistry[canonical]; ok && containsField(spec.Optional, "redirectURI") {
			if isEmptyValue(raw["redirectURI"]) {
				raw["redirectURI"] = strings.TrimRight(settings.IssuerUrl, "/") + "/callback"
			}
		}
		out = append(out, map[string]any{
			"type":   c.Type,
			"id":     c.Name,
			"name":   cmp.Or(c.DisplayName, c.Name),
			"config": raw,
		})
	}
	doc["connectors"] = out
	buf := &bytes.Buffer{}
	yamlBytes, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal dex config: %w", err)
	}
	if err := dexconfig.ValidateRuntimeYAML(yamlBytes, 1<<20); err != nil {
		return nil, fmt.Errorf("validate rendered Dex config: %w", err)
	}
	buf.Write(yamlBytes)
	return buf.Bytes(), nil
}

func validateDexExtra(extra map[string]any) error {
	return dexconfig.ValidateExtra(extra)
}

func validateDexExpiry(expiry map[string]any) error {
	return dexconfig.ValidateExpiry(expiry)
}

func validatedDexExtensions(expiryRaw, extraRaw json.RawMessage) (map[string]any, map[string]any, error) {
	decode := func(raw json.RawMessage) (map[string]any, error) {
		out := map[string]any{}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return out, nil
		}
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	expiry, err := decode(expiryRaw)
	if err != nil || validateDexExpiry(expiry) != nil {
		return nil, nil, fmt.Errorf("invalid expiry")
	}
	extra, err := decode(extraRaw)
	if err != nil || validateDexExtra(extra) != nil {
		return nil, nil, fmt.Errorf("invalid extra")
	}
	return expiry, extra, nil
}

func containsField(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

const (
	dexRuntimeManagedByLabel       = "astronomer.io/runtime-writer"
	dexRuntimePurposeLabel         = "astronomer.io/secret-purpose"
	dexRuntimeGenerationAnnotation = "astronomer.io/dex-runtime-generation"
)

type dexRuntimeSecret struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		ResourceVersion string            `json:"resourceVersion,omitempty"`
		Labels          map[string]string `json:"labels,omitempty"`
		Annotations     map[string]string `json:"annotations,omitempty"`
	} `json:"metadata"`
	Type string            `json:"type"`
	Data map[string]string `json:"data"`
}

type dexDeployment struct {
	Metadata struct {
		Generation      int64  `json:"generation"`
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Spec struct {
		Replicas int32 `json:"replicas"`
		Template struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Volumes []struct {
					Name   string `json:"name"`
					Secret *struct {
						SecretName string `json:"secretName"`
					} `json:"secret,omitempty"`
				} `json:"volumes"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration  int64 `json:"observedGeneration"`
		UpdatedReplicas     int32 `json:"updatedReplicas"`
		ReadyReplicas       int32 `json:"readyReplicas"`
		AvailableReplicas   int32 `json:"availableReplicas"`
		UnavailableReplicas int32 `json:"unavailableReplicas"`
		Conditions          []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

// applyRuntimeSecret creates or resourceVersion-replaces the stable Secret
// mounted by Dex. Existing data is compared in memory for a fixed point;
// no content-derived hash is persisted because static-client secrets may have
// low entropy. Identical repeat applies do not mutate or roll the Deployment.
// A pre-existing Secret must carry our ownership labels; this prevents a name
// collision from silently overwriting an operator-owned credential.
func (h *DexHandler) applyRuntimeSecret(ctx context.Context, clusterID, namespace, name string, configYAML []byte, generations ...int64) (bool, string, error) {
	if strings.TrimSpace(name) == "" {
		return false, "", fmt.Errorf("Dex runtime_secret_name is empty")
	}
	generation := int64(1)
	if len(generations) > 0 {
		generation = generations[0]
	}
	if generation <= 0 {
		return false, "", fmt.Errorf("Dex runtime generation is invalid")
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return false, "", err
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if generationErr := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); generationErr != nil {
		return false, "", generationErr
	}
	if err != nil {
		return false, "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, "", fmt.Errorf("Dex runtime Secret %s/%s does not exist; install or upgrade the chart to create the retained metadata-only Secret, then retry apply", namespace, name)
	}
	if err := ensureSuccess(resp); err != nil {
		return false, "", err
	}
	var existing dexRuntimeSecret
	if err := parseJSONResponse(resp, &existing); err != nil {
		return false, "", fmt.Errorf("decode existing Dex runtime Secret: %w", err)
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return false, "", err
	}
	if existing.Metadata.Labels[dexRuntimeManagedByLabel] != "dex-handler" ||
		existing.Metadata.Labels[dexRuntimePurposeLabel] != "dex-runtime" {
		return false, "", fmt.Errorf("refusing to overwrite Secret %s/%s without Dex runtime ownership labels", namespace, name)
	}
	existingGeneration, _ := strconv.ParseInt(existing.Metadata.Annotations[dexRuntimeGenerationAnnotation], 10, 64)
	if existingGeneration > generation {
		return false, "", fmt.Errorf("Dex runtime generation is stale; retry from current settings")
	}
	desired := newDexRuntimeSecret(namespace, name, configYAML, generation)
	contentChanged := existing.Data["config.yaml"] != desired.Data["config.yaml"]
	metadataCurrent := existing.Type == desired.Type && containsDexMetadata(existing.Metadata.Labels, desired.Metadata.Labels) &&
		containsDexMetadata(existing.Metadata.Annotations, desired.Metadata.Annotations)
	if existingGeneration == generation && contentChanged {
		return false, "", fmt.Errorf("Dex runtime content changed without a new database generation")
	}
	if !contentChanged && metadataCurrent {
		if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
			return false, "", err
		}
		return false, existing.Metadata.ResourceVersion, nil
	}
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": existing.Metadata.ResourceVersion,
			"labels":          desired.Metadata.Labels,
			"annotations":     desired.Metadata.Annotations,
		},
		"type": desired.Type,
		"data": desired.Data,
	})
	if err != nil {
		return false, "", err
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return false, "", err
	}
	updated, err := h.k8s.Do(ctx, clusterID, http.MethodPatch, path, body, requestHeaders("application/merge-patch+json"))
	if generationErr := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); generationErr != nil {
		return false, "", generationErr
	}
	if err != nil {
		return false, "", err
	}
	if updated.StatusCode == http.StatusConflict {
		return false, "", fmt.Errorf("Dex runtime Secret changed concurrently; retry apply")
	}
	if err := ensureSuccess(updated); err != nil {
		return false, "", err
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return false, "", err
	}
	var result dexRuntimeSecret
	if err := parseJSONResponse(updated, &result); err != nil {
		return false, "", fmt.Errorf("decode updated Dex runtime Secret: %w", err)
	}
	return contentChanged, result.Metadata.ResourceVersion, nil
}

func newDexRuntimeSecret(namespace, name string, configYAML []byte, generations ...int64) dexRuntimeSecret {
	secret := dexRuntimeSecret{
		APIVersion: "v1",
		Kind:       "Secret",
		Type:       "Opaque",
		Data: map[string]string{
			"config.yaml": base64.StdEncoding.EncodeToString(configYAML),
		},
	}
	secret.Metadata.Name = name
	secret.Metadata.Namespace = namespace
	secret.Metadata.Labels = map[string]string{
		dexRuntimeManagedByLabel:              "dex-handler",
		"app.kubernetes.io/component":         "dex-runtime",
		dexRuntimePurposeLabel:                "dex-runtime",
		"astronomer.io/backup-reconstruction": "encrypted-management-db",
	}
	secret.Metadata.Annotations = map[string]string{
		"helm.sh/resource-policy": "keep",
	}
	if len(generations) > 0 {
		secret.Metadata.Annotations[dexRuntimeGenerationAnnotation] = strconv.FormatInt(generations[0], 10)
	}
	return secret
}

func containsDexMetadata(existing, desired map[string]string) bool {
	for key, value := range desired {
		if existing[key] != value {
			return false
		}
	}
	return true
}

func (h *DexHandler) restartDeployment(ctx context.Context, clusterID, namespace, name, secretResourceVersion string, generation int64) error {
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return err
	}
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	currentResponse, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if generationErr := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); generationErr != nil {
		return generationErr
	}
	if err != nil {
		return err
	}
	if err := ensureSuccess(currentResponse); err != nil {
		return err
	}
	var current dexDeployment
	if err := parseJSONResponse(currentResponse, &current); err != nil {
		return fmt.Errorf("decode Dex Deployment before rollout: %w", err)
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return err
	}
	if current.Metadata.ResourceVersion == "" {
		return fmt.Errorf("Dex Deployment has no resourceVersion")
	}
	currentGeneration, _ := strconv.ParseInt(current.Spec.Template.Metadata.Annotations[dexRuntimeGenerationAnnotation], 10, 64)
	if currentGeneration > generation {
		return fmt.Errorf("Dex Deployment runtime generation is newer than the requested rollout")
	}
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": current.Metadata.ResourceVersion,
		},
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"astronomer.io/dex-runtime-resource-version": secretResourceVersion,
						dexRuntimeGenerationAnnotation:               strconv.FormatInt(generation, 10),
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return err
	}
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodPatch, path, body, requestHeaders("application/merge-patch+json"))
	if generationErr := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); generationErr != nil {
		return generationErr
	}
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("Dex Deployment changed concurrently; retry apply")
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	return h.requireDexGeneration(ctx, dexSettingsSingletonID, generation)
}

func (h *DexHandler) verifyDexRuntimeSecret(ctx context.Context, clusterID, namespace, name, resourceVersion string, generation int64, configYAML []byte) error {
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if generationErr := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); generationErr != nil {
		return generationErr
	}
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	var secret dexRuntimeSecret
	if err := parseJSONResponse(resp, &secret); err != nil {
		return fmt.Errorf("decode verified Dex runtime Secret")
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return err
	}
	if secret.Metadata.ResourceVersion != resourceVersion || resourceVersion == "" {
		return fmt.Errorf("Dex runtime Secret resourceVersion changed before rollout")
	}
	if secret.Metadata.Labels[dexRuntimeManagedByLabel] != "dex-handler" || secret.Metadata.Labels[dexRuntimePurposeLabel] != "dex-runtime" {
		return fmt.Errorf("Dex runtime Secret lost required ownership labels")
	}
	if secret.Metadata.Annotations[dexRuntimeGenerationAnnotation] != strconv.FormatInt(generation, 10) {
		return fmt.Errorf("Dex runtime Secret generation changed before rollout")
	}
	if secret.Data["config.yaml"] != base64.StdEncoding.EncodeToString(configYAML) {
		return fmt.Errorf("Dex runtime Secret content changed before rollout")
	}
	return nil
}

func (h *DexHandler) waitForDexDeploymentReady(ctx context.Context, clusterID, namespace, name, runtimeSecretName, resourceVersion string, generation int64) error {
	timeout := h.rolloutTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	poll := h.rolloutPollInterval
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	for {
		if err := h.requireDexGeneration(deadlineCtx, dexSettingsSingletonID, generation); err != nil {
			return err
		}
		resp, err := h.k8s.Do(deadlineCtx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
		if generationErr := h.requireDexGeneration(deadlineCtx, dexSettingsSingletonID, generation); generationErr != nil {
			return generationErr
		}
		if err == nil && ensureSuccess(resp) == nil {
			var deployment dexDeployment
			if parseJSONResponse(resp, &deployment) == nil {
				if err := h.requireDexGeneration(deadlineCtx, dexSettingsSingletonID, generation); err != nil {
					return err
				}
				if dexDeploymentReady(deployment, runtimeSecretName, resourceVersion, generation) {
					return nil
				}
			}
		}
		timer := time.NewTimer(poll)
		select {
		case <-deadlineCtx.Done():
			timer.Stop()
			return fmt.Errorf("Dex Deployment did not become ready with the verified runtime Secret")
		case <-timer.C:
		}
	}
}

func dexDeploymentReady(deployment dexDeployment, runtimeSecretName, resourceVersion string, generation int64) bool {
	if deployment.Metadata.Generation <= 0 || deployment.Status.ObservedGeneration < deployment.Metadata.Generation ||
		deployment.Spec.Replicas <= 0 || deployment.Status.UpdatedReplicas != deployment.Spec.Replicas ||
		deployment.Status.ReadyReplicas != deployment.Spec.Replicas || deployment.Status.AvailableReplicas != deployment.Spec.Replicas ||
		deployment.Status.UnavailableReplicas != 0 || deployment.Spec.Template.Metadata.Annotations["astronomer.io/dex-runtime-resource-version"] != resourceVersion ||
		deployment.Spec.Template.Metadata.Annotations[dexRuntimeGenerationAnnotation] != strconv.FormatInt(generation, 10) {
		return false
	}
	secretMounted := false
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == "config" && volume.Secret != nil && volume.Secret.SecretName == runtimeSecretName {
			secretMounted = true
		}
	}
	available := false
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == "Available" && condition.Status == "True" {
			available = true
		}
	}
	return secretMounted && available
}

func (h *DexHandler) verifyDexHealth(ctx context.Context, clusterID, namespace, serviceName string, generation int64) error {
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:5556/proxy/healthz", namespace, serviceName)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if generationErr := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); generationErr != nil {
		return generationErr
	}
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return fmt.Errorf("Dex health endpoint is not ready")
	}
	return h.requireDexGeneration(ctx, dexSettingsSingletonID, generation)
}

// mergeSecretFromExisting is the partial-update helper for PATCH /connectors/{id}.
// When the request omits a secret field (or sends it empty), keep the existing
// value so the UI can re-save form data without forcing the user to retype the
// secret. Non-secret fields are taken verbatim from req.
func mergeSecretFromExisting(connectorType string, existingRaw json.RawMessage, req map[string]any) map[string]any {
	merged := make(map[string]any, len(req))
	for k, v := range req {
		merged[k] = v
	}
	canonical, err := dexconfig.CanonicalConnectorType(connectorType)
	if err != nil {
		return merged
	}
	spec := dexConnectorRegistry[canonical]
	existing := decodeJSONMap(existingRaw)
	for _, key := range spec.Secret {
		v, ok := req[key]
		s, isStr := v.(string)
		if !ok || (isStr && strings.TrimSpace(s) == "") {
			if prev, ok := existing[key]; ok {
				merged[key] = prev
			}
		}
	}
	return merged
}
