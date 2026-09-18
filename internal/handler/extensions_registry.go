package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// openapi:request-operation postExtensions
type InstallExtensionRequest struct {
	Manifest ExtensionManifest `json:"manifest"`
	Source   string            `json:"source,omitempty"`
	Enable   bool              `json:"enable,omitempty"`
}

func (h *ExtensionHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension registry is not configured")
		return
	}
	rows, err := h.queries.ListUIExtensions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list extensions")
		return
	}
	items := make([]ExtensionRecordResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, extensionRecordResponse(row))
	}
	RespondJSON(w, http.StatusOK, ExtensionListResponse{
		Items:          items,
		SampleManifest: sampleExtensionManifest(),
	})
}

func (h *ExtensionHandler) Install(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension registry is not configured")
		return
	}
	var req InstallExtensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	validation := validateExtensionManifest(req.Manifest, h.current)
	h.warnBundleGated(&validation)
	if !validation.Valid {
		RespondJSON(w, http.StatusBadRequest, validation)
		return
	}
	enabled := req.Enable && validation.CompatibilityStatus == "compatible"
	// Fail-closed: an executable bundle cannot reach enabled=true until a
	// trusted key is configured (and, in a later phase, bundle_verified).
	if enabled && h.trustedKey == nil && manifestHasBundle(validation.Manifest) {
		enabled = false
	}
	manifestBytes, _ := json.Marshal(validation.Manifest)
	// Signed-bundle gate must be re-armed on re-install. The upsert preserves
	// the prior bundle_verified on ON CONFLICT, so a re-install that swaps in a
	// new/unsigned bundle (changed checksum or manifest) would otherwise inherit
	// the old verified flag and mount as Tier-2 without ever being re-verified.
	// Treat the gate as still valid only when the stored bundle descriptor is
	// byte-identical to what we're upserting.
	params := sqlc.UpsertUIExtensionParams{
		Name:                validation.Manifest.Name,
		DisplayName:         extensionDisplayName(validation.Manifest),
		Version:             validation.Manifest.Version,
		Source:              sanitizeExtensionSource(req.Source),
		Checksum:            validation.Checksum,
		Enabled:             enabled,
		CompatibilityStatus: validation.CompatibilityStatus,
		Manifest:            manifestBytes,
		InstalledBy:         currentUserUUID(r),
	}
	persist := func(q ExtensionQuerier, prior sqlc.UIExtension, priorFound bool) (sqlc.UIExtension, error) {
		bundleUnchanged := priorFound && prior.Checksum == validation.Checksum && string(prior.Manifest) == string(manifestBytes)
		row, err := q.UpsertUIExtension(r.Context(), params)
		if err != nil {
			return sqlc.UIExtension{}, err
		}
		if row.BundleVerified && !bundleUnchanged {
			return q.SetUIExtensionBundleVerified(r.Context(), sqlc.SetUIExtensionBundleVerifiedParams{Name: row.Name, BundleVerified: false})
		}
		return row, nil
	}

	var row sqlc.UIExtension
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "extension transaction runner is not configured")
		return
	}
	err := h.runTx(r.Context(), func(q ExtensionMutationTx) error {
		prior, lockErr := q.GetUIExtensionByNameForUpdate(r.Context(), validation.Manifest.Name)
		priorFound := lockErr == nil
		if lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
			return lockErr
		}
		row, lockErr = persist(q, prior, priorFound)
		if lockErr != nil {
			return lockErr
		}
		return recordAuditOutbox(r, q, "admin.extension.installed", "ui_extension", row.ID.String(), row.Name, http.StatusOK, map[string]any{
			"name": row.Name, "version": row.Version, "enabled": row.Enabled, "compatibility_status": row.CompatibilityStatus,
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.InstallFailed, "Failed to install extension")
		return
	}
	RespondJSON(w, http.StatusOK, extensionRecordResponse(row))
}

func (h *ExtensionHandler) Enable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, true)
}

func (h *ExtensionHandler) Disable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false)
}

func (h *ExtensionHandler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension registry is not configured")
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if !extensionNameRE.MatchString(name) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Invalid extension name")
		return
	}
	var errExtensionIncompatible = errors.New("extension is incompatible")
	mutate := func(q ExtensionQuerier, existing sqlc.UIExtension) (sqlc.UIExtension, error) {
		if enabled && existing.CompatibilityStatus != "compatible" {
			return sqlc.UIExtension{}, errExtensionIncompatible
		}
		return q.SetUIExtensionEnabled(r.Context(), sqlc.SetUIExtensionEnabledParams{Name: name, Enabled: enabled})
	}
	action := "admin.extension.disabled"
	if enabled {
		action = "admin.extension.enabled"
	}
	var row sqlc.UIExtension
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "extension transaction runner is not configured")
		return
	}
	err := h.runTx(r.Context(), func(q ExtensionMutationTx) error {
		existing, lockErr := q.GetUIExtensionByNameForUpdate(r.Context(), name)
		if lockErr != nil {
			return lockErr
		}
		row, lockErr = mutate(q, existing)
		if lockErr != nil {
			return lockErr
		}
		return recordAuditOutbox(r, q, action, "ui_extension", row.ID.String(), row.Name, http.StatusOK, map[string]any{
			"name": row.Name, "version": row.Version,
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Extension not found")
		return
	}
	if errors.Is(err, errExtensionIncompatible) {
		RespondRequestError(w, r, http.StatusConflict, apierror.IncompatibleExtension, "Incompatible extensions cannot be enabled")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update extension")
		return
	}
	RespondJSON(w, http.StatusOK, extensionRecordResponse(row))
}

func (h *ExtensionHandler) findExtension(ctx context.Context, name string) (sqlc.UIExtension, error) {
	rows, err := h.queries.ListUIExtensions(ctx)
	if err != nil {
		return sqlc.UIExtension{}, err
	}
	for _, row := range rows {
		if row.Name == name {
			return row, nil
		}
	}
	return sqlc.UIExtension{}, pgx.ErrNoRows
}
