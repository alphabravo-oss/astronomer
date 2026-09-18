package handler

import (
	"context"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// isSupportedChannelType is the create+update gate. Centralized here
// so the handler doesn't drift from the dispatcher's switch list.
func isSupportedChannelType(t string) bool {
	for _, s := range tasks.SupportedNotificationChannels {
		if t == s {
			return true
		}
	}
	return false
}

// AlertingQuerier abstracts the alerting-related database queries needed by AlertingHandler.
type AlertingQuerier interface {
	// Channels
	ListNotificationChannels(ctx context.Context, arg sqlc.ListNotificationChannelsParams) ([]sqlc.NotificationChannel, error)
	GetNotificationChannelByID(ctx context.Context, id uuid.UUID) (sqlc.NotificationChannel, error)
	CreateNotificationChannel(ctx context.Context, arg sqlc.CreateNotificationChannelParams) (sqlc.NotificationChannel, error)
	UpdateNotificationChannel(ctx context.Context, arg sqlc.UpdateNotificationChannelParams) (sqlc.NotificationChannel, error)
	DeleteNotificationChannel(ctx context.Context, id uuid.UUID) error
	CountNotificationChannels(ctx context.Context) (int64, error)
	// Rules
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	ListAlertRulesByCluster(ctx context.Context, arg sqlc.ListAlertRulesByClusterParams) ([]sqlc.AlertRule, error)
	GetAlertRuleByID(ctx context.Context, id uuid.UUID) (sqlc.AlertRule, error)
	ListAlertRulesByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.AlertRule, error)
	CreateAlertRule(ctx context.Context, arg sqlc.CreateAlertRuleParams) (sqlc.AlertRule, error)
	UpdateAlertRule(ctx context.Context, arg sqlc.UpdateAlertRuleParams) (sqlc.AlertRule, error)
	DeleteAlertRule(ctx context.Context, id uuid.UUID) error
	AddAlertRuleChannel(ctx context.Context, arg sqlc.AddAlertRuleChannelParams) error
	RemoveAlertRuleChannel(ctx context.Context, arg sqlc.RemoveAlertRuleChannelParams) error
	ListChannelsForAlertRule(ctx context.Context, alertRuleID uuid.UUID) ([]sqlc.NotificationChannel, error)
	ListAlertRuleChannelsByRules(ctx context.Context, ruleIds []uuid.UUID) ([]sqlc.AlertRuleChannel, error)
	CountAlertRules(ctx context.Context) (int64, error)
	CountAlertRulesByCluster(ctx context.Context, clusterID pgtype.UUID) (int64, error)
	// Events
	ListAlertEvents(ctx context.Context, arg sqlc.ListAlertEventsParams) ([]sqlc.AlertEvent, error)
	ListAlertEventsByRule(ctx context.Context, arg sqlc.ListAlertEventsByRuleParams) ([]sqlc.AlertEvent, error)
	ListAlertEventsFiltered(ctx context.Context, arg sqlc.ListAlertEventsFilteredParams) ([]sqlc.AlertEvent, error)
	CountAlertEventsFiltered(ctx context.Context, arg sqlc.CountAlertEventsFilteredParams) (int64, error)
	CountActiveAlertsByRules(ctx context.Context, ruleIds []uuid.UUID) ([]sqlc.CountActiveAlertsByRulesRow, error)
	GetAlertEventByID(ctx context.Context, id uuid.UUID) (sqlc.AlertEvent, error)
	AcknowledgeAlertEvent(ctx context.Context, arg sqlc.AcknowledgeAlertEventParams) error
	UpdateAlertEventStatus(ctx context.Context, arg sqlc.UpdateAlertEventStatusParams) error
	CountAlertEvents(ctx context.Context) (int64, error)
	// Silences
	ListAlertSilences(ctx context.Context, arg sqlc.ListAlertSilencesParams) ([]sqlc.AlertSilence, error)
	GetAlertSilenceByID(ctx context.Context, id uuid.UUID) (sqlc.AlertSilence, error)
	CreateAlertSilence(ctx context.Context, arg sqlc.CreateAlertSilenceParams) (sqlc.AlertSilence, error)
	DeleteAlertSilence(ctx context.Context, id uuid.UUID) error
	CountAlertSilences(ctx context.Context) (int64, error)
	// Inhibitions (P-03) — Alertmanager-style inhibition rules.
	ListAlertInhibitions(ctx context.Context, arg sqlc.ListAlertInhibitionsParams) ([]sqlc.AlertInhibition, error)
	GetAlertInhibitionByID(ctx context.Context, id uuid.UUID) (sqlc.AlertInhibition, error)
	CreateAlertInhibition(ctx context.Context, arg sqlc.CreateAlertInhibitionParams) (sqlc.AlertInhibition, error)
	UpdateAlertInhibition(ctx context.Context, arg sqlc.UpdateAlertInhibitionParams) (sqlc.AlertInhibition, error)
	DeleteAlertInhibition(ctx context.Context, id uuid.UUID) error
	CountAlertInhibitions(ctx context.Context) (int64, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClustersByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.Cluster, error)
	GetDefaultMonitoringBackend(ctx context.Context) (sqlc.MonitoringBackend, error)
	UpsertDefaultMonitoringBackend(ctx context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
}

// AlertingMutationTx is the complete transaction-bound surface used by
// alerting writes. Embedding the read interface is intentional: several
// mutations perform read/modify/write work (rule-channel associations,
// enable/disable, expire) that must remain inside the same transaction as the
// audit intent. The task outbox makes TestChannel durable without a DB/Redis
// dual-write window.
type AlertingMutationTx interface {
	AlertingQuerier
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type alertingRunTxFunc func(context.Context, func(AlertingMutationTx) error) error

// AlertingHandler handles alerting endpoints.
type AlertingHandler struct {
	queries   AlertingQuerier
	runTx     alertingRunTxFunc
	requester K8sRequester
	// settingsCache resolves DIR-08 Alertmanager timing knobs at render time.
	settingsCache *SettingsCache
	bus           *events.Bus
	// encryptor seals/unseals monitoring_backends.auth_config (migration 146).
	// This handler is a WRITER of that column — persistSharedAlertingAssetHashes
	// stamps the rendered-asset hashes into it — so it needs the key for the
	// same reason MonitoringHandler does. Optional in development only.
	encryptor *auth.Encryptor
}

// SetEncryptor wires the Fernet encryptor for the monitoring-backend
// credential (migration 146).
func (h *AlertingHandler) SetEncryptor(encryptor *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = encryptor
}

func (h *AlertingHandler) monitoringDecryptor() imonitoring.Decryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

func (h *AlertingHandler) monitoringSealer() imonitoring.Encryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

// NewAlertingHandler creates a new alerting handler.
func NewAlertingHandler(queries AlertingQuerier) *AlertingHandler {
	return &AlertingHandler{queries: queries}
}

func NewAlertingHandlerWithDeps(queries AlertingQuerier, requester K8sRequester) *AlertingHandler {
	return &AlertingHandler{queries: queries, requester: requester}
}

// SetRunTx wires the production database transaction used to commit an
// alerting mutation, its durable side-effect intent, and its audit intent as
// one unit.
func (h *AlertingHandler) SetRunTx(runTx alertingRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *AlertingHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetSettingsCache wires platform settings for Alertmanager timing (DIR-08).
func (h *AlertingHandler) SetSettingsCache(c *SettingsCache) {
	if h == nil {
		return
	}
	h.settingsCache = c
}

// SetEventBus wires the SSE bus for alerting.changed liveness events (P4.9).
// Optional: publishers are fire-and-forget and nil-safe.
func (h *AlertingHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishAlertingChanged emits the metadata-only alerting.changed event after
// a successful alerting-row write. kind is rule|event|silence; clusterID is
// the entity's cluster when it has one ("" for global entities — those
// publish unscoped and reach superusers only, per the SEC-R07 fail-closed
// drop). The worker-side halves of this domain (alert-event ingestion/
// resolution, anomaly-baseline recompute) publish through the worker
// runtime's Redis-attached bus instead — see internal/worker/tasks.
func (h *AlertingHandler) publishAlertingChanged(kind, clusterID string, entityID uuid.UUID) {
	if h == nil {
		return
	}
	events.PublishChanged(h.bus, "alerting", clusterID, entityID.String(), map[string]any{"kind": kind})
}

func (h *AlertingHandler) alertmanagerTiming(ctx context.Context) (groupWait, groupInterval, repeatInterval string) {
	groupWait, groupInterval, repeatInterval = "30s", "5m", "3h"
	if h == nil || h.settingsCache == nil {
		return
	}
	if v := strings.TrimSpace(h.settingsCache.StringValue(ctx, "alertmanager.group_wait", groupWait)); v != "" {
		groupWait = v
	}
	if v := strings.TrimSpace(h.settingsCache.StringValue(ctx, "alertmanager.group_interval", groupInterval)); v != "" {
		groupInterval = v
	}
	if v := strings.TrimSpace(h.settingsCache.StringValue(ctx, "alertmanager.repeat_interval", repeatInterval)); v != "" {
		repeatInterval = v
	}
	return
}
