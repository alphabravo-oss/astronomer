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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
)

type dexReconcileResult struct {
	Changed              bool
	SecretVersion, State string
	Staged, Applied      bool
}

func (h *DexHandler) requireDexGeneration(ctx context.Context, id uuid.UUID, generation int64) error {
	if _, err := h.queries.GetDexSettingsForGeneration(ctx, sqlc.GetDexSettingsForGenerationParams{ID: id, RuntimeGeneration: generation}); err != nil {
		return fmt.Errorf("Dex runtime generation is stale or was superseded")
	}
	return nil
}

func (h *DexHandler) reconcileDexRuntime(ctx context.Context, settings sqlc.DexSetting, clients []map[string]any, connectors []sqlc.DexConnector) (dexReconcileResult, error) {
	result := dexReconcileResult{State: "pending"}
	configYAML, err := h.renderDexConfig(settings, clients, connectors)
	if err != nil {
		return result, err
	}
	if err := h.requireDexGeneration(ctx, settings.ID, settings.RuntimeGeneration); err != nil {
		return result, err
	}
	clusterID := uuid.UUID(settings.ClusterID.Bytes).String()
	changed, secretVersion, err := h.applyRuntimeSecret(ctx, clusterID, settings.Namespace, settings.RuntimeSecretName, configYAML, settings.RuntimeGeneration)
	if err != nil {
		return result, err
	}
	result.Changed, result.SecretVersion = changed, secretVersion
	if secretVersion == "" {
		return result, fmt.Errorf("Dex runtime Secret update returned no resourceVersion")
	}
	if err := h.verifyDexRuntimeSecret(ctx, clusterID, settings.Namespace, settings.RuntimeSecretName, secretVersion, settings.RuntimeGeneration, configYAML); err != nil {
		return result, err
	}
	if _, err := h.queries.MarkDexRuntimeStaged(ctx, sqlc.MarkDexRuntimeStagedParams{ID: settings.ID, RuntimeGeneration: settings.RuntimeGeneration}); err != nil {
		return result, fmt.Errorf("Dex runtime generation became stale before staging")
	}
	result.Staged, result.State = true, "staged"
	if settings.RuntimePhase == "prepare" {
		return result, nil
	}
	if err := h.requireDexGeneration(ctx, settings.ID, settings.RuntimeGeneration); err != nil {
		return result, err
	}
	ready := false
	if !changed {
		ready, _ = h.dexDeploymentReadyOnce(ctx, clusterID, settings.Namespace, settings.DeploymentName, settings.RuntimeSecretName, secretVersion, settings.RuntimeGeneration)
	}
	if !ready {
		if err := h.restartDeployment(ctx, clusterID, settings.Namespace, settings.DeploymentName, secretVersion, settings.RuntimeGeneration); err != nil {
			return result, err
		}
		if err := h.waitForDexDeploymentReady(ctx, clusterID, settings.Namespace, settings.DeploymentName, settings.RuntimeSecretName, secretVersion, settings.RuntimeGeneration); err != nil {
			return result, err
		}
	}
	if err := h.verifyDexHealth(ctx, clusterID, settings.Namespace, settings.ServiceName, settings.RuntimeGeneration); err != nil {
		return result, err
	}
	if _, err := h.queries.MarkDexRuntimeApplied(ctx, sqlc.MarkDexRuntimeAppliedParams{ID: settings.ID, RuntimeGeneration: settings.RuntimeGeneration}); err != nil {
		return result, fmt.Errorf("Dex runtime generation became stale before activation")
	}
	result.Applied, result.State = true, "applied"
	return result, nil
}

func (h *DexHandler) dexDeploymentReadyOnce(ctx context.Context, clusterID, namespace, name, runtimeSecretName, resourceVersion string, generation int64) (bool, error) {
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return false, err
	}
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	resp, err := h.k8s.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if generationErr := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); generationErr != nil {
		return false, generationErr
	}
	if err != nil {
		return false, err
	}
	if err := ensureSuccess(resp); err != nil {
		return false, err
	}
	var deployment dexDeployment
	if err := parseJSONResponse(resp, &deployment); err != nil {
		return false, err
	}
	if err := h.requireDexGeneration(ctx, dexSettingsSingletonID, generation); err != nil {
		return false, err
	}
	return dexDeploymentReady(deployment, runtimeSecretName, resourceVersion, generation), nil
}

// RegisterAsSSO is the one-click ergonomic helper that creates (or updates)
// the SSO row pointing at our installed Dex. The frontend can offer this
// once the operator has filled in dex_settings + at least one connector and
// applied them to the cluster.
//
// POST /api/v1/auth/dex/register-as-sso/
//
// Body (optional fields):
//
//	{
//	  "client_id":     "astronomer",
//	  "client_secret": "...plaintext... (will be encrypted)",
//	  "display_name":  "Sign in with Dex"
//	}
func (h *DexHandler) astronomerPublicClients(ctx context.Context, settings sqlc.DexSetting, clientID, clientSecret string) ([]map[string]any, sqlc.DexSetting, error) {
	if h == nil || h.queries == nil {
		return nil, settings, nil
	}
	cfg, err := h.queries.GetPlatformConfig(ctx)
	if err != nil {
		return nil, settings, fmt.Errorf("load platform callback configuration")
	}
	if strings.TrimSpace(cfg.ServerUrl) == "" {
		return nil, settings, fmt.Errorf("platform callback URL is not configured")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.ServerUrl), "/")
	if err := validateCanonicalDexURL(base, true); err != nil {
		return nil, settings, fmt.Errorf("platform callback URL is invalid")
	}
	callback := base + "/api/v1/auth/callback/dex"
	callbackSlash := callback + "/"

	clients, settings, err := h.loadPublicClients(ctx, settings)
	if err != nil {
		return nil, settings, err
	}
	found := false
	for i := range clients {
		id, _ := clients[i]["id"].(string)
		if id != clientID {
			continue
		}
		found = true
		clients[i]["name"] = cmp.Or(asString(clients[i]["name"]), "Astronomer")
		if clientSecret != "" {
			clients[i]["secret"] = clientSecret
		}
		clients[i]["redirectURIs"] = mergeStringList(clients[i]["redirectURIs"], []string{callback, callbackSlash})
	}
	if !found {
		client := map[string]any{
			"id":           clientID,
			"name":         "Astronomer",
			"redirectURIs": []string{callback, callbackSlash},
		}
		if clientSecret != "" {
			client["secret"] = clientSecret
		}
		clients = append(clients, client)
	}

	return clients, settings, nil
}

func (h *DexHandler) encryptPublicClients(clients []map[string]any) (crypto.Sealed, error) {
	if len(clients) == 0 {
		return crypto.Sealed{}, nil
	}
	if h == nil || h.encryptor == nil {
		return crypto.Sealed{}, fmt.Errorf("encrypt Dex static clients: encryptor is not configured")
	}
	raw, err := json.Marshal(clients)
	if err != nil {
		return crypto.Sealed{}, fmt.Errorf("encode Dex static clients: %w", err)
	}
	value, err := h.encryptor.Encrypt(raw)
	clear(raw)
	if err != nil {
		return crypto.Sealed{}, fmt.Errorf("encrypt Dex static clients: %w", err)
	}
	return value, nil
}

// loadPublicClients opens the sealed public_clients family, the only copy of
// the Dex static clients. A zero sealed value is an empty client list.
func (h *DexHandler) loadPublicClients(_ context.Context, row sqlc.DexSetting) ([]map[string]any, sqlc.DexSetting, error) {
	if row.RuntimeSecretName == "" {
		row.RuntimeSecretName = "astronomer-dex-runtime"
	}
	clients := make([]map[string]any, 0)
	if row.PublicClientsKeyID == "" {
		return clients, row, nil
	}
	if h == nil || h.encryptor == nil {
		return nil, row, fmt.Errorf("decrypt Dex static clients: encryptor is not configured")
	}
	plain, err := h.encryptor.Decrypt(crypto.FromParts(row.PublicClientsKeyID, row.PublicClientsAlgorithm, row.PublicClientsCiphertext, row.PublicClientsNonce))
	if err != nil {
		return nil, row, fmt.Errorf("decrypt Dex static clients: %w", err)
	}
	defer clear(plain)
	if len(bytes.TrimSpace(plain)) == 0 || bytes.Equal(bytes.TrimSpace(plain), []byte("null")) {
		return clients, row, nil
	}
	if err := json.Unmarshal(plain, &clients); err != nil {
		return nil, row, fmt.Errorf("decode Dex static clients: %w", err)
	}
	if err := validatePublicClients(clients); err != nil {
		return nil, row, fmt.Errorf("validate Dex static clients")
	}
	return clients, row, nil
}

func mergePublicClientSecrets(existing, requested []map[string]any) []map[string]any {
	secrets := make(map[string]string, len(existing))
	for _, client := range existing {
		if id := asString(client["id"]); id != "" {
			secrets[id] = asString(client["secret"])
		}
	}
	out := make([]map[string]any, 0, len(requested))
	for _, client := range requested {
		copyClient := make(map[string]any, len(client))
		for key, value := range client {
			if key != "secret_configured" && key != "secretConfigured" && key != "__secret_set" {
				copyClient[key] = value
			}
		}
		if asString(copyClient["secret"]) == "" {
			if secret := secrets[asString(copyClient["id"])]; secret != "" {
				copyClient["secret"] = secret
			}
		}
		out = append(out, copyClient)
	}
	return out
}

func validatePublicClients(clients []map[string]any) error {
	return dexconfig.ValidateStaticClients(clients)
}

func redactPublicClients(clients []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(clients))
	for _, client := range clients {
		copyClient := make(map[string]any, len(client)+1)
		for key, value := range client {
			copyClient[key] = value
		}
		if secret := asString(copyClient["secret"]); secret != "" {
			copyClient["secret"] = ""
			copyClient["secret_configured"] = true
		} else {
			delete(copyClient, "secret")
			copyClient["secret_configured"] = false
		}
		out = append(out, sanitizeDexMap(copyClient))
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func mergeStringList(existing any, add []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	switch vals := existing.(type) {
	case []string:
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	case []any:
		for _, raw := range vals {
			v, _ := raw.(string)
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	for _, v := range add {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// connectorResponse builds the JSON shape we return on every connector read.
// Sensitive fields are redacted.
