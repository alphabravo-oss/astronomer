package handler

import (
	"context"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type forwardedAuditOutbox struct{ target any }

func (w forwardedAuditOutbox) UpsertAuditOutbox(ctx context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if outbox, ok := w.target.(interface {
		UpsertAuditOutbox(context.Context, sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error)
	}); ok {
		return outbox.UpsertAuditOutbox(ctx, arg)
	}
	if legacy, ok := w.target.(interface {
		CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error
	}); ok {
		if err := legacy.CreateAuditLogV1(ctx, auditLogParamsFromOutbox(arg)); err != nil {
			return sqlc.AuditOutbox{}, err
		}
	}
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

type forwardedTaskOutbox struct{ target any }

func (w forwardedTaskOutbox) UpsertTaskOutbox(ctx context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if outbox, ok := w.target.(interface {
		UpsertTaskOutbox(context.Context, sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error)
	}); ok {
		return outbox.UpsertTaskOutbox(ctx, arg)
	}
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

type dashboardMutationFixture struct {
	DashboardQuerier
	forwardedAuditOutbox
}

func wireDashboardMutationFixture(h *DashboardHandler, q DashboardQuerier) *DashboardHandler {
	tx := dashboardMutationFixture{DashboardQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(DashboardMutationTx) error) error { return fn(tx) })
	return h
}

type dexMutationFixture struct {
	DexQuerier
	forwardedAuditOutbox
}

func wireDexMutationFixture(h *DexHandler, q DexQuerier) *DexHandler {
	tx := dexMutationFixture{DexQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(DexMutationTx) error) error { return fn(tx) })
	return h
}

type gitOpsMutationFixture struct {
	GitOpsQuerier
	forwardedAuditOutbox
	forwardedTaskOutbox
}

func wireGitOpsMutationFixture(h *GitOpsHandler, q GitOpsQuerier) *GitOpsHandler {
	tx := gitOpsMutationFixture{GitOpsQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}, forwardedTaskOutbox: forwardedTaskOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(GitOpsMutationTx) error) error { return fn(tx) })
	return h
}

type groupMappingsMutationFixture struct {
	GroupMappingsQuerier
	forwardedAuditOutbox
}

func wireGroupMappingsMutationFixture(h *GroupMappingsHandler, q GroupMappingsQuerier) *GroupMappingsHandler {
	tx := groupMappingsMutationFixture{GroupMappingsQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(GroupMappingsMutationTx) error) error { return fn(tx) })
	return h
}

type maintenanceMutationFixture struct {
	MaintenanceQuerier
	forwardedAuditOutbox
}

func wireMaintenanceMutationFixture(h *MaintenanceHandler, q MaintenanceQuerier) *MaintenanceHandler {
	tx := maintenanceMutationFixture{MaintenanceQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(MaintenanceMutationTx) error) error { return fn(tx) })
	return h
}

type nativeRBACMutationFixture struct {
	NativeRBACQuerier
	forwardedAuditOutbox
}

func wireNativeRBACMutationFixture(h *NativeRBACHandler, q NativeRBACQuerier) *NativeRBACHandler {
	tx := nativeRBACMutationFixture{NativeRBACQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(NativeRBACMutationTx) error) error { return fn(tx) })
	return h
}

type notificationTemplateMutationFixture struct {
	NotificationTemplateQuerier
	forwardedAuditOutbox
}

func wireNotificationTemplateMutationFixture(h *NotificationTemplateHandler, q NotificationTemplateQuerier) *NotificationTemplateHandler {
	tx := notificationTemplateMutationFixture{NotificationTemplateQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(NotificationTemplateMutationTx) error) error { return fn(tx) })
	return h
}

type platformSettingsMutationFixture struct {
	PlatformSettingsQuerier
	forwardedAuditOutbox
}

func wirePlatformSettingsMutationFixture(h *PlatformSettingsHandler, q PlatformSettingsQuerier) *PlatformSettingsHandler {
	tx := platformSettingsMutationFixture{PlatformSettingsQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(PlatformSettingsMutationTx) error) error { return fn(tx) })
	return h
}

type quotaMutationFixture struct {
	QuotaQuerier
	forwardedAuditOutbox
}

func wireQuotaMutationFixture(h *QuotaHandler, q QuotaQuerier) *QuotaHandler {
	tx := quotaMutationFixture{QuotaQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(QuotaMutationTx) error) error { return fn(tx) })
	return h
}

type readAuditPolicyMutationFixture struct {
	ReadAuditPolicyQuerier
	forwardedAuditOutbox
}

func wireReadAuditPolicyMutationFixture(h *ReadAuditPolicyHandler, q ReadAuditPolicyQuerier) *ReadAuditPolicyHandler {
	tx := readAuditPolicyMutationFixture{ReadAuditPolicyQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(ReadAuditPolicyMutationTx) error) error { return fn(tx) })
	return h
}

type vaultMutationFixture struct {
	VaultConnectionQuerier
	forwardedAuditOutbox
}

func wireVaultMutationFixture(h *VaultHandler, q VaultConnectionQuerier) *VaultHandler {
	tx := vaultMutationFixture{VaultConnectionQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(VaultMutationTx) error) error { return fn(tx) })
	return h
}

type webhookMutationFixture struct {
	WebhookQuerier
	forwardedAuditOutbox
}

func wireWebhookMutationFixture(h *WebhookHandler, q WebhookQuerier) *WebhookHandler {
	tx := webhookMutationFixture{WebhookQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(WebhookMutationTx) error) error { return fn(tx) })
	return h
}

type rbacMutationFixture struct {
	RBACQuerier
	forwardedAuditOutbox
}

func (tx rbacMutationFixture) ApplyProjectRoleTemplate(ctx context.Context, arg sqlc.ApplyProjectRoleTemplateParams) (sqlc.ApplyProjectRoleTemplateRow, error) {
	if applier, ok := tx.RBACQuerier.(interface {
		ApplyProjectRoleTemplate(context.Context, sqlc.ApplyProjectRoleTemplateParams) (sqlc.ApplyProjectRoleTemplateRow, error)
	}); ok {
		return applier.ApplyProjectRoleTemplate(ctx, arg)
	}
	return sqlc.ApplyProjectRoleTemplateRow{}, nil
}

func wireRBACMutationFixture(h *RBACHandler, q RBACQuerier) *RBACHandler {
	tx := rbacMutationFixture{RBACQuerier: q, forwardedAuditOutbox: forwardedAuditOutbox{q}}
	h.SetRunTx(func(_ context.Context, fn func(RBACMutationTx) error) error { return fn(tx) })
	return h
}
