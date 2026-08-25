package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ResourceSettingsMutationTx is the transaction-scoped surface for the
// legacy general-settings and SSO-provider endpoints. Production supplies a
// sqlc query set bound to one pgx transaction.
type ResourceSettingsMutationTx interface {
	audit.OutboxQuerier
	GetPlatformConfigForUpdate(context.Context) (sqlc.PlatformConfiguration, error)
	UpsertPlatformConfig(context.Context, sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error)
	LockSSOProviderKey(context.Context, string) error
	GetSSOConfigurationByProvider(context.Context, string) (sqlc.SsoConfiguration, error)
	GetSSOConfigurationByIDForUpdate(context.Context, uuid.UUID) (sqlc.SsoConfiguration, error)
	CreateSSOConfiguration(context.Context, sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
	DeleteSSOConfiguration(context.Context, uuid.UUID) error
}

type resourceSettingsRunTxFunc func(context.Context, func(ResourceSettingsMutationTx) error) error

// SetRunTx enables atomic state + mandatory-audit commits for settings/SSO.
// The nil fallback exists only for the handler's narrow unit/dev fakes.
func (h *ResourceHandler) SetRunTx(runTx resourceSettingsRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ResourceHandler) TransactionalSettingsSSOAuditWired() bool {
	return h != nil && h.runTx != nil
}

var (
	errSSOProviderConflict = errors.New("sso provider already exists")
	errSSOProviderNotFound = errors.New("sso provider not found")
)

func (h *ResourceHandler) createSSOProviderTx(r *http.Request, params sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error) {
	var created sqlc.SsoConfiguration
	err := h.runTx(r.Context(), func(q ResourceSettingsMutationTx) error {
		// The advisory key lock covers the absent-row case that SELECT FOR
		// UPDATE cannot. Re-read after taking it before making the decision.
		if err := q.LockSSOProviderKey(r.Context(), params.Provider); err != nil {
			return err
		}
		if _, err := q.GetSSOConfigurationByProvider(r.Context(), params.Provider); err == nil {
			return errSSOProviderConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var err error
		created, err = q.CreateSSOConfiguration(r.Context(), params)
		if err != nil {
			if isUniqueViolation(err) {
				return errSSOProviderConflict
			}
			return err
		}
		return recordAuditOutbox(r, q, "sso.provider.create", "sso_provider", created.ID.String(), created.DisplayName, http.StatusCreated, map[string]any{
			"provider": created.Provider,
			"type":     ssoProviderType(created),
			"enabled":  created.IsEnabled,
		})
	})
	return created, err
}

func (h *ResourceHandler) deleteSSOProviderTx(r *http.Request, id uuid.UUID) (sqlc.SsoConfiguration, error) {
	var existing sqlc.SsoConfiguration
	err := h.runTx(r.Context(), func(q ResourceSettingsMutationTx) error {
		var err error
		existing, err = q.GetSSOConfigurationByIDForUpdate(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errSSOProviderNotFound
		}
		if err != nil {
			return err
		}
		if err := q.DeleteSSOConfiguration(r.Context(), id); err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "sso.provider.delete", "sso_provider", existing.ID.String(), existing.DisplayName, http.StatusNoContent, map[string]any{
			"provider": existing.Provider,
			"type":     ssoProviderType(existing),
		})
	})
	return existing, err
}

// compensateSSOCreate removes exactly the row created by this request when
// post-commit runtime registration fails. The provider-key advisory lock and
// ID comparison prevent deleting a replacement installed concurrently.
func (h *ResourceHandler) compensateSSOCreate(r *http.Request, created sqlc.SsoConfiguration) error {
	if h == nil || h.runTx == nil {
		return errors.New("transactional compensation is unavailable")
	}
	return h.runTx(r.Context(), func(q ResourceSettingsMutationTx) error {
		if err := q.LockSSOProviderKey(r.Context(), created.Provider); err != nil {
			return err
		}
		current, err := q.GetSSOConfigurationByIDForUpdate(r.Context(), created.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // already repaired by another actor
		}
		if err != nil {
			return err
		}
		if current.Provider != created.Provider {
			return fmt.Errorf("sso compensation identity changed")
		}
		if err := q.DeleteSSOConfiguration(r.Context(), created.ID); err != nil {
			return err
		}
		return recordAuditOutbox(r, q, "sso.provider.create_compensated", "sso_provider", created.ID.String(), created.DisplayName, http.StatusServiceUnavailable, map[string]any{
			"provider": created.Provider,
			"type":     ssoProviderType(created),
			"reason":   "runtime_registration_failed",
		})
	})
}
