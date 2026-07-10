// Package argosecurity owns the shared Argo API request and response policy.
// Argo copies Application sources into spec, operation, status and history, so
// callers must not try to sanitize only one response shape or one nesting level.
package argosecurity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/redaction"
)

const Marker = redaction.Marker

const (
	maxDocumentDepth       = 12
	maxStructuredStringLen = 1 << 20
	maxGeneratorEntries    = 64
	maxGeneratorScalarLen  = 512
)

var forbiddenSourceKeys = map[string]struct{}{
	"values": {}, "valuesobject": {}, "parameters": {}, "fileparameters": {},
	"plugin": {}, "env": {}, "envs": {}, "manifests": {}, "info": {},
}

var applicationSourceKeys = map[string]struct{}{
	"repourl": {}, "path": {}, "targetrevision": {}, "chart": {}, "ref": {}, "helm": {}, "kustomize": {}, "directory": {},
}

var helmSourceKeys = map[string]struct{}{
	"releasename": {}, "version": {}, "values": {}, "valuesobject": {}, "parameters": {}, "fileparameters": {}, "valuefiles": {},
}

var kustomizeSourceKeys = map[string]struct{}{
	"nameprefix": {}, "namesuffix": {}, "images": {},
}

var directorySourceKeys = map[string]struct{}{
	"recurse": {},
}

// Sanitize returns a detached JSON-shaped value safe for API/UI responses.
// Values that cannot be represented as JSON fail closed to a redaction marker.
func Sanitize(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return Marker
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return Marker
	}
	return sanitizeArgoValue(decoded, sanitizeContext{})
}

// SanitizeJSON sanitizes one complete JSON document.
func SanitizeJSON(raw []byte) ([]byte, error) {
	value, err := decodeJSONDocument(raw)
	if err != nil {
		return nil, fmt.Errorf("decode Argo JSON response: %w", err)
	}
	return json.Marshal(sanitizeArgoValue(value, sanitizeContext{}))
}

type sanitizeContext struct {
	parentKey string
	kind      string
	depth     int
}

func sanitizeArgoValue(value any, ctx sanitizeContext) any {
	if ctx.depth > maxDocumentDepth {
		return Marker
	}
	switch typed := value.(type) {
	case map[string]any:
		kind, _ := typed["kind"].(string)
		if kind == "" {
			kind = ctx.kind
		}
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized := normalizeKey(key)
			if shouldRedactKey(normalized, ctx.parentKey, kind) && !emptyJSONValue(item) {
				out[key] = redactShape(item)
				continue
			}
			child := sanitizeContext{parentKey: normalized, kind: kind, depth: ctx.depth + 1}
			if (normalized == "manifests" || normalized == "manifest") && !emptyJSONValue(item) {
				out[key] = sanitizeManifestValue(item)
				continue
			}
			if (strings.EqualFold(kind, "ConfigMap") || strings.EqualFold(kind, "ConfigMapList")) && normalized == "data" {
				out[key] = sanitizeConfigMapData(item)
				continue
			}
			out[key] = sanitizeArgoValue(item, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeArgoValue(item, sanitizeContext{parentKey: ctx.parentKey, kind: ctx.kind, depth: ctx.depth + 1})
		}
		return out
	case string:
		return sanitizeStringDocument(typed, ctx)
	default:
		return typed
	}
}

func sanitizeStringDocument(value string, ctx sanitizeContext) string {
	trimmed := strings.TrimSpace(value)
	if looksLikeJSONWrapper(trimmed) {
		if len(value) > maxStructuredStringLen {
			return Marker
		}
		decoded, err := decodeJSONDocument([]byte(trimmed))
		if err != nil {
			return Marker
		}
		if text, ok := decoded.(string); ok && !looksLikeJSONWrapper(strings.TrimSpace(text)) {
			return sanitizeString(value)
		}
		safe := sanitizeArgoValue(decoded, sanitizeContext{parentKey: ctx.parentKey, kind: ctx.kind, depth: ctx.depth + 1})
		if raw, marshalErr := json.Marshal(safe); marshalErr == nil {
			return string(raw)
		}
		return Marker
	}
	return sanitizeString(value)
}

func shouldRedactKey(key, parentKey, kind string) bool {
	if (key == "data" || key == "stringdata") && (strings.EqualFold(kind, "Secret") || strings.EqualFold(kind, "SecretList")) {
		return true
	}
	if parentKey == "helm" {
		switch key {
		case "values", "valuesobject", "parameters", "fileparameters", "valuefiles":
			return true
		}
	}
	if referenceKey(key) {
		return false
	}
	if redaction.IsSensitiveKey(key) {
		return true
	}
	for _, fragment := range []string{"password", "passwd", "token", "apikey", "privatekey", "secretkey", "clientsecret", "clientcertkey", "credential", "kubeconfig"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func referenceKey(key string) bool {
	return key == "secretname" || key == "existingsecret" || key == "secretref" || key == "secretkeyref" ||
		strings.HasSuffix(key, "secretname") || strings.HasSuffix(key, "secretref")
}

func redactShape(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key := range typed {
			out[key] = Marker
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = Marker
		}
		return out
	default:
		return Marker
	}
}

func sanitizeManifestValue(value any) any {
	switch typed := value.(type) {
	case string:
		return sanitizeStructuredString(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeManifestValue(item)
		}
		return out
	default:
		return sanitizeArgoValue(value, sanitizeContext{})
	}
}

func sanitizeConfigMapData(value any) any {
	items, ok := value.(map[string]any)
	if !ok {
		return sanitizeArgoValue(value, sanitizeContext{})
	}
	out := make(map[string]any, len(items))
	for key, item := range items {
		if shouldRedactKey(normalizeKey(key), "data", "ConfigMap") {
			out[key] = Marker
			continue
		}
		if text, ok := item.(string); ok {
			out[key] = sanitizeStructuredString(text)
			continue
		}
		out[key] = sanitizeArgoValue(item, sanitizeContext{})
	}
	return out
}

func sanitizeStructuredString(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") && !strings.Contains(value, "\n") && !strings.Contains(value, ":") {
		return sanitizeString(value)
	}
	if looksLikeJSONWrapper(trimmed) {
		if len(value) > maxStructuredStringLen {
			return Marker
		}
		decoded, err := decodeJSONDocument([]byte(value))
		if err != nil {
			return Marker
		}
		if raw, err := json.Marshal(sanitizeArgoValue(decoded, sanitizeContext{depth: 1})); err == nil {
			return string(raw)
		}
		return Marker
	}
	if len(value) <= maxStructuredStringLen {
		if decoded, err := decodeJSONDocument([]byte(value)); err == nil {
			if raw, err := json.Marshal(sanitizeArgoValue(decoded, sanitizeContext{depth: 1})); err == nil {
				return string(raw)
			}
		}
	}
	var yamlValue any
	if yaml.Unmarshal([]byte(value), &yamlValue) == nil {
		if raw, err := yaml.Marshal(sanitizeArgoValue(yamlValue, sanitizeContext{})); err == nil {
			return string(raw)
		}
	}
	return sanitizeString(value)
}

func sanitizeString(value string) string {
	value = embeddedHTTPURL.ReplaceAllStringFunc(value, sanitizeURLDiagnostic)
	value = cloudCredential.ReplaceAllString(value, "$1="+Marker)
	value = credentialAssignment.ReplaceAllString(value, "$1="+Marker)
	return redaction.String(value)
}

func sanitizeURLDiagnostic(value string) string {
	trimmed := strings.TrimRight(value, ").,;>]")
	suffix := strings.TrimPrefix(value, trimmed)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return value
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String() + suffix
}

func embeddedCredentialURL(value string) bool {
	for _, candidate := range embeddedHTTPURL.FindAllString(value, -1) {
		candidate = strings.TrimRight(candidate, ").,;>]")
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
			return true
		}
	}
	return false
}

// SanitizeString removes credential fragments from operational text and URLs
// while retaining non-sensitive context such as host, path, phase and message.
func SanitizeString(value string) string {
	return sanitizeString(value)
}

// ValidateMutation rejects Application source forms that can persist secret
// material or bypass the closed typed source model. It accepts safe ordinary
// action bodies (for example sync revision/prune) and reference-only sources.
func ValidateMutation(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Argo mutation for validation: %w", err)
	}
	decoded, err := decodeJSONDocument(raw)
	if err != nil {
		return fmt.Errorf("decode Argo mutation for validation: %w", err)
	}
	return validateMutationValue(decoded, mutationContext{})
}

// ValidateMutationJSON validates one complete JSON request document.
func ValidateMutationJSON(raw []byte) error {
	return validateMutationJSON(raw, mutationContext{})
}

// ValidateMutationJSONForPath adds only endpoint-specific Argo schema rules.
// Today that is the AppProject destination wildcard, which must never be
// accepted merely because an unrelated payload happens to contain a
// `destinations[].server` shape.
func ValidateMutationJSONForPath(raw []byte, requestPath string) error {
	return validateMutationJSON(raw, mutationContext{allowProjectDestinationWildcard: isProjectRequestPath(requestPath)})
}

func validateMutationJSON(raw []byte, ctx mutationContext) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	value, err := decodeJSONDocument(raw)
	if err != nil {
		return fmt.Errorf("invalid Argo JSON mutation: %w", err)
	}
	switch value.(type) {
	case map[string]any, []any:
	default:
		return fmt.Errorf("Argo JSON mutation must be an object or array")
	}
	return validateMutationValue(value, ctx)
}

func isProjectRequestPath(requestPath string) bool {
	clean := strings.TrimSuffix(strings.Split(requestPath, "?")[0], "/")
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "api" && parts[i+1] == "v1" && parts[i+2] == "projects" {
			return true
		}
	}
	return false
}

func decodeJSONDocument(raw []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

type mutationContext struct {
	inSource                        bool
	path                            string
	depth                           int
	allowProjectDestinationWildcard bool
}

func validateMutationValue(value any, ctx mutationContext) error {
	if ctx.depth > maxDocumentDepth {
		return fmt.Errorf("Argo mutation exceeds maximum document depth")
	}
	switch typed := value.(type) {
	case map[string]any:
		if err := validateJSONPatchObject(typed, ctx); err != nil {
			return err
		}
		for key, item := range typed {
			normalized := normalizeKey(key)
			path := key
			if ctx.path != "" {
				path = ctx.path + "." + key
			}
			inSource := ctx.inSource || normalized == "source"
			childInSource := inSource || normalized == "sources"
			if normalized == "source" && !emptyJSONValue(item) {
				if err := validateClosedSourceObject(item, path, false, nil); err != nil {
					return err
				}
			}
			if normalized == "sources" && !emptyJSONValue(item) {
				items, ok := item.([]any)
				if !ok || len(items) == 0 || len(items) > maxGeneratorEntries {
					return fmt.Errorf("Argo mutation sources must be a non-empty array")
				}
				refs, err := collectSourceRefs(items)
				if err != nil {
					return err
				}
				for i, source := range items {
					if err := validateClosedSourceObject(source, fmt.Sprintf("%s[%d]", path, i), true, refs); err != nil {
						return err
					}
				}
			}
			if normalized == "operation" && !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation operations are not admitted")
			}
			if (normalized == "repourl" || normalized == "repo") && !emptyJSONValue(item) {
				text, ok := item.(string)
				if !ok || ValidateRepositoryURL(text) != nil {
					return fmt.Errorf("%s must be a canonical credential-free URL", path)
				}
			}
			if (normalized == "apiurl" || normalized == "server") && !emptyJSONValue(item) {
				text, ok := item.(string)
				wildcard := normalized == "server" && text == "*" && ctx.allowProjectDestinationWildcard && projectDestinationServerPath(path)
				if !wildcard && (!ok || ValidateCredentialFreeURL(text) != nil) {
					return fmt.Errorf("%s must be a canonical credential-free URL", path)
				}
			}
			if normalized == "sourcerepos" && !emptyJSONValue(item) {
				values, ok := item.([]any)
				if !ok {
					return fmt.Errorf("%s must be an array", path)
				}
				for _, rawRepo := range values {
					repo, ok := rawRepo.(string)
					if !ok || ValidateSourceRepoPattern(repo) != nil {
						return fmt.Errorf("%s contains a non-canonical or credential-bearing repository URL", path)
					}
				}
			}
			if _, forbidden := forbiddenSourceKeys[normalized]; forbidden && (inSource || normalized == "manifests" || normalized == "info") && !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation contains a non-reference-only source field")
			}
			if normalized == "annotations" && !emptyJSONValue(item) {
				if err := validateSafeAnnotations(item); err != nil {
					return err
				}
			}
			if normalized == "elements" || (normalized == "values" && strings.Contains(strings.ToLower(ctx.path), "clusters")) {
				if err := validateGeneratorValues(item, path); err != nil {
					return err
				}
			}
			if inSource && strings.Contains(normalized, "env") && !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation environment fields are not admitted")
			}
			if inSource && shouldRejectSourceKey(normalized, item) {
				return fmt.Errorf("Argo mutation source contains inline secret material")
			}
			if normalized == "patch" {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					if err := validateEncodedMutationDocument(text, ctx); err != nil {
						return fmt.Errorf("%s: %w", path, err)
					}
				}
			}
			if err := validateMutationValue(item, mutationContext{inSource: childInSource, path: path, depth: ctx.depth + 1, allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard}); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range typed {
			if err := validateMutationValue(item, mutationContext{inSource: ctx.inSource, path: fmt.Sprintf("%s[%d]", ctx.path, i), depth: ctx.depth + 1, allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard}); err != nil {
				return err
			}
		}
	case string:
		// Ordinary descriptions, names and annotations may begin with brackets.
		// Encoded mutation documents are interpreted only by schema-known fields
		// such as `patch`, handled above.
	}
	return nil
}

func validateEncodedMutationDocument(text string, ctx mutationContext) error {
	if len(text) > maxStructuredStringLen {
		return fmt.Errorf("encoded Argo mutation exceeds inspection limit")
	}
	decoded, err := decodeJSONDocument([]byte(strings.TrimSpace(text)))
	if err != nil {
		return fmt.Errorf("encoded Argo mutation is malformed")
	}
	for depth := ctx.depth + 1; depth <= maxDocumentDepth; depth++ {
		wrapped, ok := decoded.(string)
		if !ok {
			return validateMutationValue(decoded, mutationContext{depth: depth, allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard})
		}
		if len(wrapped) > maxStructuredStringLen {
			return fmt.Errorf("encoded Argo mutation exceeds inspection limit")
		}
		decoded, err = decodeJSONDocument([]byte(strings.TrimSpace(wrapped)))
		if err != nil {
			return fmt.Errorf("encoded Argo mutation is malformed")
		}
	}
	return fmt.Errorf("encoded Argo mutation exceeds maximum document depth")
}

func projectDestinationServerPath(value string) bool {
	matched, _ := regexp.MatchString(`(^|\.)destinations(?:\[[0-9]+\]|\.[0-9]+)\.server$`, strings.ToLower(value))
	return matched
}

func looksLikeJSONWrapper(value string) bool {
	return strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "\"")
}

// validateJSONPatchObject projects RFC 6902 path/value pairs back into a
// nested document before applying the same policy. Without this step a patch
// such as {"path":"/spec/source/helm/values","value":"..."} has no
// literal source object for the recursive validator to see.
func validateJSONPatchObject(object map[string]any, ctx mutationContext) error {
	op, hasOperation := object["op"].(string)
	path, hasPath := object["path"].(string)
	if !hasOperation || !hasPath {
		return nil
	}
	var value any
	if !strings.EqualFold(op, "remove") {
		value = object["value"]
	}
	if err := validateJSONPointerMutation(path, value, ctx); err != nil {
		return err
	}
	if from, ok := object["from"].(string); ok && strings.TrimSpace(from) != "" {
		// A move/copy can disclose a sensitive source even when its destination
		// is otherwise harmless, so validate the source path as non-empty.
		if err := validateJSONPointerMutation(from, Marker, ctx); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONPointerMutation(pointer string, value any, ctx mutationContext) error {
	if pointer == "" {
		return validateMutationValue(value, ctx)
	}
	if !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("Argo JSON patch contains an invalid path")
	}
	segments := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	nested := value
	for i := len(segments) - 1; i >= 0; i-- {
		segment := strings.ReplaceAll(strings.ReplaceAll(segments[i], "~1", "/"), "~0", "~")
		nested = map[string]any{segment: nested}
	}
	if err := validateMutationValue(nested, mutationContext{allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard}); err != nil {
		return fmt.Errorf("Argo JSON patch targets a non-admitted field")
	}
	return nil
}

func validateClosedSourceObject(value any, sourcePath string, multiSource bool, refs map[string]struct{}) error {
	source, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("Argo mutation source must be an object")
	}
	for key, item := range source {
		normalized := normalizeKey(key)
		if _, allowed := applicationSourceKeys[normalized]; !allowed {
			return fmt.Errorf("Argo mutation contains a field outside the admitted source shape")
		}
		switch normalized {
		case "repourl":
			text, ok := item.(string)
			if !ok || ValidateRepositoryURL(text) != nil {
				return fmt.Errorf("%s.%s must be a canonical credential-free repository URL", sourcePath, key)
			}
		case "ref":
			text, ok := item.(string)
			if !multiSource || !ok || !safeSourceRef.MatchString(text) {
				return fmt.Errorf("%s.%s must be a constrained multi-source reference", sourcePath, key)
			}
			if chart, _ := source["chart"].(string); chart != "" {
				return fmt.Errorf("%s cannot combine ref with chart", sourcePath)
			}
		case "helm":
			if err := validateHelmSourceObject(item, sourcePath+"."+key, multiSource, refs); err != nil {
				return err
			}
		case "kustomize":
			if err := validateClosedNestedSourceObject(item, sourcePath+"."+key, kustomizeSourceKeys); err != nil {
				return err
			}
		case "directory":
			if err := validateClosedNestedSourceObject(item, sourcePath+"."+key, directorySourceKeys); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectSourceRefs(items []any) (map[string]struct{}, error) {
	refs := make(map[string]struct{})
	for i, item := range items {
		source, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Argo mutation source must be an object")
		}
		raw, exists := source["ref"]
		if !exists || emptyJSONValue(raw) {
			continue
		}
		ref, ok := raw.(string)
		if !ok || !safeSourceRef.MatchString(ref) {
			return nil, fmt.Errorf("sources[%d].ref must be a constrained identifier", i)
		}
		if _, duplicate := refs[ref]; duplicate {
			return nil, fmt.Errorf("sources[%d].ref is duplicated", i)
		}
		refs[ref] = struct{}{}
	}
	return refs, nil
}

func validateHelmSourceObject(value any, sourcePath string, multiSource bool, refs map[string]struct{}) error {
	if err := validateClosedNestedSourceObject(value, sourcePath, helmSourceKeys); err != nil {
		return err
	}
	object, _ := value.(map[string]any)
	for key, item := range object {
		normalized := normalizeKey(key)
		switch normalized {
		case "values", "valuesobject", "parameters", "fileparameters":
			if !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation contains inline Helm source material")
			}
		case "valuefiles":
			if emptyJSONValue(item) {
				continue
			}
			if !multiSource {
				return fmt.Errorf("Argo Helm valueFiles require the constrained multi-source form")
			}
			files, ok := item.([]any)
			if !ok || len(files) > maxGeneratorEntries {
				return fmt.Errorf("%s.valueFiles has invalid shape", sourcePath)
			}
			for _, rawFile := range files {
				file, ok := rawFile.(string)
				if !ok || validateHelmValueFile(file, refs) != nil {
					return fmt.Errorf("%s.valueFiles contains an unsafe reference", sourcePath)
				}
			}
		}
	}
	return nil
}

func validateHelmValueFile(value string, refs map[string]struct{}) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxGeneratorScalarLen ||
		strings.ContainsAny(value, "\\\r\n?#:") || strings.Contains(value, "{{") ||
		redaction.String(value) != value || cloudCredential.MatchString(value) || credentialAssignment.MatchString(value) || embeddedCredentialURL(value) {
		return fmt.Errorf("unsafe Helm value file")
	}
	relative := value
	if strings.HasPrefix(value, "$") {
		parts := strings.SplitN(strings.TrimPrefix(value, "$"), "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid Helm value repository reference")
		}
		if _, ok := refs[parts[0]]; !ok {
			return fmt.Errorf("unknown Helm value repository reference")
		}
		relative = parts[1]
	}
	clean := path.Clean(relative)
	if clean == "." || clean != relative || path.IsAbs(relative) || strings.HasPrefix(clean, "../") || clean == ".." {
		return fmt.Errorf("Helm value file must be a clean relative path")
	}
	return nil
}

var (
	safeTemplateScalar        = regexp.MustCompile(`^\{\{[a-zA-Z0-9_.-]{1,128}\}\}$`)
	safeNotificationRecipient = regexp.MustCompile(`^[a-zA-Z0-9_.@+-]{1,256}$`)
	safeSourceRef             = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,62}$`)
	safeSCPRepository         = regexp.MustCompile(`^git@([A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?):([A-Za-z0-9._/-]{1,512})$`)
	embeddedHTTPURL           = regexp.MustCompile(`https?://[^\s<>'"]+`)
	cloudCredential           = regexp.MustCompile(`(?i)\b(x-amz-(?:credential|signature|security-token)|googleaccessid|signature|sig|x-goog-signature)\s*=\s*[^&\s,;]+`)
	credentialAssignment      = regexp.MustCompile(`(?i)\b(credential|credentials)\s*=\s*[^&\s,;]+`)
)

// ValidateCredentialFreeURL is the shared write boundary for Argo endpoint,
// repository, source and destination URLs. Diagnostic output uses the same
// policy but strips every query and fragment rather than attempting to
// classify provider-specific signatures.
func ValidateCredentialFreeURL(raw string) error {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return fmt.Errorf("URL must be non-empty and canonical")
	}
	if safeTemplateScalar.MatchString(raw) {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return fmt.Errorf("URL must include scheme and host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return fmt.Errorf("URL credentials, query and fragment are not admitted")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "http", "ssh", "git", "oci":
	default:
		return fmt.Errorf("URL scheme is not admitted")
	}
	if parsed.String() != raw || strings.ContainsAny(parsed.Host, "\\\r\n\t") {
		return fmt.Errorf("URL must be canonical")
	}
	return nil
}

// ValidateRepositoryURL admits credential-free HTTP(S)/OCI repository URLs,
// plus the two Git transports whose conventional `git` username identifies
// the protocol rather than a secret. Passwords, arbitrary usernames, query
// strings and fragments remain forbidden.
func ValidateRepositoryURL(raw string) error {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return fmt.Errorf("repository URL must be non-empty and canonical")
	}
	if safeTemplateScalar.MatchString(raw) {
		return nil
	}
	if matches := safeSCPRepository.FindStringSubmatch(raw); matches != nil {
		clean := path.Clean(matches[2])
		if clean == "." || clean != matches[2] || path.IsAbs(matches[2]) || strings.HasPrefix(clean, "../") || strings.Contains(matches[2], "//") {
			return fmt.Errorf("SCP repository path must be canonical")
		}
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return fmt.Errorf("repository URL must include scheme and host")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return fmt.Errorf("repository URL query and fragment are not admitted")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "ssh", "git":
		if parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword || parsed.User.Username() != "git" {
				return fmt.Errorf("repository transport admits only the conventional git username")
			}
		}
	case "https", "http", "oci":
		if parsed.User != nil {
			return fmt.Errorf("repository URL userinfo is not admitted")
		}
	default:
		return fmt.Errorf("repository URL scheme is not admitted")
	}
	if parsed.String() != raw || strings.ContainsAny(parsed.Host, "\\\r\n\t") {
		return fmt.Errorf("repository URL must be canonical")
	}
	return nil
}

// ValidateSourceRepoPattern preserves Argo's documented wildcard and negated
// sourceRepos semantics while applying the credential-free URL policy.
func ValidateSourceRepoPattern(raw string) error {
	if raw == "*" {
		return nil
	}
	pattern := raw
	if strings.HasPrefix(pattern, "!") {
		pattern = strings.TrimPrefix(pattern, "!")
		if pattern == "" || pattern == "*" {
			return fmt.Errorf("negated repository pattern must be scoped")
		}
	}
	if strings.Contains(pattern, "*") {
		candidate := strings.ReplaceAll(pattern, "*", "wildcard")
		return ValidateRepositoryURL(candidate)
	}
	return ValidateRepositoryURL(pattern)
}

func validateGeneratorValues(value any, path string) error {
	validateObject := func(object map[string]any) error {
		if len(object) > maxGeneratorEntries {
			return fmt.Errorf("%s exceeds generator value limit", path)
		}
		for key, item := range object {
			if shouldRedactKey(normalizeKey(key), "generator", "") {
				return fmt.Errorf("%s contains a secret-shaped generator key", path)
			}
			switch scalar := item.(type) {
			case nil, bool, json.Number, float64:
			case string:
				if len(scalar) > maxGeneratorScalarLen || strings.ContainsAny(scalar, "\r\n") {
					return fmt.Errorf("%s contains an oversized generator scalar", path)
				}
				if redaction.String(scalar) != scalar || cloudCredential.MatchString(scalar) || credentialAssignment.MatchString(scalar) || embeddedCredentialURL(scalar) {
					return fmt.Errorf("%s contains credential-shaped generator data", path)
				}
				if strings.Contains(scalar, "://") && ValidateCredentialFreeURL(scalar) != nil {
					return fmt.Errorf("%s contains a credential-bearing generator URL", path)
				}
			default:
				return fmt.Errorf("%s generator values must be scalars", path)
			}
		}
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		return validateObject(typed)
	case []any:
		if len(typed) > maxGeneratorEntries {
			return fmt.Errorf("%s exceeds generator element limit", path)
		}
		for _, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%s generator elements must be objects", path)
			}
			if err := validateObject(object); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s generator values have invalid shape", path)
	}
}

func validateSafeAnnotations(value any) error {
	annotations, ok := value.(map[string]any)
	if !ok || len(annotations) > 32 {
		return fmt.Errorf("Argo mutation annotations have invalid shape")
	}
	for key, raw := range annotations {
		text, ok := raw.(string)
		if !ok || len(text) > maxGeneratorScalarLen || strings.ContainsAny(text, "\r\n") {
			return fmt.Errorf("Argo mutation annotation %q has an invalid value", key)
		}
		switch key {
		case "argocd.argoproj.io/sync-wave":
			wave, err := strconv.Atoi(text)
			if err != nil || wave < -100 || wave > 100 {
				return fmt.Errorf("Argo sync-wave annotation must be an integer from -100 to 100")
			}
		case "argocd.argoproj.io/compare-options":
			if text != "IgnoreExtraneous" {
				return fmt.Errorf("Argo compare-options annotation is not admitted")
			}
		default:
			if !strings.HasPrefix(key, "notifications.argoproj.io/subscribe.") ||
				!safeNotificationRecipient.MatchString(text) ||
				strings.Contains(strings.ToLower(key), "secret") {
				return fmt.Errorf("Argo mutation annotation %q is not admitted", key)
			}
		}
	}
	return nil
}

func validateClosedNestedSourceObject(value any, path string, allowed map[string]struct{}) error {
	if emptyJSONValue(value) {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("Argo mutation nested source must be an object")
	}
	for key := range object {
		if _, ok := allowed[normalizeKey(key)]; !ok {
			return fmt.Errorf("Argo mutation contains a field outside the admitted nested source shape")
		}
	}
	return nil
}

func shouldRejectSourceKey(key string, value any) bool {
	if emptyJSONValue(value) || referenceKey(key) {
		return false
	}
	if shouldRedactKey(key, "source", "") {
		return true
	}
	if (key == "repourl" || key == "repo") && !emptyJSONValue(value) {
		text, ok := value.(string)
		return !ok || ValidateRepositoryURL(text) != nil
	}
	if text, ok := value.(string); ok && strings.Contains(text, "://") {
		parsed, err := url.Parse(text)
		return err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != ""
	}
	return false
}

func emptyJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case map[string]any:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}

func normalizeKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, key)
}
