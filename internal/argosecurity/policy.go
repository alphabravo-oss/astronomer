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
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/redaction"
)

const Marker = redaction.Marker

var forbiddenSourceKeys = map[string]struct{}{
	"values": {}, "valuesobject": {}, "parameters": {}, "fileparameters": {}, "valuefiles": {},
	"plugin": {}, "env": {}, "envs": {}, "manifests": {}, "info": {}, "sources": {},
}

var applicationSourceKeys = map[string]struct{}{
	"repourl": {}, "path": {}, "targetrevision": {}, "chart": {}, "helm": {}, "kustomize": {}, "directory": {},
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
}

func sanitizeArgoValue(value any, ctx sanitizeContext) any {
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
			child := sanitizeContext{parentKey: normalized, kind: kind}
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
			out[i] = sanitizeArgoValue(item, ctx)
		}
		return out
	case string:
		return sanitizeString(typed)
	default:
		return typed
	}
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
	for _, fragment := range []string{"password", "passwd", "token", "apikey", "privatekey", "secretkey", "clientsecret", "clientcertkey", "credentials", "kubeconfig"} {
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
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		if raw, err := json.Marshal(sanitizeArgoValue(decoded, sanitizeContext{})); err == nil {
			return string(raw)
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
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		changed := false
		if parsed.User != nil {
			parsed.User = nil
			changed = true
		}
		query := parsed.Query()
		for key, values := range query {
			normalized := normalizeKey(key)
			if shouldRedactKey(normalized, "query", "") || normalized == "auth" {
				for i := range values {
					values[i] = Marker
				}
				query[key] = values
				changed = true
			}
		}
		if changed {
			parsed.RawQuery = query.Encode()
			value = parsed.String()
		}
	}
	return redaction.String(value)
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
	return validateMutationValue(value, mutationContext{})
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
	inSource bool
	path     string
}

func validateMutationValue(value any, ctx mutationContext) error {
	switch typed := value.(type) {
	case map[string]any:
		if err := validateJSONPatchObject(typed); err != nil {
			return err
		}
		for key, item := range typed {
			normalized := normalizeKey(key)
			path := key
			if ctx.path != "" {
				path = ctx.path + "." + key
			}
			inSource := ctx.inSource || normalized == "source" || normalized == "sources" || normalized == "template" || normalized == "generators" || normalized == "elements"
			if normalized == "source" && !emptyJSONValue(item) {
				if err := validateClosedSourceObject(item, path); err != nil {
					return err
				}
			}
			if normalized == "operation" && !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation operations are not admitted")
			}
			if _, forbidden := forbiddenSourceKeys[normalized]; forbidden && (inSource || normalized == "sources" || normalized == "manifests" || normalized == "info") && !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation contains a non-reference-only source field")
			}
			if normalized == "annotations" && !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation annotations are not admitted")
			}
			if inSource && strings.Contains(normalized, "env") && !emptyJSONValue(item) {
				return fmt.Errorf("Argo mutation environment fields are not admitted")
			}
			if inSource && shouldRejectSourceKey(normalized, item) {
				return fmt.Errorf("Argo mutation source contains inline secret material")
			}
			if normalized == "patch" {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					if err := ValidateMutationJSON([]byte(text)); err != nil {
						return fmt.Errorf("%s: %w", path, err)
					}
				}
			}
			if err := validateMutationValue(item, mutationContext{inSource: inSource, path: path}); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range typed {
			if err := validateMutationValue(item, mutationContext{inSource: ctx.inSource, path: fmt.Sprintf("%s[%d]", ctx.path, i)}); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateJSONPatchObject projects RFC 6902 path/value pairs back into a
// nested document before applying the same policy. Without this step a patch
// such as {"path":"/spec/source/helm/values","value":"..."} has no
// literal source object for the recursive validator to see.
func validateJSONPatchObject(object map[string]any) error {
	op, hasOperation := object["op"].(string)
	path, hasPath := object["path"].(string)
	if !hasOperation || !hasPath {
		return nil
	}
	var value any
	if !strings.EqualFold(op, "remove") {
		value = object["value"]
	}
	if err := validateJSONPointerMutation(path, value); err != nil {
		return err
	}
	if from, ok := object["from"].(string); ok && strings.TrimSpace(from) != "" {
		// A move/copy can disclose a sensitive source even when its destination
		// is otherwise harmless, so validate the source path as non-empty.
		if err := validateJSONPointerMutation(from, Marker); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONPointerMutation(pointer string, value any) error {
	if pointer == "" {
		return validateMutationValue(value, mutationContext{})
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
	if err := validateMutationValue(nested, mutationContext{}); err != nil {
		return fmt.Errorf("Argo JSON patch targets a non-admitted field")
	}
	return nil
}

func validateClosedSourceObject(value any, path string) error {
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
		case "helm":
			if err := validateClosedNestedSourceObject(item, path+"."+key, helmSourceKeys); err != nil {
				return err
			}
		case "kustomize":
			if err := validateClosedNestedSourceObject(item, path+"."+key, kustomizeSourceKeys); err != nil {
				return err
			}
		case "directory":
			if err := validateClosedNestedSourceObject(item, path+"."+key, directorySourceKeys); err != nil {
				return err
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
