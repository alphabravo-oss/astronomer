package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	astronomerDefaultRelease    = "astronomer"
	astronomerDefaultNamespace  = "astronomer"
	managementLoggingSecretName = "astronomer-mgmt-loki-ingest"
	managementLoggingSecretKey  = "token"
	managementLoggingHelmWait   = 90

	managementLoggingEnabledMetaKey     = "managementLoggingEnabled"
	managementLoggingOverlayHashMetaKey = "managementLoggingOverlayHash"
)

type lokiIngestTokenStore interface {
	GetLokiIngestTokenByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.LokiIngestToken, error)
	UpsertLokiIngestToken(ctx context.Context, arg sqlc.UpsertLokiIngestTokenParams) (sqlc.LokiIngestToken, error)
}

var _ lokiIngestTokenStore = (*sqlc.Queries)(nil)

// managementLoggingOverlayInput is the non-secret view used to decide whether
// the Astronomer chart should ship server/worker logs to hosted Loki.
type managementLoggingOverlayInput struct {
	Status       string
	IngestPublic bool
	Host         string
	Port         string
	TenantID     string
}

func (in managementLoggingOverlayInput) ready() bool {
	if !in.IngestPublic || strings.TrimSpace(in.Host) == "" || strings.TrimSpace(in.TenantID) == "" {
		return false
	}
	return lokiRunning(map[string]any{"status": in.Status})
}

// managementLoggingValuesOverlay returns a helm --reuse-values overlay for the
// Astronomer chart. Nil means leave chart defaults (enabled: false). The
// overlay never contains a bearer token; Fluent Bit reads it from a Secret.
func managementLoggingValuesOverlay(in managementLoggingOverlayInput) map[string]any {
	if !in.ready() {
		return nil
	}
	endpoint := managementLoggingEndpoint(in.Host, in.Port)
	if endpoint == "" {
		return nil
	}
	return map[string]any{
		"managementLogging": map[string]any{
			"enabled":  true,
			"backend":  "loki",
			"endpoint": endpoint,
			"loki": map[string]any{
				"tenantID": in.TenantID,
			},
			"auth": map[string]any{
				"bearerSecretRef": map[string]any{
					"name": managementLoggingSecretName,
					"key":  managementLoggingSecretKey,
				},
			},
		},
	}
}

func managementLoggingDisableOverlay() map[string]any {
	return map[string]any{
		"managementLogging": map[string]any{"enabled": false},
	}
}

func managementLoggingEndpoint(host, port string) string {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		return host
	}
	switch port {
	case "", "443":
		return "https://" + host
	case "80":
		return "http://" + host
	default:
		return "https://" + host + ":" + port
	}
}

func managementLoggingOverlayHash(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ReconcileManagementLogging overlays Astronomer chart managementLogging onto
// hosted Loki when the warehouse is healthy and ingest is public. Distinct
// from member-cluster Fluent Bit: this ships astronomer-server/worker logs
// from the management cluster (is_local). Chart default stays off when Loki
// is not installed.
func (h *MonitoringHandler) ReconcileManagementLogging(ctx context.Context) error {
	if h == nil {
		return nil
	}
	local, ok := h.localManagementCluster(ctx)
	if !ok {
		return nil
	}
	state := h.LokiAttachState(ctx)
	in := managementLoggingOverlayInput{
		Status:       state.Status,
		IngestPublic: state.IngestPublic,
		Host:         state.Host,
		Port:         state.Port,
		TenantID:     local.ID.String(),
	}
	overlay := managementLoggingValuesOverlay(in)
	if overlay == nil {
		return h.disableManagementLoggingIfApplied(ctx, local.ID.String())
	}
	if h.encryptor == nil || h.requester == nil || h.helm == nil {
		return nil
	}
	if _, ok := h.queries.(lokiIngestTokenStore); !ok {
		return nil
	}
	plain, err := h.ensureManagementLokiToken(ctx, local.ID)
	if err != nil {
		return err
	}
	ns := h.astronomerReleaseNamespace()
	if err := applyAlertSecret(ctx, h.requester, local.ID.String(), ns, managementLoggingSecretName, map[string]string{
		managementLoggingSecretKey: plain,
	}); err != nil {
		return fmt.Errorf("apply management logging ingest secret: %w", err)
	}
	return h.applyManagementLoggingOverlay(ctx, local.ID.String(), overlay)
}

func (h *MonitoringHandler) localManagementCluster(ctx context.Context) (sqlc.Cluster, bool) {
	if h == nil || h.queries == nil {
		return sqlc.Cluster{}, false
	}
	clusters, err := h.sizerListAllClusters(ctx)
	if err != nil {
		return sqlc.Cluster{}, false
	}
	backend, berr := h.queries.GetDefaultMonitoringBackend(ctx)
	var lokiMeta map[string]any
	if berr == nil {
		lokiMeta = sharedStackMetadata(backend, "sharedLoki")
	}
	picked, ok := sizerPickManagementCluster(clusters, nil, lokiMeta)
	if !ok || !picked.IsLocal {
		for _, c := range clusters {
			if c.IsLocal {
				return c, true
			}
		}
		return sqlc.Cluster{}, false
	}
	return picked, true
}

func (h *MonitoringHandler) astronomerReleaseNamespace() string {
	if h != nil {
		if ns := strings.TrimSpace(h.grafanaExpose.PlatformNamespace); ns != "" {
			return ns
		}
	}
	return astronomerDefaultNamespace
}

func (h *MonitoringHandler) ensureManagementLokiToken(ctx context.Context, clusterID uuid.UUID) (string, error) {
	if h.encryptor == nil {
		return "", errors.New("encryption is not configured")
	}
	store, ok := h.queries.(lokiIngestTokenStore)
	if !ok {
		return "", errors.New("loki ingest token store not configured")
	}
	tok, err := store.GetLokiIngestTokenByCluster(ctx, clusterID)
	if err == nil && strings.TrimSpace(tok.TokenEncrypted) != "" {
		plain, decErr := h.encryptor.Decrypt(tok.TokenEncrypted)
		if decErr != nil {
			return "", fmt.Errorf("decrypt management ingest token: %w", decErr)
		}
		return plain, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	plain, err := mintLokiIngestToken()
	if err != nil {
		return "", err
	}
	sealed, err := h.encryptor.Encrypt(plain)
	if err != nil {
		return "", fmt.Errorf("encrypt management ingest token: %w", err)
	}
	if _, err := store.UpsertLokiIngestToken(ctx, sqlc.UpsertLokiIngestTokenParams{
		ClusterID:      clusterID,
		TokenHash:      lokiauth.HashBearer(plain),
		TokenEncrypted: sealed,
		CreatedByID:    pgtype.UUID{},
	}); err != nil {
		return "", fmt.Errorf("store management ingest token: %w", err)
	}
	return plain, nil
}

func (h *MonitoringHandler) applyManagementLoggingOverlay(ctx context.Context, clusterID string, overlay map[string]any) error {
	if h.helm == nil {
		return nil
	}
	hash := managementLoggingOverlayHash(overlay)
	if hash != "" && hash == h.managementLoggingAppliedHash(ctx) {
		return nil
	}
	ns := h.astronomerReleaseNamespace()
	result, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmUpgrade, protocol.HelmRequestPayload{
		ReleaseName: astronomerDefaultRelease,
		Namespace:   ns,
		Values:      overlay,
		Timeout:     managementLoggingHelmWait,
		ReuseValues: true,
	})
	if err != nil {
		return fmt.Errorf("overlay astronomer managementLogging: %w", err)
	}
	if result != nil && !result.Success && strings.TrimSpace(result.Error) != "" {
		return fmt.Errorf("overlay astronomer managementLogging: %s", result.Error)
	}
	return h.stampManagementLoggingOverlay(ctx, true, hash)
}

func (h *MonitoringHandler) disableManagementLoggingIfApplied(ctx context.Context, clusterID string) error {
	if !h.managementLoggingWasEnabled(ctx) {
		return nil
	}
	if h.helm == nil {
		return nil
	}
	overlay := managementLoggingDisableOverlay()
	hash := managementLoggingOverlayHash(overlay)
	if hash != "" && hash == h.managementLoggingAppliedHash(ctx) {
		return nil
	}
	ns := h.astronomerReleaseNamespace()
	if _, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmUpgrade, protocol.HelmRequestPayload{
		ReleaseName: astronomerDefaultRelease,
		Namespace:   ns,
		Values:      overlay,
		Timeout:     managementLoggingHelmWait,
		ReuseValues: true,
	}); err != nil {
		return fmt.Errorf("disable astronomer managementLogging: %w", err)
	}
	return h.stampManagementLoggingOverlay(ctx, false, hash)
}

func (h *MonitoringHandler) managementLoggingWasEnabled(ctx context.Context) bool {
	meta := h.sharedLokiMeta(ctx)
	return boolFromAny(meta[managementLoggingEnabledMetaKey])
}

func (h *MonitoringHandler) managementLoggingAppliedHash(ctx context.Context) string {
	return stringFromMap(h.sharedLokiMeta(ctx), managementLoggingOverlayHashMetaKey)
}

func (h *MonitoringHandler) sharedLokiMeta(ctx context.Context) map[string]any {
	if h == nil || h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return nil
	}
	return sharedStackMetadata(backend, "sharedLoki")
}

func (h *MonitoringHandler) stampManagementLoggingOverlay(ctx context.Context, enabled bool, overlayHash string) error {
	if h == nil || h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return err
	}
	authCfg, err := resolveMonitoringBackendAuthConfig(backend, h.monitoringDecryptor())
	if err != nil {
		return err
	}
	meta := mapFromMapValue(authCfg["sharedLoki"])
	if meta == nil {
		meta = map[string]any{}
	}
	meta[managementLoggingEnabledMetaKey] = enabled
	meta[managementLoggingOverlayHashMetaKey] = overlayHash
	authCfg["sharedLoki"] = meta
	params := sqlc.UpsertDefaultMonitoringBackendParams{
		BackendType:        backend.BackendType,
		QueryUrl:           backend.QueryUrl,
		AlertmanagerUrl:    backend.AlertmanagerUrl,
		TenantID:           backend.TenantID,
		AuthType:           backend.AuthType,
		DefaultStepSeconds: backend.DefaultStepSeconds,
		TimeoutSeconds:     backend.TimeoutSeconds,
		CreatedByID:        backend.CreatedByID,
	}
	if err := imonitoring.SealInto(&params, authCfg, h.monitoringSealer()); err != nil {
		return err
	}
	_, err = h.queries.UpsertDefaultMonitoringBackend(ctx, params)
	return err
}
