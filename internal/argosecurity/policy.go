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
	"unicode"
	"unicode/utf8"

	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/redaction"
)

const Marker = redaction.Marker

// MaxArgoResponseBodyBytes is the common allocation and inspection ceiling
// for typed and proxied Argo JSON responses. Readers must consume at most one
// byte beyond this limit before rejecting the response.
const MaxArgoResponseBodyBytes = 16 << 20

const (
	maxDocumentDepth       = 24
	maxGeneratorDepth      = 8
	maxStructuredStringLen = 1 << 20
	maxGeneratorEntries    = 64
	maxGeneratorScalarLen  = 512
	maxAssignmentLHSBytes  = 256
	maxAssignmentSpace     = 32
	maxAssignmentPrefix    = 32
	maxSanitizedStringLen  = MaxArgoResponseBodyBytes
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
			if containsCanonicalSensitiveMarker(value) {
				return Marker
			}
			return sanitizeString(value)
		}
		decoded, err := decodeJSONDocument([]byte(trimmed))
		if err != nil {
			if containsCanonicalSensitiveMarker(value) {
				return Marker
			}
			return sanitizeString(value)
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
	return canonicalSensitiveKey(key)
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
	value, _ = sanitizeCanonicalAssignments(value)
	return redaction.String(value)
}

func canonicalSensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	return canonicalSensitiveNormalized([]byte(normalized))
}

func canonicalSensitiveNormalized(normalized []byte) bool {
	if len(normalized) == 0 || referenceNormalizedKey(normalized) {
		return false
	}
	if bytes.Equal(normalized, []byte("sig")) {
		return true
	}
	return canonicalMarkerBytes(normalized)
}

func referenceNormalizedKey(normalized []byte) bool {
	return bytes.Equal(normalized, []byte("secretname")) ||
		bytes.Equal(normalized, []byte("existingsecret")) ||
		bytes.Equal(normalized, []byte("secretref")) ||
		bytes.Equal(normalized, []byte("secretkeyref")) ||
		bytes.HasSuffix(normalized, []byte("secretname")) ||
		bytes.HasSuffix(normalized, []byte("secretref"))
}

type canonicalAssignmentMatch struct {
	valueStart int
	valueEnd   int
	next       int
}

// findCanonicalAssignment scans each input byte at most once from start. Its
// LHS normalization is exactly normalizeKey's ASCII alphanumeric/lowercase
// domain: every other ASCII or Unicode rune is an admitted separator, while
// bounded whitespace and ':'/'=' delimit the assignment grammar. Fixed local
// storage prevents dense safe assignments from allocating per candidate.
func findCanonicalAssignment(value string, start int) (canonicalAssignmentMatch, bool) {
	var normalized [maxAssignmentLHSBytes + maxAssignmentPrefix]byte
	normalizedLen := 0
	semanticStart := 0
	lhsBytes := 0
	lhsValid := true
	valueBoundary := -1

	reset := func() {
		normalizedLen = 0
		semanticStart = 0
		lhsBytes = 0
		lhsValid = true
	}
	for cursor := start; cursor < len(value); {
		if valueBoundary >= 0 && cursor >= valueBoundary {
			// Retain only a bounded normalized tail from the preceding value.
			// It detects a credential family split across the value/LHS
			// boundary, while semanticStart identifies the exact new LHS for
			// the narrow metadata allowlist.
			if normalizedLen > maxAssignmentPrefix {
				copy(normalized[:], normalized[normalizedLen-maxAssignmentPrefix:normalizedLen])
				normalizedLen = maxAssignmentPrefix
			}
			semanticStart = normalizedLen
			lhsBytes = 0
			lhsValid = true
			valueBoundary = -1
		}
		r, size := utf8.DecodeRuneInString(value[cursor:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if isNormalizedKeyRune(r) {
			lhsBytes += size
			if lhsBytes > maxAssignmentLHSBytes || normalizedLen >= len(normalized) {
				lhsValid = false
			} else {
				if r >= 'A' && r <= 'Z' {
					r += 'a' - 'A'
				}
				normalized[normalizedLen] = byte(r)
				normalizedLen++
			}
			cursor += size
			continue
		}
		if unicode.IsSpace(r) {
			spaceStart := cursor
			lineBoundary := false
			for cursor < len(value) {
				r, size = utf8.DecodeRuneInString(value[cursor:])
				if !unicode.IsSpace(r) {
					break
				}
				lineBoundary = lineBoundary || r == '\r' || r == '\n'
				cursor += size
			}
			spaceBytes := cursor - spaceStart
			if normalizedLen > 0 {
				lhsBytes += spaceBytes
				if lineBoundary || spaceBytes > maxAssignmentSpace || lhsBytes > maxAssignmentLHSBytes {
					// A line or an excessive whitespace run is a safe prose
					// boundary. Ordinary bounded spaces remain part of the LHS
					// normalization exactly as normalizeKey specifies.
					reset()
				}
			}
			continue
		}
		if r != ':' && r != '=' {
			if normalizedLen > 0 {
				lhsBytes += size
				if lhsBytes > maxAssignmentLHSBytes {
					lhsValid = false
				}
			}
			cursor += size
			continue
		}

		delimiterEnd := cursor + size
		valueStart, valueEnd, next, hasValue := assignmentValueRange(value, delimiterEnd)
		if lhsValid && normalizedLen > 0 && hasValue && canonicalSensitiveAssignmentLHS(normalized[:normalizedLen], normalized[semanticStart:normalizedLen]) {
			return canonicalAssignmentMatch{valueStart: valueStart, valueEnd: valueEnd, next: next}, true
		}
		if hasValue {
			valueBoundary = next
		}
		reset()
		cursor = delimiterEnd
		for cursor < len(value) && cursor-delimiterEnd <= maxAssignmentSpace {
			r, size = utf8.DecodeRuneInString(value[cursor:])
			if !unicode.IsSpace(r) {
				break
			}
			cursor += size
		}
	}
	return canonicalAssignmentMatch{}, false
}

func canonicalSensitiveAssignmentLHS(normalized, semanticLHS []byte) bool {
	if !canonicalMarkerBytes(normalized) && !bytes.Contains(normalized, []byte("sig")) {
		return false
	}
	// Containment fails closed. Only a complete normalized LHS with exact,
	// non-value metadata semantics is exempted; arbitrary continuations and
	// free-text secret reference labels are never accepted here.
	if !safeAssignmentMetadataLHS(semanticLHS) {
		return true
	}
	prefix := normalized[:len(normalized)-len(semanticLHS)]
	return canonicalMarkerBytes(prefix) || bytes.Contains(prefix, []byte("sig"))
}

func safeAssignmentMetadataLHS(normalized []byte) bool {
	switch string(normalized) {
	case "apikeyowner", "privatekeycount", "passwordcount", "accesstokenstatus", "awsaccesskeyidrotation":
		return true
	default:
		return false
	}
}

func isNormalizedKeyRune(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func assignmentValueRange(value string, start int) (valueStart, valueEnd, next int, ok bool) {
	cursor := start
	for cursor < len(value) && cursor-start <= maxAssignmentSpace {
		r, size := utf8.DecodeRuneInString(value[cursor:])
		if !unicode.IsSpace(r) {
			break
		}
		cursor += size
	}
	if cursor-start > maxAssignmentSpace || cursor >= len(value) {
		return 0, 0, cursor, false
	}
	valueStart = cursor
	quote := value[cursor]
	if quote == '\'' || quote == '"' {
		cursor++
		for cursor < len(value) {
			if value[cursor] == '\\' && cursor+1 < len(value) {
				cursor += 2
				continue
			}
			if value[cursor] == quote {
				cursor++
				return valueStart, cursor, cursor, true
			}
			if value[cursor] == '\r' || value[cursor] == '\n' {
				break
			}
			_, size := utf8.DecodeRuneInString(value[cursor:])
			cursor += size
		}
		return valueStart, cursor, cursor, cursor > valueStart+1
	}
	for cursor < len(value) {
		r, size := utf8.DecodeRuneInString(value[cursor:])
		if unicode.IsSpace(r) || strings.ContainsRune(",;}]", r) {
			break
		}
		cursor += size
	}
	return valueStart, cursor, cursor, cursor > valueStart
}

func sanitizeCanonicalAssignments(value string) (string, bool) {
	match, found := findCanonicalAssignment(value, 0)
	if !found {
		return value, false
	}
	var out strings.Builder
	out.Grow(min(len(value), maxSanitizedStringLen))
	last := 0
	for found {
		if out.Len()+match.valueStart-last+len(Marker) > maxSanitizedStringLen {
			return Marker, true
		}
		out.WriteString(value[last:match.valueStart])
		out.WriteString(Marker)
		last = match.valueEnd
		match, found = findCanonicalAssignment(value, match.next)
	}
	if out.Len()+len(value)-last > maxSanitizedStringLen {
		return Marker, true
	}
	out.WriteString(value[last:])
	return out.String(), true
}

func canonicalCredentialDetected(value string) bool {
	if _, found := findCanonicalAssignment(value, 0); found {
		return true
	}
	if redaction.String(value) != value || embeddedCredentialURL(value) {
		return true
	}
	return false
}

func containsCanonicalSensitiveMarker(value string) bool {
	if canonicalMarkerString(value) {
		return true
	}
	return canonicalCredentialDetected(value)
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

// SanitizeDurableReason sanitizes operator-provided text before it can enter
// operation payloads, retry state, audit events or timelines. Complete JSON
// objects/arrays receive recursive key-aware sanitation; prose that merely
// begins with a bracket or quote remains prose.
func SanitizeDurableReason(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > maxStructuredStringLen {
		if looksLikeJSONWrapper(trimmed) && containsCanonicalSensitiveMarker(trimmed) {
			return Marker, nil
		}
		return "", fmt.Errorf("reason exceeds sanitation limit")
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if decoded, err := decodeJSONDocument([]byte(trimmed)); err == nil {
			safe := sanitizeArgoValue(decoded, sanitizeContext{})
			raw, err := json.Marshal(safe)
			if err != nil {
				return "", fmt.Errorf("sanitize structured reason: %w", err)
			}
			return string(raw), nil
		}
		if containsCanonicalSensitiveMarker(trimmed) {
			return Marker, nil
		}
	}
	if strings.Contains(trimmed, "\n") || canonicalCredentialDetected(trimmed) {
		var decoded any
		if err := yaml.Unmarshal([]byte(trimmed), &decoded); err == nil {
			switch decoded.(type) {
			case map[string]any, []any:
				raw, err := json.Marshal(sanitizeArgoValue(decoded, sanitizeContext{}))
				if err != nil {
					return "", fmt.Errorf("sanitize structured reason: %w", err)
				}
				return string(raw), nil
			}
		}
	}
	safe := sanitizeString(trimmed)
	return safe, nil
}

// ValidateMutation rejects Application source forms that can persist secret
// material or bypass the closed typed source model. It accepts safe ordinary
// action bodies (for example sync revision/prune) and reference-only sources.
func ValidateMutation(value any) error {
	return validateMutation(value, mutationContext{})
}

// ValidateApplicationSetMutation applies the closed generator union in
// addition to the shared Argo mutation policy.
func ValidateApplicationSetMutation(value any) error {
	return validateMutation(value, mutationContext{applicationSet: true})
}

func validateMutation(value any, ctx mutationContext) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Argo mutation for validation: %w", err)
	}
	decoded, err := decodeJSONDocument(raw)
	if err != nil {
		return fmt.Errorf("decode Argo mutation for validation: %w", err)
	}
	return validateMutationValue(decoded, ctx)
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
	return validateMutationJSON(raw, mutationContext{
		allowProjectDestinationWildcard: isProjectRequestPath(requestPath),
		applicationSet:                  isApplicationSetRequestPath(requestPath),
	})
}

func isApplicationSetRequestPath(requestPath string) bool {
	clean := strings.TrimSuffix(strings.Split(requestPath, "?")[0], "/")
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "api" && parts[i+1] == "v1" && parts[i+2] == "applicationsets" {
			return true
		}
	}
	return false
}

func validateMutationJSON(raw []byte, ctx mutationContext) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return fmt.Errorf("invalid Argo JSON mutation: %w", err)
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

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var inspectValue func() error
	inspectValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("JSON object key is not a string")
				}
				folded := strings.ToLower(key)
				if _, duplicate := seen[folded]; duplicate {
					return fmt.Errorf("duplicate or case-colliding JSON key %q", key)
				}
				seen[folded] = struct{}{}
				if err := inspectValue(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := inspectValue(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	return inspectValue()
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
	applicationSet                  bool
	patchSourceRefs                 map[string]struct{}
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
			if normalized == "generators" && ctx.applicationSet {
				if err := validateApplicationSetGenerators(item, path, ctx.depth+1); err != nil {
					return err
				}
			}
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
			if err := validateMutationValue(item, mutationContext{inSource: childInSource, path: path, depth: ctx.depth + 1, allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard, applicationSet: ctx.applicationSet, patchSourceRefs: ctx.patchSourceRefs}); err != nil {
				return err
			}
		}
	case []any:
		if refs := collectJSONPatchSourceRefs(typed); len(refs) > 0 {
			ctx.patchSourceRefs = refs
		}
		for i, item := range typed {
			if err := validateMutationValue(item, mutationContext{inSource: ctx.inSource, path: fmt.Sprintf("%s[%d]", ctx.path, i), depth: ctx.depth + 1, allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard, applicationSet: ctx.applicationSet, patchSourceRefs: ctx.patchSourceRefs}); err != nil {
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
			return validateMutationValue(decoded, mutationContext{depth: depth, allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard, applicationSet: ctx.applicationSet, patchSourceRefs: ctx.patchSourceRefs})
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
	if op != strings.ToLower(strings.TrimSpace(op)) {
		return fmt.Errorf("Argo JSON patch operation must use canonical casing")
	}
	switch op {
	case "add", "replace", "remove", "test", "copy", "move":
	default:
		return fmt.Errorf("Argo JSON patch contains an unsupported operation")
	}
	var value any
	if op != "remove" {
		value = object["value"]
	}
	if err := validateJSONPointerMutation(path, value, op, false, ctx); err != nil {
		return err
	}
	if from, ok := object["from"].(string); ok && strings.TrimSpace(from) != "" {
		if op != "move" && op != "copy" {
			return fmt.Errorf("Argo JSON patch from is valid only for move/copy")
		}
		if err := validateJSONPointerMutation(from, nil, op, true, ctx); err != nil {
			return err
		}
	} else if op == "move" || op == "copy" {
		return fmt.Errorf("Argo JSON patch move/copy requires from")
	}
	return nil
}

func validateJSONPointerMutation(pointer string, value any, operation string, from bool, ctx mutationContext) error {
	if pointer == "" {
		return validateMutationValue(value, ctx)
	}
	segments, err := decodeJSONPointer(pointer)
	if err != nil {
		return err
	}
	if sourcePointer(segments) {
		if err := validateSourceJSONPointer(segments, value, operation, from, ctx.patchSourceRefs); err != nil {
			return fmt.Errorf("Argo JSON patch targets a non-admitted source field: %w", err)
		}
		return nil
	}
	nested := value
	for i := len(segments) - 1; i >= 0; i-- {
		nested = map[string]any{segments[i]: nested}
	}
	if err := validateMutationValue(nested, mutationContext{allowProjectDestinationWildcard: ctx.allowProjectDestinationWildcard, applicationSet: ctx.applicationSet, patchSourceRefs: ctx.patchSourceRefs}); err != nil {
		return fmt.Errorf("Argo JSON patch targets a non-admitted field")
	}
	return nil
}

func decodeJSONPointer(pointer string) ([]string, error) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("Argo JSON patch contains an invalid path")
	}
	raw := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	segments := make([]string, len(raw))
	for i, segment := range raw {
		var out strings.Builder
		for j := 0; j < len(segment); j++ {
			if segment[j] != '~' {
				out.WriteByte(segment[j])
				continue
			}
			if j+1 >= len(segment) || (segment[j+1] != '0' && segment[j+1] != '1') {
				return nil, fmt.Errorf("Argo JSON patch contains an invalid escape")
			}
			j++
			if segment[j] == '0' {
				out.WriteByte('~')
			} else {
				out.WriteByte('/')
			}
		}
		segments[i] = out.String()
		if segments[i] == ".." {
			return nil, fmt.Errorf("Argo JSON patch traversal segments are not admitted")
		}
	}
	return segments, nil
}

func sourcePointer(segments []string) bool {
	return len(segments) >= 2 && segments[0] == "spec" && (segments[1] == "source" || segments[1] == "sources")
}

func validArrayPointer(value string, appendAllowed bool) bool {
	if appendAllowed && value == "-" {
		return true
	}
	if value == "" {
		return false
	}
	if len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateSourceJSONPointer(segments []string, value any, operation string, from bool, refs map[string]struct{}) error {
	removing := operation == "remove"
	copying := operation == "copy" || operation == "move"
	if from {
		if len(segments) == 4 && segments[1] == "sources" && validArrayPointer(segments[2], false) && segments[3] == "targetRevision" {
			return nil
		}
		if len(segments) == 3 && segments[1] == "source" && segments[2] == "targetRevision" {
			return nil
		}
		return fmt.Errorf("move/copy from source fields is admitted only for targetRevision")
	}
	if removing {
		return nil
	}
	if segments[1] == "source" {
		switch {
		case len(segments) == 2:
			return validateClosedSourceObject(value, "spec.source", false, nil)
		case len(segments) == 3 && segments[2] == "targetRevision":
			return validatePatchScalar(value, copying)
		case len(segments) == 3 && segments[2] == "repoURL":
			text, ok := value.(string)
			if !ok || ValidateRepositoryURL(text) != nil {
				return fmt.Errorf("repoURL is unsafe")
			}
			return nil
		default:
			return fmt.Errorf("single-source patch path is not admitted")
		}
	}
	if len(segments) == 2 {
		return validateMutationValue(map[string]any{"sources": value}, mutationContext{})
	}
	if !validArrayPointer(segments[2], operation == "add") {
		return fmt.Errorf("sources index is invalid")
	}
	switch {
	case len(segments) == 3:
		if copying {
			return fmt.Errorf("copy/move of an opaque source is not admitted")
		}
		return validateClosedSourceObject(value, "spec.sources[patch]", true, refs)
	case len(segments) == 4 && segments[3] == "targetRevision":
		return validatePatchScalar(value, copying)
	case len(segments) == 4 && segments[3] == "repoURL":
		text, ok := value.(string)
		if !ok || ValidateRepositoryURL(text) != nil {
			return fmt.Errorf("repoURL is unsafe")
		}
		return nil
	case len(segments) == 4 && segments[3] == "ref":
		text, ok := value.(string)
		if !ok || !safeSourceRef.MatchString(text) {
			return fmt.Errorf("source ref is invalid")
		}
		return nil
	case len(segments) == 5 && segments[3] == "helm" && segments[4] == "valueFiles":
		files, ok := value.([]any)
		if !ok || len(files) > maxGeneratorEntries {
			return fmt.Errorf("valueFiles must be a bounded array")
		}
		for _, raw := range files {
			file, ok := raw.(string)
			if !ok || validateHelmValueFile(file, refs) != nil {
				return fmt.Errorf("valueFiles contains an unsafe reference")
			}
		}
		return nil
	case len(segments) == 6 && segments[3] == "helm" && segments[4] == "valueFiles" && validArrayPointer(segments[5], operation == "add"):
		file, ok := value.(string)
		if !ok {
			return fmt.Errorf("valueFiles item must be a string")
		}
		return validateHelmValueFile(file, refs)
	default:
		return fmt.Errorf("multi-source patch path is not admitted")
	}
}

func validatePatchScalar(value any, copying bool) error {
	if copying && value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok || len(text) > maxGeneratorScalarLen || strings.ContainsAny(text, "\r\n") || canonicalCredentialDetected(text) {
		return fmt.Errorf("patch scalar is unsafe")
	}
	return nil
}

func collectJSONPatchSourceRefs(items []any) map[string]struct{} {
	refs := map[string]struct{}{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok || object["op"] != "remove" {
			continue
		}
		pointer, _ := object["path"].(string)
		segments, err := decodeJSONPointer(pointer)
		if err == nil && sourcePointer(segments) && segments[1] == "sources" && len(segments) <= 3 {
			// Without reading current upstream state we cannot prove that a
			// removed source is not the sibling ref used elsewhere in the patch.
			return refs
		}
	}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		op, _ := object["op"].(string)
		if op != "add" && op != "replace" {
			continue
		}
		pointer, _ := object["path"].(string)
		segments, err := decodeJSONPointer(pointer)
		if err != nil || !sourcePointer(segments) || segments[1] != "sources" {
			continue
		}
		value := object["value"]
		if len(segments) == 2 {
			if sources, ok := value.([]any); ok {
				if found, err := collectSourceRefs(sources); err == nil {
					for ref := range found {
						refs[ref] = struct{}{}
					}
				}
			}
		}
		if len(segments) == 3 {
			if source, ok := value.(map[string]any); ok {
				if ref, ok := source["ref"].(string); ok && safeSourceRef.MatchString(ref) {
					refs[ref] = struct{}{}
				}
			}
		}
		if len(segments) == 4 && segments[3] == "ref" {
			if ref, ok := value.(string); ok && safeSourceRef.MatchString(ref) {
				refs[ref] = struct{}{}
			}
		}
	}
	return refs
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
		canonicalCredentialDetected(value) {
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
	safeTemplateScalar          = regexp.MustCompile(`^\{\{[a-zA-Z0-9_.-]{1,128}\}\}$`)
	safeNotificationRecipient   = regexp.MustCompile(`^[a-zA-Z0-9_.@+-]{1,256}$`)
	safeSourceRef               = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,62}$`)
	safeGeneratorKey            = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
	safeSCPRepository           = regexp.MustCompile(`^git@([A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?):([A-Za-z0-9._/-]{1,512})$`)
	embeddedHTTPURL             = regexp.MustCompile(`(?i)https?://[^\s<>'"]+`)
	canonicalSensitiveFragments = []string{
		"password", "passwd", "secret", "clientsecret", "clientcertkey", "kubeconfig",
		"apikey", "privatekey", "token", "credential", "awsaccesskeyid", "googleaccessid",
		"signature", "bearer", "xamzsecuritytoken", "authorization", "cookie", "clientkey",
		"cacertificate", "certificateauthoritydata",
	}
	canonicalMarkerMachine = buildCanonicalMarkerMachine(canonicalSensitiveFragments)
)

type canonicalMarkerNode struct {
	next     [36]int
	failure  int
	terminal bool
}

// buildCanonicalMarkerMachine constructs an Aho-Corasick machine once. The
// hot path then recognizes every canonical family with one transition per
// normalized byte instead of performing one complete string scan per family.
func buildCanonicalMarkerMachine(patterns []string) []canonicalMarkerNode {
	nodes := []canonicalMarkerNode{{}}
	for _, pattern := range patterns {
		state := 0
		for i := 0; i < len(pattern); i++ {
			index := canonicalMarkerIndex(pattern[i])
			if nodes[state].next[index] == 0 {
				nodes = append(nodes, canonicalMarkerNode{})
				nodes[state].next[index] = len(nodes) - 1
			}
			state = nodes[state].next[index]
		}
		nodes[state].terminal = true
	}
	queue := make([]int, 0, len(nodes))
	for index, child := range nodes[0].next {
		if child != 0 {
			queue = append(queue, child)
			continue
		}
		nodes[0].next[index] = 0
	}
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		for index, child := range nodes[state].next {
			if child != 0 {
				nodes[child].failure = nodes[nodes[state].failure].next[index]
				nodes[child].terminal = nodes[child].terminal || nodes[nodes[child].failure].terminal
				queue = append(queue, child)
				continue
			}
			nodes[state].next[index] = nodes[nodes[state].failure].next[index]
		}
	}
	return nodes
}

func canonicalMarkerIndex(value byte) int {
	if value >= '0' && value <= '9' {
		return int(value-'0') + 26
	}
	return int(value - 'a')
}

func canonicalMarkerBytes(value []byte) bool {
	state := 0
	for _, item := range value {
		if item >= 'A' && item <= 'Z' {
			item += 'a' - 'A'
		}
		if (item < 'a' || item > 'z') && (item < '0' || item > '9') {
			continue
		}
		state = canonicalMarkerMachine[state].next[canonicalMarkerIndex(item)]
		if canonicalMarkerMachine[state].terminal {
			return true
		}
	}
	return false
}

func canonicalMarkerString(value string) bool {
	state := 0
	for cursor := 0; cursor < len(value); {
		r, size := utf8.DecodeRuneInString(value[cursor:])
		cursor += size
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			continue
		}
		state = canonicalMarkerMachine[state].next[canonicalMarkerIndex(byte(r))]
		if canonicalMarkerMachine[state].terminal {
			return true
		}
	}
	return false
}

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

func validateApplicationSetGenerators(value any, generatorPath string, depth int) error {
	if depth > maxGeneratorDepth {
		return fmt.Errorf("ApplicationSet generators exceed maximum depth")
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 || len(items) > maxGeneratorEntries {
		return fmt.Errorf("%s must be a bounded non-empty generator array", generatorPath)
	}
	for i, item := range items {
		generator, ok := item.(map[string]any)
		if !ok || len(generator) != 1 {
			return fmt.Errorf("%s[%d] must select exactly one generator branch", generatorPath, i)
		}
		for branch, config := range generator {
			branchPath := fmt.Sprintf("%s[%d].%s", generatorPath, i, branch)
			switch branch {
			case "list":
				object, err := closedObject(config, branchPath, "elements")
				if err != nil {
					return err
				}
				if err := validateGeneratorValues(object["elements"], branchPath+".elements"); err != nil {
					return err
				}
			case "clusters":
				object, err := closedObject(config, branchPath, "selector", "values")
				if err != nil {
					return err
				}
				if rawValues, exists := object["values"]; exists && !emptyJSONValue(rawValues) {
					if err := validateGeneratorValues(rawValues, branchPath+".values"); err != nil {
						return err
					}
				}
				if selector, exists := object["selector"]; exists && !emptyJSONValue(selector) {
					if err := validateGeneratorSelector(selector, branchPath+".selector"); err != nil {
						return err
					}
				}
			case "git":
				object, err := closedObject(config, branchPath, "repoURL", "revision", "files", "directories", "values")
				if err != nil {
					return err
				}
				repo, ok := object["repoURL"].(string)
				if !ok || ValidateRepositoryURL(repo) != nil {
					return fmt.Errorf("%s.repoURL must be a safe repository URL", branchPath)
				}
				if revision, exists := object["revision"]; exists && !emptyJSONValue(revision) {
					if err := validateGeneratorScalar(revision, branchPath+".revision"); err != nil {
						return err
					}
				}
				if rawValues, exists := object["values"]; exists && !emptyJSONValue(rawValues) {
					if err := validateGeneratorValues(rawValues, branchPath+".values"); err != nil {
						return err
					}
				}
				if err := validateGeneratorObjectArray(object["files"], branchPath+".files", "path"); err != nil {
					return err
				}
				if err := validateGeneratorObjectArray(object["directories"], branchPath+".directories", "path", "exclude"); err != nil {
					return err
				}
			case "matrix":
				object, err := closedObject(config, branchPath, "generators")
				if err != nil {
					return err
				}
				children, ok := object["generators"].([]any)
				if !ok || len(children) != 2 {
					return fmt.Errorf("%s.generators must contain exactly two children", branchPath)
				}
				if err := validateApplicationSetGenerators(children, branchPath+".generators", depth+1); err != nil {
					return err
				}
			case "merge":
				object, err := closedObject(config, branchPath, "generators", "mergeKeys")
				if err != nil {
					return err
				}
				children, ok := object["generators"].([]any)
				if !ok || len(children) < 2 || len(children) > maxGeneratorEntries {
					return fmt.Errorf("%s.generators must contain at least two children", branchPath)
				}
				if err := validateApplicationSetGenerators(children, branchPath+".generators", depth+1); err != nil {
					return err
				}
				keys, ok := object["mergeKeys"].([]any)
				if !ok || len(keys) == 0 || len(keys) > 16 {
					return fmt.Errorf("%s.mergeKeys must be a bounded non-empty array", branchPath)
				}
				seen := map[string]struct{}{}
				for _, rawKey := range keys {
					key, ok := rawKey.(string)
					if !ok || !safeGeneratorKey.MatchString(key) {
						return fmt.Errorf("%s.mergeKeys contains an invalid key", branchPath)
					}
					if _, duplicate := seen[key]; duplicate {
						return fmt.Errorf("%s.mergeKeys contains a duplicate key", branchPath)
					}
					seen[key] = struct{}{}
				}
			default:
				return fmt.Errorf("%s[%d] contains unknown or incorrectly-cased generator branch %q", generatorPath, i, branch)
			}
		}
	}
	return nil
}

func closedObject(value any, objectPath string, allowed ...string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", objectPath)
	}
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range object {
		if _, ok := set[key]; !ok {
			return nil, fmt.Errorf("%s contains unknown or incorrectly-cased field %q", objectPath, key)
		}
	}
	return object, nil
}

func validateGeneratorSelector(value any, selectorPath string) error {
	object, err := closedObject(value, selectorPath, "matchLabels", "matchExpressions")
	if err != nil {
		return err
	}
	if labels, exists := object["matchLabels"]; exists && !emptyJSONValue(labels) {
		if err := validateGeneratorValues(labels, selectorPath+".matchLabels"); err != nil {
			return err
		}
	}
	if expressions, exists := object["matchExpressions"]; exists && !emptyJSONValue(expressions) {
		items, ok := expressions.([]any)
		if !ok || len(items) > maxGeneratorEntries {
			return fmt.Errorf("%s.matchExpressions has invalid shape", selectorPath)
		}
		for i, item := range items {
			itemPath := fmt.Sprintf("%s.matchExpressions[%d]", selectorPath, i)
			expression, err := closedObject(item, itemPath, "key", "operator", "values")
			if err != nil {
				return err
			}
			for _, field := range []string{"key", "operator"} {
				if err := validateGeneratorScalar(expression[field], itemPath+"."+field); err != nil {
					return err
				}
			}
			if values, exists := expression["values"]; exists {
				items, ok := values.([]any)
				if !ok || len(items) > maxGeneratorEntries {
					return fmt.Errorf("%s.values has invalid shape", itemPath)
				}
				for j, scalar := range items {
					if err := validateGeneratorScalar(scalar, fmt.Sprintf("%s.values[%d]", itemPath, j)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func validateGeneratorObjectArray(value any, arrayPath string, allowed ...string) error {
	if emptyJSONValue(value) {
		return nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > maxGeneratorEntries {
		return fmt.Errorf("%s has invalid shape", arrayPath)
	}
	for i, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", arrayPath, i)
		object, err := closedObject(item, itemPath, allowed...)
		if err != nil {
			return err
		}
		for key, scalar := range object {
			if err := validateGeneratorScalar(scalar, itemPath+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGeneratorScalar(value any, scalarPath string) error {
	switch scalar := value.(type) {
	case nil, bool, json.Number, float64:
		return nil
	case string:
		if len(scalar) > maxGeneratorScalarLen || strings.ContainsAny(scalar, "\r\n") {
			if len(scalar) > maxGeneratorScalarLen {
				return fmt.Errorf("%s contains an oversized generator scalar", scalarPath)
			}
		}
		return validateGeneratorString(scalar, scalarPath, 0)
	default:
		return fmt.Errorf("%s must be a scalar", scalarPath)
	}
}

func validateGeneratorString(value, scalarPath string, depth int) error {
	if depth > maxGeneratorDepth {
		return fmt.Errorf("%s exceeds structured generator depth", scalarPath)
	}
	trimmed := strings.TrimSpace(value)
	if canonicalCredentialDetected(value) {
		return fmt.Errorf("%s contains credential-shaped generator data", scalarPath)
	}
	if looksLikeJSONWrapper(trimmed) {
		decoded, err := decodeJSONDocument([]byte(trimmed))
		if err == nil {
			return validateGeneratorStructuredValue(decoded, scalarPath, depth+1)
		}
		if containsCanonicalSensitiveMarker(value) {
			return fmt.Errorf("%s contains a sensitive marker in malformed structured data", scalarPath)
		}
		return nil
	}
	if strings.Contains(value, "\n") || strings.Contains(value, ":") {
		var decoded any
		if err := yaml.Unmarshal([]byte(value), &decoded); err == nil {
			switch decoded.(type) {
			case map[string]any, []any:
				return validateGeneratorStructuredValue(decoded, scalarPath, depth+1)
			}
		}
		if containsCanonicalSensitiveMarker(value) {
			return fmt.Errorf("%s contains a sensitive marker in malformed structured data", scalarPath)
		}
	}
	return nil
}

func validateGeneratorStructuredValue(value any, scalarPath string, depth int) error {
	if depth > maxGeneratorDepth {
		return fmt.Errorf("%s exceeds structured generator depth", scalarPath)
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > maxGeneratorEntries {
			return fmt.Errorf("%s exceeds structured generator entry limit", scalarPath)
		}
		for key, item := range typed {
			if canonicalSensitiveKey(key) {
				return fmt.Errorf("%s contains sensitive structured key %q", scalarPath, key)
			}
			if err := validateGeneratorStructuredValue(item, scalarPath+"."+key, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > maxGeneratorEntries {
			return fmt.Errorf("%s exceeds structured generator entry limit", scalarPath)
		}
		for i, item := range typed {
			if err := validateGeneratorStructuredValue(item, fmt.Sprintf("%s[%d]", scalarPath, i), depth+1); err != nil {
				return err
			}
		}
	case string:
		return validateGeneratorString(typed, scalarPath, depth+1)
	}
	return nil
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
			if err := validateGeneratorScalar(item, path+"."+key); err != nil {
				return err
			}
			if scalar, ok := item.(string); ok && strings.Contains(scalar, "://") && ValidateCredentialFreeURL(scalar) != nil {
				return fmt.Errorf("%s contains a credential-bearing generator URL", path)
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
