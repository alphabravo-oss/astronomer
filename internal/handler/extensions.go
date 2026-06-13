package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

type ExtensionQuerier interface {
	ListUIExtensions(ctx context.Context) ([]sqlc.UIExtension, error)
	UpsertUIExtension(ctx context.Context, arg sqlc.UpsertUIExtensionParams) (sqlc.UIExtension, error)
	SetUIExtensionEnabled(ctx context.Context, arg sqlc.SetUIExtensionEnabledParams) (sqlc.UIExtension, error)
}

type ExtensionHandler struct {
	queries ExtensionQuerier
	auditor any
	current string
}

func NewExtensionHandler(queries ExtensionQuerier) *ExtensionHandler {
	return &ExtensionHandler{queries: queries, auditor: queries, current: version.Version}
}

func (h *ExtensionHandler) SetAuditWriter(a any) {
	if h == nil {
		return
	}
	h.auditor = a
}

func (h *ExtensionHandler) SetCurrentVersion(v string) {
	if h == nil {
		return
	}
	h.current = strings.TrimSpace(v)
}

type ExtensionManifest struct {
	APIVersion           string                  `json:"apiVersion"`
	Name                 string                  `json:"name"`
	DisplayName          string                  `json:"displayName"`
	Version              string                  `json:"version"`
	CompatibleAstronomer string                  `json:"compatibleAstronomer"`
	Entry                string                  `json:"entry"`
	Permissions          []string                `json:"permissions"`
	BackendAPIScopes     []string                `json:"backendApiScopes,omitempty"`
	CSP                  ExtensionCSP            `json:"csp,omitempty"`
	ExtensionPoints      ExtensionManifestPoints `json:"extensionPoints"`
}

type ExtensionCSP struct {
	ScriptSrc  []string `json:"scriptSrc,omitempty"`
	ConnectSrc []string `json:"connectSrc,omitempty"`
	FrameSrc   []string `json:"frameSrc,omitempty"`
	ImageSrc   []string `json:"imageSrc,omitempty"`
}

type ExtensionManifestPoints struct {
	Sidebar     []ExtensionSidebarPoint `json:"sidebar,omitempty"`
	Widgets     []ExtensionWidgetPoint  `json:"widgets,omitempty"`
	ClusterTabs []ExtensionClusterTab   `json:"clusterTabs,omitempty"`
	Settings    []ExtensionSettingsPage `json:"settings,omitempty"`
}

type ExtensionSidebarPoint struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type ExtensionWidgetPoint struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ExtensionClusterTab struct {
	Label     string `json:"label"`
	Component string `json:"component"`
}

type ExtensionSettingsPage struct {
	Label     string `json:"label"`
	Component string `json:"component"`
}

type ExtensionValidationFinding struct {
	Field    string `json:"field,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type ExtensionValidationResponse struct {
	Valid               bool                         `json:"valid"`
	CompatibilityStatus string                       `json:"compatibility_status"`
	Checksum            string                       `json:"checksum"`
	Manifest            ExtensionManifest            `json:"manifest"`
	Warnings            []ExtensionValidationFinding `json:"warnings"`
	Errors              []ExtensionValidationFinding `json:"errors"`
}

type ExtensionRecordResponse struct {
	ID                  uuid.UUID         `json:"id"`
	Name                string            `json:"name"`
	DisplayName         string            `json:"display_name"`
	Version             string            `json:"version"`
	Source              string            `json:"source"`
	Checksum            string            `json:"checksum"`
	Enabled             bool              `json:"enabled"`
	CompatibilityStatus string            `json:"compatibility_status"`
	Manifest            ExtensionManifest `json:"manifest"`
	InstalledAt         string            `json:"installed_at"`
	UpdatedAt           string            `json:"updated_at"`
}

type ExtensionListResponse struct {
	Items          []ExtensionRecordResponse `json:"items"`
	SampleManifest ExtensionManifest         `json:"sample_manifest"`
}

type InstallExtensionRequest struct {
	Manifest ExtensionManifest `json:"manifest"`
	Source   string            `json:"source,omitempty"`
	Enable   bool              `json:"enable,omitempty"`
}

var extensionNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func (h *ExtensionHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Extension registry is not configured")
		return
	}
	rows, err := h.queries.ListUIExtensions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_failed", "Failed to list extensions")
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

func (h *ExtensionHandler) SampleManifest(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, sampleExtensionManifest())
}

func (h *ExtensionHandler) Validate(w http.ResponseWriter, r *http.Request) {
	manifest, err := decodeExtensionManifest(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_manifest", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, validateExtensionManifest(manifest, h.current))
}

func (h *ExtensionHandler) Install(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Extension registry is not configured")
		return
	}
	var req InstallExtensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_json", "Invalid JSON body")
		return
	}
	validation := validateExtensionManifest(req.Manifest, h.current)
	if !validation.Valid {
		RespondJSON(w, http.StatusBadRequest, validation)
		return
	}
	enabled := req.Enable && validation.CompatibilityStatus == "compatible"
	manifestBytes, _ := json.Marshal(validation.Manifest)
	row, err := h.queries.UpsertUIExtension(r.Context(), sqlc.UpsertUIExtensionParams{
		Name:                validation.Manifest.Name,
		DisplayName:         extensionDisplayName(validation.Manifest),
		Version:             validation.Manifest.Version,
		Source:              sanitizeExtensionSource(req.Source),
		Checksum:            validation.Checksum,
		Enabled:             enabled,
		CompatibilityStatus: validation.CompatibilityStatus,
		Manifest:            manifestBytes,
		InstalledBy:         currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "install_failed", "Failed to install extension")
		return
	}
	recordAudit(r, h.auditor, "admin.extension.installed", "ui_extension", row.ID.String(), row.Name, map[string]any{
		"name":                 row.Name,
		"version":              row.Version,
		"enabled":              row.Enabled,
		"compatibility_status": row.CompatibilityStatus,
	})
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Extension registry is not configured")
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if !extensionNameRE.MatchString(name) {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_name", "Invalid extension name")
		return
	}
	if enabled {
		existing, err := h.findExtension(r.Context(), name)
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, "not_found", "Extension not found")
			return
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "lookup_failed", "Failed to read extension")
			return
		}
		if existing.CompatibilityStatus != "compatible" {
			RespondRequestError(w, r, http.StatusConflict, "incompatible_extension", "Incompatible extensions cannot be enabled")
			return
		}
	}
	row, err := h.queries.SetUIExtensionEnabled(r.Context(), sqlc.SetUIExtensionEnabledParams{Name: name, Enabled: enabled})
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Extension not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "update_failed", "Failed to update extension")
		return
	}
	action := "admin.extension.disabled"
	if enabled {
		action = "admin.extension.enabled"
	}
	recordAudit(r, h.auditor, action, "ui_extension", row.ID.String(), row.Name, map[string]any{
		"name":    row.Name,
		"version": row.Version,
	})
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

func decodeExtensionManifest(r *http.Request) (ExtensionManifest, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return ExtensionManifest{}, fmt.Errorf("invalid JSON body")
	}
	var wrapped struct {
		Manifest ExtensionManifest `json:"manifest"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Manifest.Name != "" {
		return wrapped.Manifest, nil
	}
	var manifest ExtensionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ExtensionManifest{}, fmt.Errorf("manifest must be a JSON object")
	}
	return manifest, nil
}

func validateExtensionManifest(manifest ExtensionManifest, currentVersion string) ExtensionValidationResponse {
	out := ExtensionValidationResponse{
		Manifest:            normalizeExtensionManifest(manifest),
		CompatibilityStatus: "unknown",
		Warnings:            []ExtensionValidationFinding{},
		Errors:              []ExtensionValidationFinding{},
	}
	out.Checksum = extensionChecksum(out.Manifest)
	if out.Manifest.APIVersion != "extensions.astronomer.io/v1alpha1" {
		out.Errors = append(out.Errors, extensionError("apiVersion", "apiVersion must be extensions.astronomer.io/v1alpha1"))
	}
	if !extensionNameRE.MatchString(out.Manifest.Name) {
		out.Errors = append(out.Errors, extensionError("name", "name must be DNS-label compatible"))
	}
	if out.Manifest.DisplayName == "" {
		out.Warnings = append(out.Warnings, extensionWarning("displayName", "displayName is empty; name will be shown in the UI"))
	}
	if _, err := semver.NewVersion(strings.TrimPrefix(out.Manifest.Version, "v")); err != nil {
		out.Errors = append(out.Errors, extensionError("version", "version must be valid semver"))
	}
	status, compatibilityWarning := extensionCompatibility(out.Manifest.CompatibleAstronomer, currentVersion)
	out.CompatibilityStatus = status
	if compatibilityWarning != "" {
		out.Warnings = append(out.Warnings, extensionWarning("compatibleAstronomer", compatibilityWarning))
	}
	if status == "incompatible" {
		out.Errors = append(out.Errors, extensionError("compatibleAstronomer", "extension is incompatible with this Astronomer version"))
	}
	if !safeExtensionEntry(out.Manifest.Entry) {
		out.Errors = append(out.Errors, extensionError("entry", "entry must be a relative JavaScript bundle path"))
	}
	if !hasExtensionPoints(out.Manifest.ExtensionPoints) {
		out.Errors = append(out.Errors, extensionError("extensionPoints", "at least one extension point is required"))
	}
	validateExtensionPoints(out.Manifest.ExtensionPoints, &out)
	for _, permission := range out.Manifest.Permissions {
		if !validExtensionPermission(permission) {
			out.Errors = append(out.Errors, extensionError("permissions", "permission must use resource:verb format: "+permission))
		}
	}
	validateExtensionCSP(out.Manifest.CSP, &out)
	out.Valid = len(out.Errors) == 0
	return out
}

func normalizeExtensionManifest(in ExtensionManifest) ExtensionManifest {
	in.APIVersion = strings.TrimSpace(in.APIVersion)
	in.Name = strings.TrimSpace(in.Name)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Version = strings.TrimSpace(in.Version)
	in.CompatibleAstronomer = strings.TrimSpace(in.CompatibleAstronomer)
	in.Entry = strings.TrimSpace(in.Entry)
	in.Permissions = trimStringSlice(in.Permissions)
	in.BackendAPIScopes = trimStringSlice(in.BackendAPIScopes)
	return in
}

func extensionCompatibility(rangeExpr, currentVersion string) (string, string) {
	if strings.TrimSpace(rangeExpr) == "" {
		return "unknown", "compatibleAstronomer is empty; extension will fail closed until a range is declared"
	}
	if currentVersion == "" || currentVersion == "dev" {
		if _, err := semver.NewConstraint(rangeExpr); err != nil {
			return "unknown", "compatibleAstronomer is not a valid semver constraint"
		}
		return "compatible", ""
	}
	constraint, err := semver.NewConstraint(rangeExpr)
	if err != nil {
		return "unknown", "compatibleAstronomer is not a valid semver constraint"
	}
	current, err := semver.NewVersion(strings.TrimPrefix(currentVersion, "v"))
	if err != nil {
		return "unknown", "current Astronomer version is not semver; compatibility cannot be proven"
	}
	if !constraint.Check(current) {
		return "incompatible", ""
	}
	return "compatible", ""
}

func safeExtensionEntry(entry string) bool {
	if entry == "" || strings.HasPrefix(entry, "/") || strings.Contains(entry, "..") {
		return false
	}
	if u, err := url.Parse(entry); err == nil && u.Scheme != "" {
		return false
	}
	return path.Ext(entry) == ".js"
}

func hasExtensionPoints(points ExtensionManifestPoints) bool {
	return len(points.Sidebar)+len(points.Widgets)+len(points.ClusterTabs)+len(points.Settings) > 0
}

func validateExtensionPoints(points ExtensionManifestPoints, out *ExtensionValidationResponse) {
	for i, item := range points.Sidebar {
		field := fmt.Sprintf("extensionPoints.sidebar[%d]", i)
		if strings.TrimSpace(item.Label) == "" {
			out.Errors = append(out.Errors, extensionError(field+".label", "sidebar label is required"))
		}
		if !strings.HasPrefix(item.Path, "/dashboard/extensions/") {
			out.Errors = append(out.Errors, extensionError(field+".path", "sidebar path must live under /dashboard/extensions/"))
		}
	}
	for i, tab := range points.ClusterTabs {
		field := fmt.Sprintf("extensionPoints.clusterTabs[%d]", i)
		if strings.TrimSpace(tab.Label) == "" || strings.TrimSpace(tab.Component) == "" {
			out.Errors = append(out.Errors, extensionError(field, "cluster tab label and component are required"))
		}
	}
	for i, widget := range points.Widgets {
		field := fmt.Sprintf("extensionPoints.widgets[%d]", i)
		if strings.TrimSpace(widget.ID) == "" || strings.TrimSpace(widget.Title) == "" {
			out.Errors = append(out.Errors, extensionError(field, "widget id and title are required"))
		}
	}
}

func validExtensionPermission(permission string) bool {
	parts := strings.Split(strings.TrimSpace(permission), ":")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func validateExtensionCSP(csp ExtensionCSP, out *ExtensionValidationResponse) {
	for _, source := range csp.ScriptSrc {
		trimmed := strings.TrimSpace(source)
		switch trimmed {
		case "'unsafe-eval'", "'unsafe-inline'", "*":
			out.Errors = append(out.Errors, extensionError("csp.scriptSrc", "scriptSrc cannot include "+trimmed))
		}
	}
	for _, source := range append(append(csp.ConnectSrc, csp.FrameSrc...), csp.ImageSrc...) {
		if strings.TrimSpace(source) == "*" {
			out.Warnings = append(out.Warnings, extensionWarning("csp", "wildcard non-script CSP source should be narrowed before production use"))
		}
	}
}

func extensionChecksum(manifest ExtensionManifest) string {
	raw, _ := json.Marshal(manifest)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func extensionRecordResponse(row sqlc.UIExtension) ExtensionRecordResponse {
	var manifest ExtensionManifest
	_ = json.Unmarshal(row.Manifest, &manifest)
	return ExtensionRecordResponse{
		ID:                  row.ID,
		Name:                row.Name,
		DisplayName:         row.DisplayName,
		Version:             row.Version,
		Source:              row.Source,
		Checksum:            row.Checksum,
		Enabled:             row.Enabled,
		CompatibilityStatus: row.CompatibilityStatus,
		Manifest:            manifest,
		InstalledAt:         row.InstalledAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:           row.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func extensionDisplayName(manifest ExtensionManifest) string {
	if manifest.DisplayName != "" {
		return manifest.DisplayName
	}
	return manifest.Name
}

func sanitizeExtensionSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "manual"
	}
	if len(source) > 128 {
		return source[:128]
	}
	return source
}

func trimStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func extensionError(field, message string) ExtensionValidationFinding {
	return ExtensionValidationFinding{Field: field, Severity: "error", Message: message}
}

func extensionWarning(field, message string) ExtensionValidationFinding {
	return ExtensionValidationFinding{Field: field, Severity: "warning", Message: message}
}

func sampleExtensionManifest() ExtensionManifest {
	return ExtensionManifest{
		APIVersion:           "extensions.astronomer.io/v1alpha1",
		Name:                 "cost-insights",
		DisplayName:          "Cost Insights",
		Version:              "0.1.0",
		CompatibleAstronomer: ">=0.9.0 <1.0.0",
		Entry:                "index.js",
		Permissions:          []string{"clusters:read", "monitoring:read"},
		CSP: ExtensionCSP{
			ConnectSrc: []string{"'self'"},
			ImageSrc:   []string{"'self'", "data:"},
		},
		ExtensionPoints: ExtensionManifestPoints{
			Sidebar: []ExtensionSidebarPoint{{
				Label: "Cost",
				Path:  "/dashboard/extensions/cost-insights",
			}},
			Widgets: []ExtensionWidgetPoint{{
				ID:    "cost-summary",
				Title: "Cost summary",
			}},
			ClusterTabs: []ExtensionClusterTab{{
				Label:     "Cost",
				Component: "ClusterCostTab",
			}},
		},
	}
}
