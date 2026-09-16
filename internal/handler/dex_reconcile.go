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

func (h *DexHandler) encryptPublicClients(clients []map[string]any) (string, error) {
	if len(clients) == 0 {
		return "", nil
	}
	if h == nil || h.encryptor == nil {
		return "", fmt.Errorf("encrypt Dex static clients: encryptor is not configured")
	}
	raw, err := json.Marshal(clients)
	if err != nil {
		return "", fmt.Errorf("encode Dex static clients: %w", err)
	}
	value, err := h.encryptor.Encrypt(string(raw))
	if err != nil {
		return "", fmt.Errorf("encrypt Dex static clients: %w", err)
	}
	return value, nil
}

// loadPublicClients uses the compatibility copy until the durable cutover
// marker is set. Before cutover it opportunistically backfills (but never
// scrubs) the envelope. After cutover only the envelope is authoritative.
func (h *DexHandler) loadPublicClients(ctx context.Context, row sqlc.DexSetting) ([]map[string]any, sqlc.DexSetting, error) {
	if row.RuntimeSecretName == "" {
		row.RuntimeSecretName = "astronomer-dex-runtime"
	}
	decode := func(raw []byte) ([]map[string]any, error) {
		clients := make([]map[string]any, 0)
		if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return clients, nil
		}
		if err := json.Unmarshal(raw, &clients); err != nil {
			return nil, fmt.Errorf("decode Dex static clients: %w", err)
		}
		if err := validatePublicClients(clients); err != nil {
			return nil, fmt.Errorf("validate Dex static clients")
		}
		return clients, nil
	}
	// Before the explicit cutover, public_clients is the compatibility source of
	// truth because a previous binary may still update it. New binaries dual-write
	// the envelope but never scrub while old replicas can be serving.
	if !row.PublicClientsCutoverAt.Valid {
		clients, err := decode(row.PublicClients)
		if err != nil || len(clients) == 0 || row.PublicClientsEncrypted != "" {
			return clients, row, err
		}
		encrypted, err := h.encryptPublicClients(clients)
		if err != nil {
			return nil, row, err
		}
		backfilled, err := h.queries.BackfillDexPublicClientsEnvelope(ctx, sqlc.BackfillDexPublicClientsEnvelopeParams{
			ID: row.ID, PublicClientsEncrypted: encrypted, LegacyPublicClients: row.PublicClients,
		})
		if err == nil {
			if backfilled.RuntimeSecretName == "" {
				backfilled.RuntimeSecretName = row.RuntimeSecretName
			}
			return clients, backfilled, nil
		}
		latest, readErr := h.queries.GetDexSettings(ctx, row.ID)
		if readErr != nil {
			return nil, row, fmt.Errorf("backfill Dex static-client envelope")
		}
		latestClients, decodeErr := decode(latest.PublicClients)
		return latestClients, latest, decodeErr
	}
	if row.PublicClientsEncrypted != "" {
		if h == nil || h.encryptor == nil {
			return nil, row, fmt.Errorf("decrypt Dex static clients: encryptor is not configured")
		}
		plain, err := h.encryptor.Decrypt(row.PublicClientsEncrypted)
		if err != nil {
			return nil, row, fmt.Errorf("decrypt Dex static clients: %w", err)
		}
		clients, err := decode([]byte(plain))
		return clients, row, err
	}
	// Empty ciphertext is the canonical encoding for an empty client array.
	// The cutover constraint guarantees the compatibility copy is also empty.
	return []map[string]any{}, row, nil
}

func mustDexJSON(value any, fallback []byte) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(fallback)
	}
	return raw
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
