package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SSOCallbackTx is supplied exclusively by a transaction-bound sqlc query set.
type SSOCallbackTx interface {
	SSOQuerier
	audit.OutboxQuerier
	GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error)
	InsertSSOSession(context.Context, sqlc.InsertSSOSessionParams) error
	CreateRefreshSession(context.Context, sqlc.CreateRefreshSessionParams) error
}

type ssoRunTxFunc func(context.Context, func(SSOCallbackTx) error) error

func (h *SSOHandler) SetRunTx(runTx ssoRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *SSOHandler) TransactionalAuditWired() bool {
	return h != nil && h.manager != nil && h.runTx != nil && h.encryptor != nil && h.jwt != nil && h.jwt.KeyCount() > 0 && h.rbacCache != nil
}

var errSSOAccountDisabled = errors.New("SSO account is disabled")

func (h *SSOHandler) commitSSOCallback(r *http.Request, provider string, info *auth.SSOUserInfo) (sqlc.User, auth.PreparedTokenPair, error) {
	var user sqlc.User
	if !h.TransactionalAuditWired() {
		return user, auth.PreparedTokenPair{}, audit.ErrOutboxUnavailable
	}
	pair, err := h.jwt.PrepareTokenPairContext(r.Context())
	if err != nil {
		return user, pair, err
	}
	var result auth.SyncResult
	err = h.runTx(r.Context(), func(q SSOCallbackTx) error {
		if q == nil {
			return audit.ErrOutboxUnavailable
		}
		connector, err := resolveSSOConnector(r.Context(), q, info)
		if err != nil {
			return err
		}
		var provisioned, linked bool
		user, provisioned, linked, err = findOrCreateSSOUser(r.Context(), q, info)
		if err != nil {
			return err
		}
		// Serialize reconciliation and re-check activation after acquiring the
		// user lock, including concurrent deactivation or another SSO login.
		user, err = q.GetUserByIDForUpdate(r.Context(), user.ID)
		if err != nil {
			return err
		}
		if !user.IsActive {
			return errSSOAccountDisabled
		}
		result, err = auth.SyncUserGroups(r.Context(), q, user.ID, connector, info.Groups, true)
		if err != nil {
			return err
		}
		if err := q.UpdateUserLastLogin(r.Context(), user.ID); err != nil {
			return err
		}
		if err := h.persistSSOSession(r.Context(), q, user.ID, provider, pair, info); err != nil {
			return err
		}
		if err := createRefreshSession(r.Context(), q, user.ID, pair); err != nil {
			return err
		}
		return auditSSOCallback(r, q, user, provider, info, provisioned, linked, result)
	})
	if err != nil {
		return sqlc.User{}, auth.PreparedTokenPair{}, err
	}
	if len(result.Added)+len(result.Removed) > 0 {
		h.rbacCache.Invalidate(user.ID.String())
	}
	return user, pair, nil
}

func resolveSSOConnector(ctx context.Context, q SSOQuerier, info *auth.SSOUserInfo) (pgtype.UUID, error) {
	if info == nil || strings.TrimSpace(info.ConnectorID) == "" {
		return pgtype.UUID{}, errors.New("SSO connector identity is missing")
	}
	connector, err := q.GetDexConnectorByName(ctx, info.ConnectorID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("resolve SSO connector: %w", err)
	}
	if !connector.Enabled || connector.ID == uuid.Nil {
		return pgtype.UUID{}, errors.New("SSO connector is unavailable")
	}
	return pgtype.UUID{Bytes: connector.ID, Valid: true}, nil
}

func (h *SSOHandler) persistSSOSession(ctx context.Context, q interface {
	InsertSSOSession(context.Context, sqlc.InsertSSOSessionParams) error
}, userID uuid.UUID, provider string, pair auth.PreparedTokenPair, info *auth.SSOUserInfo) error {
	if q == nil || h.encryptor == nil || info == nil {
		return audit.ErrOutboxUnavailable
	}
	if info.UpstreamIDToken == "" {
		return nil // This provider has no upstream logout protocol.
	}
	cipher, err := h.encryptor.Encrypt(info.UpstreamIDToken)
	if err != nil {
		return err
	}
	return q.InsertSSOSession(ctx, sqlc.InsertSSOSessionParams{
		Jti: pair.AccessID(), UserID: userID, ProviderName: provider,
		UpstreamIDTokenEncrypted: cipher, EndSessionEndpoint: info.EndSessionEndpoint,
		ExpiresAt: pair.RefreshExpiry(),
	})
}

func auditSSOCallback(r *http.Request, q audit.OutboxQuerier, user sqlc.User, provider string, info *auth.SSOUserInfo, provisioned, linked bool, result auth.SyncResult) error {
	actor := pgtype.UUID{Bytes: user.ID, Valid: true}
	for _, event := range []struct {
		action  string
		enabled bool
	}{
		{"sso.user_provisioned", provisioned}, {"principal.linked", linked}, {"sso.callback", true},
	} {
		if !event.enabled {
			continue
		}
		if err := recordAuditOutboxAs(r, q, actor, event.action, "user", user.ID.String(), user.Username, http.StatusFound,
			map[string]any{"provider": provider, "connector_id": info.ConnectorID}); err != nil {
			return err
		}
	}
	for _, change := range []struct {
		action   string
		bindings []auth.SyncedBinding
	}{
		{"auth.group_sync.binding_added", result.Added}, {"auth.group_sync.binding_removed", result.Removed},
	} {
		for _, binding := range change.bindings {
			if err := recordAuditOutboxAs(r, q, pgtype.UUID{}, change.action, "role_binding", binding.BindingID.String(), "", http.StatusFound, map[string]any{
				"user_id": user.ID.String(), "role_id": binding.RoleID.String(), "scope": binding.Scope,
				"group_name": binding.GroupName, "cluster_id": uuidOrEmpty(binding.ClusterID), "project_id": uuidOrEmpty(binding.ProjectID),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
