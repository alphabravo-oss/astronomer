package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

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

var extensionNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func (h *ExtensionHandler) Validate(w http.ResponseWriter, r *http.Request) {
	manifest, err := decodeExtensionManifest(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidManifest, err.Error())
		return
	}
	validation := validateExtensionManifest(manifest, h.current)
	h.warnBundleGated(&validation)
	RespondJSON(w, http.StatusOK, validation)
}

// warnBundleGated emits the fail-closed signing warning when a manifest ships
// an executable (Tier-2) bundle but no trusted key is configured. The mounting
// gate is enforced at install (enabled forced false) and in /mounts/.
func (h *ExtensionHandler) warnBundleGated(v *ExtensionValidationResponse) {
	if h.trustedKey == nil && manifestHasBundle(v.Manifest) {
		v.Warnings = append(v.Warnings, extensionWarning("extensionPoints", "executable bundles gated: no trusted key"))
	}
}

// manifestHasBundle reports whether any extension point declares a Tier-2
// bundle render.
func manifestHasBundle(m ExtensionManifest) bool {
	has := func(r *ExtensionRender) bool { return r != nil && r.Bundle != nil }
	for _, p := range m.ExtensionPoints.Sidebar {
		if has(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.Widgets {
		if has(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.ClusterTabs {
		if has(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.Settings {
		if has(p.Render) {
			return true
		}
	}
	return false
}

func decodeExtensionManifest(r *http.Request) (ExtensionManifest, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return ExtensionManifest{}, fmt.Errorf("invalid JSON body")
	}
	// openapi:request-operation postExtensionsValidate
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
	validateExtensionPoints(out.Manifest, &out)
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

func validateExtensionPoints(manifest ExtensionManifest, out *ExtensionValidationResponse) {
	points := manifest.ExtensionPoints
	perms := permissionSet(manifest.Permissions)
	for i, item := range points.Sidebar {
		field := fmt.Sprintf("extensionPoints.sidebar[%d]", i)
		if strings.TrimSpace(item.Label) == "" {
			out.Errors = append(out.Errors, extensionError(field+".label", "sidebar label is required"))
		}
		if !strings.HasPrefix(item.Path, "/dashboard/extensions/") {
			out.Errors = append(out.Errors, extensionError(field+".path", "sidebar path must live under /dashboard/extensions/"))
		}
		validateExtensionPointRender(field, item.Render, item.DataSources, perms, out)
	}
	for i, tab := range points.ClusterTabs {
		field := fmt.Sprintf("extensionPoints.clusterTabs[%d]", i)
		if strings.TrimSpace(tab.Label) == "" || strings.TrimSpace(tab.Component) == "" {
			out.Errors = append(out.Errors, extensionError(field, "cluster tab label and component are required"))
		}
		validateExtensionPointRender(field, tab.Render, tab.DataSources, perms, out)
	}
	for i, widget := range points.Widgets {
		field := fmt.Sprintf("extensionPoints.widgets[%d]", i)
		if strings.TrimSpace(widget.ID) == "" || strings.TrimSpace(widget.Title) == "" {
			out.Errors = append(out.Errors, extensionError(field, "widget id and title are required"))
		}
		validateExtensionPointRender(field, widget.Render, widget.DataSources, perms, out)
	}
	for i, page := range points.Settings {
		field := fmt.Sprintf("extensionPoints.settings[%d]", i)
		if strings.TrimSpace(page.Label) == "" || strings.TrimSpace(page.Component) == "" {
			out.Errors = append(out.Errors, extensionError(field, "settings label and component are required"))
		}
		validateExtensionPointRender(field, page.Render, page.DataSources, perms, out)
	}
}

func permissionSet(permissions []string) map[string]bool {
	set := make(map[string]bool, len(permissions))
	for _, p := range permissions {
		set[strings.TrimSpace(p)] = true
	}
	return set
}

// allowedProxies, allowedDataMethods, allowedScopes and allowedFormats are the
// closed enums the declarative spec is validated against.
var (
	allowedProxies      = map[string]bool{"astronomer-api": true, "k8s": true, "prometheus": true}
	allowedDataMethods  = map[string]bool{"GET": true, "POST": true}
	allowedScopes       = map[string]bool{"global": true, "cluster": true, "project": true}
	allowedWidgetKinds  = map[string]bool{"table": true, "chart": true, "stat": true, "form": true}
	allowedFieldFormats = map[string]bool{"text": true, "number": true, "bytes": true, "datetime": true, "duration": true, "badge": true, "currency": true}
	allowedInputTypes   = map[string]bool{"text": true, "number": true, "select": true, "toggle": true}
	allowedChartTypes   = map[string]bool{"line": true, "bar": true, "area": true}
	allowedDataShapes   = map[string]bool{"list": true, "object": true, "series": true}
	allowedPathTokens   = map[string]bool{"clusterId": true, "projectId": true, "namespace": true}
	writeVerbs          = map[string]bool{"create": true, "update": true, "delete": true}

	bundleSHA256RE    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pathPlaceholderRE = regexp.MustCompile(`\{([^}]*)\}`)
)

// validateExtensionPointRender validates the Tier-1/Tier-2 render spec and the
// data sources declared on a single extension point. dataSources are the
// Tier-1 sources living on the point; Tier-2 sources live inside the bundle
// descriptor and are validated there.
func validateExtensionPointRender(field string, render *ExtensionRender, dataSources []DataSourceRef, perms map[string]bool, out *ExtensionValidationResponse) {
	ids := map[string]bool{}
	for i, ds := range dataSources {
		id := validateDataSourceRef(fmt.Sprintf("%s.dataSources[%d]", field, i), ds, perms, out)
		if id != "" {
			ids[id] = true
		}
	}
	if render == nil {
		return
	}
	if render.Declarative != nil && render.Bundle != nil {
		out.Errors = append(out.Errors, extensionError(field+".render", "render: a point is either declarative or bundle, not both"))
		return
	}
	if render.Declarative != nil {
		validateDeclarativeWidget(field+".render.declarative", render.Declarative, ids, dataSources, out)
	}
	if render.Bundle != nil {
		validateBundleDescriptor(field+".render.bundle", render.Bundle, perms, out)
	}
}

// validateDataSourceRef validates a single DataSourceRef and returns its ID
// (empty if the ref is unusable).
func validateDataSourceRef(field string, ds DataSourceRef, perms map[string]bool, out *ExtensionValidationResponse) string {
	id := strings.TrimSpace(ds.ID)
	if id == "" {
		out.Errors = append(out.Errors, extensionError(field+".id", "dataSource id is required"))
	}
	if !allowedProxies[ds.Proxy] {
		out.Errors = append(out.Errors, extensionError(field+".proxy", "proxy must be one of astronomer-api|k8s|prometheus"))
	}
	if !allowedDataMethods[ds.Method] {
		out.Errors = append(out.Errors, extensionError(field+".method", "method must be GET or POST"))
	}
	if !allowedDataShapes[ds.Shape] {
		out.Errors = append(out.Errors, extensionError(field+".shape", "shape must be one of list|object|series"))
	}
	validateDataSourcePath(field+".path", ds.Path, ds.Proxy, out)
	for _, f := range ds.Fields {
		if strings.TrimSpace(f) == "*" || f == "" {
			out.Errors = append(out.Errors, extensionError(field+".fields", "fields must be explicit dot-paths; '*' is rejected"))
			break
		}
	}
	// RBAC requirement: canonical, non-wildcard, valid scope.
	if !rbac.IsCanonicalResource(ds.RBAC.Resource) || ds.RBAC.Resource == "*" {
		out.Errors = append(out.Errors, extensionError(field+".rbac.resource", "rbac.resource must be a canonical resource and not '*'"))
	}
	if !rbac.IsCanonicalVerb(ds.RBAC.Verb) || ds.RBAC.Verb == "*" {
		out.Errors = append(out.Errors, extensionError(field+".rbac.verb", "rbac.verb must be a canonical verb and not '*'"))
	}
	if !allowedScopes[ds.RBAC.Scope] {
		out.Errors = append(out.Errors, extensionError(field+".rbac.scope", "rbac.scope must be one of global|cluster|project"))
	}
	// RBAC ceiling: resource:verb must be declared in permissions[].
	if ds.RBAC.Resource != "" && ds.RBAC.Verb != "" {
		if !perms[ds.RBAC.Resource+":"+ds.RBAC.Verb] {
			out.Errors = append(out.Errors, extensionError(field+".rbac", "not declared in permissions[]"))
		}
	}
	// Write sources require a write verb.
	if ds.Method == "POST" && !writeVerbs[ds.RBAC.Verb] {
		out.Errors = append(out.Errors, extensionError(field+".rbac.verb", "POST dataSource requires a write verb (create|update|delete)"))
	}
	return id
}

func validateDataSourcePath(field, p, proxy string, out *ExtensionValidationResponse) {
	if !strings.HasPrefix(p, "/") {
		out.Errors = append(out.Errors, extensionError(field, "path must start with /"))
		return
	}
	if strings.Contains(p, "..") {
		out.Errors = append(out.Errors, extensionError(field, "path must not contain '..'"))
		return
	}
	if proxy == "astronomer-api" && !strings.HasPrefix(p, "/api/v1/") {
		out.Errors = append(out.Errors, extensionError(field, "astronomer-api path must start with /api/v1/"))
	}
	for _, m := range pathPlaceholderRE.FindAllStringSubmatch(p, -1) {
		if !allowedPathTokens[m[1]] {
			out.Errors = append(out.Errors, extensionError(field, "path placeholder {"+m[1]+"} is not one of {clusterId},{projectId},{namespace}"))
		}
	}
}

func validateDeclarativeWidget(field string, w *DeclarativeWidget, ids map[string]bool, dataSources []DataSourceRef, out *ExtensionValidationResponse) {
	if !allowedWidgetKinds[w.Kind] {
		out.Errors = append(out.Errors, extensionError(field+".kind", "kind must be one of table|chart|stat|form"))
	}
	if !ids[strings.TrimSpace(w.DataSource)] {
		out.Errors = append(out.Errors, extensionError(field+".dataSource", "dataSource must reference a declared dataSources[].id"))
	}
	for i, fb := range w.Fields {
		if fb.Format != "" && !allowedFieldFormats[fb.Format] {
			out.Errors = append(out.Errors, extensionError(fmt.Sprintf("%s.fields[%d].format", field, i), "field format must be one of text|number|bytes|datetime|duration|badge|currency"))
		}
	}
	if w.Chart != nil && !allowedChartTypes[w.Chart.Type] {
		out.Errors = append(out.Errors, extensionError(field+".chart.type", "chart type must be one of line|bar|area"))
	}
	if w.Stat != nil && w.Stat.Value.Format != "" && !allowedFieldFormats[w.Stat.Value.Format] {
		out.Errors = append(out.Errors, extensionError(field+".stat.value.format", "field format must be one of text|number|bytes|datetime|duration|badge|currency"))
	}
	if w.Form != nil {
		submit := strings.TrimSpace(w.Form.Submit)
		if !ids[submit] {
			out.Errors = append(out.Errors, extensionError(field+".form.submit", "form.submit must reference a declared dataSources[].id"))
		} else {
			for _, ds := range dataSources {
				if ds.ID == submit && ds.Method != "POST" {
					out.Errors = append(out.Errors, extensionError(field+".form.submit", "form.submit dataSource must use method POST"))
				}
			}
		}
		for i, in := range w.Form.Inputs {
			if !allowedInputTypes[in.Type] {
				out.Errors = append(out.Errors, extensionError(fmt.Sprintf("%s.form.inputs[%d].type", field, i), "input type must be one of text|number|select|toggle"))
			}
		}
	}
}

func validateBundleDescriptor(field string, b *BundleDescriptor, perms map[string]bool, out *ExtensionValidationResponse) {
	if !bundleSHA256RE.MatchString(b.SHA256) {
		out.Errors = append(out.Errors, extensionError(field+".sha256", "sha256 must match sha256:<64 hex chars>"))
	}
	if !strings.HasPrefix(b.Integrity, "sha384-") {
		out.Errors = append(out.Errors, extensionError(field+".integrity", "integrity must be an SRI sha384- digest"))
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b.Signature)); err != nil || strings.TrimSpace(b.Signature) == "" {
		out.Errors = append(out.Errors, extensionError(field+".signature", "signature must be base64 Ed25519"))
	}
	if u, err := url.Parse(b.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		out.Errors = append(out.Errors, extensionError(field+".url", "url must be an absolute https URL"))
	}
	if !safeExtensionEntry(b.Entry) {
		out.Errors = append(out.Errors, extensionError(field+".entry", "entry must be a relative JavaScript bundle path"))
	}
	hostOK := false
	if so, err := url.Parse(b.SandboxOrigin); err == nil && so.Scheme == "https" && so.Host != "" && so.Path == "" {
		hostOK = true
	}
	if !hostOK {
		out.Errors = append(out.Errors, extensionError(field+".sandboxOrigin", "sandboxOrigin must be an absolute https origin"))
	}
	if len(b.CSP.FrameSrc) == 0 {
		out.Errors = append(out.Errors, extensionError(field+".csp.frameSrc", "bundle requires csp.frameSrc"))
	}
	for _, src := range b.CSP.ConnectSrc {
		s := strings.TrimSpace(src)
		if s == "*" || strings.Contains(s, "/api/") {
			out.Errors = append(out.Errors, extensionError(field+".csp.connectSrc", "bundle connectSrc must not be '*' or any /api/ host"))
			break
		}
	}
	for _, source := range b.CSP.ScriptSrc {
		switch strings.TrimSpace(source) {
		case "'unsafe-eval'", "'unsafe-inline'", "*":
			out.Errors = append(out.Errors, extensionError(field+".csp.scriptSrc", "scriptSrc cannot include "+strings.TrimSpace(source)))
		}
	}
	// Tier-2 bundle data sources live inside the descriptor.
	for i, ds := range b.DataSources {
		validateDataSourceRef(fmt.Sprintf("%s.dataSources[%d]", field, i), ds, perms, out)
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
