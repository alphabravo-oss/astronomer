// Package dexconfig owns the closed, shared validation contract for every Dex
// configuration accepted by the API or executed by the bundled runtime.
package dexconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type NestedRequirement struct {
	Parent string
	Keys   []string
}

type ConnectorSpec struct {
	Type, DisplayHint          string
	Required, Optional, Secret []string
	Nested                     []NestedRequirement
}

var connectorRegistry = map[string]ConnectorSpec{
	"oidc":      {Type: "oidc", DisplayHint: "Generic OpenID Connect (Keycloak, Authentik, Auth0, ...)", Required: []string{"issuer", "clientID", "clientSecret"}, Optional: []string{"redirectURI", "scopes", "userNameKey", "insecureSkipVerify"}, Secret: []string{"clientSecret"}},
	"okta":      {Type: "okta", DisplayHint: "Okta (treated as OIDC with Okta defaults)", Required: []string{"issuer", "clientID", "clientSecret"}, Optional: []string{"redirectURI", "scopes", "groups"}, Secret: []string{"clientSecret"}},
	"microsoft": {Type: "microsoft", DisplayHint: "Azure AD / Microsoft Entra ID", Required: []string{"tenant", "clientID", "clientSecret"}, Optional: []string{"redirectURI", "groups", "onlySecurityGroups", "useGroupsAsWhitelist"}, Secret: []string{"clientSecret"}},
	"github":    {Type: "github", DisplayHint: "GitHub OAuth (orgs / teams)", Required: []string{"clientID", "clientSecret"}, Optional: []string{"redirectURI", "orgs", "teams", "loadAllGroups"}, Secret: []string{"clientSecret"}},
	"gitlab":    {Type: "gitlab", DisplayHint: "GitLab (self-hosted or gitlab.com)", Required: []string{"baseURL", "clientID", "clientSecret"}, Optional: []string{"redirectURI", "groups"}, Secret: []string{"clientSecret"}},
	"bitbucket": {Type: "bitbucket", DisplayHint: "Bitbucket Cloud", Required: []string{"clientID", "clientSecret"}, Optional: []string{"redirectURI", "teams"}, Secret: []string{"clientSecret"}},
	"google":    {Type: "google", DisplayHint: "Google Workspace", Required: []string{"clientID", "clientSecret"}, Optional: []string{"redirectURI", "scopes", "hostedDomains"}, Secret: []string{"clientSecret"}},
	"saml":      {Type: "saml", DisplayHint: "SAML 2.0 (ADFS, Shibboleth, Okta-SAML, ...)", Required: []string{"ssoURL", "entityIssuer"}, Optional: []string{"ca", "caData", "redirectURI", "usernameAttr", "emailAttr", "groupsAttr", "groupsDelim", "filterGroups", "allowedGroups", "insecureSkipSignatureValidation", "nameIDPolicyFormat"}},
	"ldap":      {Type: "ldap", DisplayHint: "LDAP / Active Directory", Required: []string{"host", "bindDN", "bindPW"}, Optional: []string{"insecureNoSSL", "insecureSkipVerify", "rootCAData", "startTLS", "usernamePrompt"}, Secret: []string{"bindPW"}, Nested: []NestedRequirement{{Parent: "userSearch", Keys: []string{"baseDN", "username", "idAttr", "emailAttr"}}}},
	"oauth":     {Type: "oauth", DisplayHint: "Generic OAuth 2.0", Required: []string{"clientID", "clientSecret", "tokenURL", "authorizationURL", "userInfoURL"}, Optional: []string{"redirectURI", "scopes", "userIDKey"}, Secret: []string{"clientSecret"}},
}

func Registry() map[string]ConnectorSpec {
	out := make(map[string]ConnectorSpec, len(connectorRegistry))
	for key, spec := range connectorRegistry {
		out[key] = spec
	}
	return out
}

func ConnectorTypes() []string {
	out := make([]string, 0, len(connectorRegistry))
	for key := range connectorRegistry {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// ValidateURL applies one canonical URL policy to the API and runtime
// preflight. Query strings are rejected for all Dex-managed URLs: provider
// authorization parameters belong in typed connector fields, not opaque URLs.
func ValidateURL(raw string, allowLoopbackHTTP bool) error {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t") {
		return fmt.Errorf("must be a canonical URL without whitespace")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("must be an absolute URL")
	}
	if u.User != nil {
		return fmt.Errorf("must not contain credentials")
	}
	if separator := strings.Index(raw, "://"); separator <= 0 || raw[:separator] != strings.ToLower(raw[:separator]) {
		return fmt.Errorf("scheme must be lowercase")
	}
	if u.Fragment != "" {
		return fmt.Errorf("must not contain a fragment")
	}
	if u.ForceQuery || u.RawQuery != "" {
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return fmt.Errorf("query string has a non-canonical encoding")
		}
		for name, entries := range values {
			decodedName, err := url.QueryUnescape(name)
			if err != nil {
				return fmt.Errorf("query name has invalid encoding")
			}
			if len(entries) != 1 {
				return fmt.Errorf("query string contains duplicate names")
			}
			decodedValue, err := url.QueryUnescape(entries[0])
			if err != nil {
				return fmt.Errorf("query value has invalid encoding")
			}
			if credentialShape(decodedName) || credentialShape(decodedValue) {
				return fmt.Errorf("query string contains a credential-shaped name or value")
			}
		}
		return fmt.Errorf("must not contain a query string")
	}
	if u.RawPath != "" || strings.Contains(u.EscapedPath(), "%") {
		return fmt.Errorf("must not contain encoded path segments")
	}
	if u.Scheme != strings.ToLower(u.Scheme) || u.Host != strings.ToLower(u.Host) {
		return fmt.Errorf("scheme and host must be lowercase")
	}
	if u.Scheme != "https" && !(allowLoopbackHTTP && u.Scheme == "http" && isLoopback(u.Hostname())) {
		return fmt.Errorf("must use https")
	}
	if strings.HasSuffix(u.Hostname(), ".") {
		return fmt.Errorf("host must not have a trailing dot")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port must be between 1 and 65535")
		}
		if (u.Scheme == "https" && n == 443) || (u.Scheme == "http" && n == 80) {
			return fmt.Errorf("default ports must be omitted")
		}
	}
	if cleaned := path.Clean(u.Path); u.Path != "" && u.Path != "/" && cleaned != u.Path && cleaned+"/" != u.Path {
		return fmt.Errorf("path must be canonical")
	}
	return nil
}

func credentialShape(value string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(value))
	for _, fragment := range []string{"secret", "password", "passwd", "token", "apikey", "privatekey", "credential", "bindpw"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }

func ValidateListener(raw string) error {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || strings.TrimSpace(host) == "" {
		return fmt.Errorf("must be an explicit host:port address")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

// CanonicalConnectorType rejects aliases and casing variants before callers
// perform any connector-specific lookup or branch. The stored/runtime contract
// is deliberately a single lowercase spelling for every connector type.
func CanonicalConnectorType(connectorType string) (string, error) {
	canonical := strings.ToLower(strings.TrimSpace(connectorType))
	if canonical == "" || connectorType != canonical {
		return "", fmt.Errorf("connector type must use its canonical lowercase spelling")
	}
	if _, ok := connectorRegistry[canonical]; !ok {
		return "", fmt.Errorf("unknown connector type")
	}
	return canonical, nil
}

func ValidateConnector(connectorType string, raw map[string]any) error {
	canonical, err := CanonicalConnectorType(connectorType)
	if err != nil {
		return err
	}
	spec, ok := connectorRegistry[canonical]
	if !ok {
		return fmt.Errorf("unknown connector type")
	}
	connectorType = canonical
	missing := []string{}
	for _, key := range spec.Required {
		if isEmpty(raw[key]) {
			missing = append(missing, key)
		}
	}
	for _, nested := range spec.Nested {
		object, ok := raw[nested.Parent].(map[string]any)
		if !ok {
			missing = append(missing, nested.Parent)
			continue
		}
		for _, key := range nested.Keys {
			if isEmpty(object[key]) {
				missing = append(missing, nested.Parent+"."+key)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	allowed := map[string]bool{}
	for _, key := range append(append(append([]string{}, spec.Required...), spec.Optional...), spec.Secret...) {
		allowed[key] = true
	}
	for _, nested := range spec.Nested {
		allowed[nested.Parent] = true
	}
	if connectorType == "ldap" {
		allowed["groupSearch"] = true
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("connector config contains an unsupported field")
		}
	}
	stringFields := map[string][]string{
		"oidc": {"issuer", "clientID", "clientSecret", "redirectURI", "userNameKey"}, "okta": {"issuer", "clientID", "clientSecret", "redirectURI"},
		"microsoft": {"tenant", "clientID", "clientSecret", "redirectURI"}, "github": {"clientID", "clientSecret", "redirectURI"},
		"gitlab": {"baseURL", "clientID", "clientSecret", "redirectURI"}, "bitbucket": {"clientID", "clientSecret", "redirectURI"},
		"google": {"clientID", "clientSecret", "redirectURI"}, "saml": {"ssoURL", "entityIssuer", "ca", "caData", "redirectURI", "usernameAttr", "emailAttr", "groupsAttr", "groupsDelim", "nameIDPolicyFormat"},
		"ldap": {"host", "bindDN", "bindPW", "rootCAData", "usernamePrompt"}, "oauth": {"clientID", "clientSecret", "tokenURL", "authorizationURL", "userInfoURL", "redirectURI", "userIDKey"},
	}
	boolFields := map[string][]string{"oidc": {"insecureSkipVerify"}, "microsoft": {"onlySecurityGroups", "useGroupsAsWhitelist"}, "github": {"loadAllGroups"}, "saml": {"insecureSkipSignatureValidation"}, "ldap": {"insecureNoSSL", "insecureSkipVerify", "startTLS"}}
	listFields := map[string][]string{"oidc": {"scopes"}, "okta": {"scopes", "groups"}, "microsoft": {"groups"}, "github": {"orgs", "teams"}, "gitlab": {"groups"}, "bitbucket": {"teams"}, "google": {"scopes", "hostedDomains"}, "saml": {"filterGroups", "allowedGroups"}, "oauth": {"scopes"}}
	for _, key := range stringFields[connectorType] {
		if value, exists := raw[key]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("connector field %s must be a string", key)
			}
		}
	}
	for _, key := range boolFields[connectorType] {
		if value, exists := raw[key]; exists {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("connector field %s must be a boolean", key)
			}
		}
	}
	for _, key := range listFields[connectorType] {
		if value, exists := raw[key]; exists {
			if _, err := StringList(value, false); err != nil {
				return fmt.Errorf("connector field %s must be a unique string array", key)
			}
		}
	}
	for _, key := range []string{"issuer", "baseURL", "ssoURL", "tokenURL", "authorizationURL", "userInfoURL", "redirectURI"} {
		if value, ok := raw[key].(string); ok && value != "" {
			if err := ValidateURL(value, key == "redirectURI"); err != nil {
				return fmt.Errorf("connector field %s: %w", key, err)
			}
		}
	}
	if connectorType == "ldap" {
		if err := ValidateListener(raw["host"].(string)); err != nil {
			return fmt.Errorf("connector field host: %w", err)
		}
		for _, parent := range []string{"userSearch", "groupSearch"} {
			if value, exists := raw[parent]; exists {
				if err := validateLDAPSearch(parent, value); err != nil {
					return err
				}
			}
		}
	}
	if connectorType == "saml" {
		if value, _ := raw["nameIDPolicyFormat"].(string); value != "" && !allowedSAMLNameID(value) {
			return fmt.Errorf("connector field nameIDPolicyFormat is unsupported")
		}
	}
	return nil
}

func validateLDAPSearch(parent string, value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("LDAP search field %s must be an object", parent)
	}
	allowed := map[string]bool{"baseDN": true, "filter": true, "username": true, "idAttr": true, "emailAttr": true, "nameAttr": true, "preferredUsernameAttr": true, "groups": true, "userMatchers": true}
	for key, item := range object {
		if !allowed[key] {
			return fmt.Errorf("LDAP search field %s contains an unsupported field", parent)
		}
		if key == "userMatchers" {
			items, ok := item.([]any)
			if !ok || len(items) == 0 {
				return fmt.Errorf("LDAP userMatchers must be a non-empty array")
			}
			for _, rawMatcher := range items {
				matcher, ok := rawMatcher.(map[string]any)
				if !ok || len(matcher) != 2 {
					return fmt.Errorf("LDAP userMatcher must contain userAttr and groupAttr")
				}
				for _, k := range []string{"userAttr", "groupAttr"} {
					v, ok := matcher[k].(string)
					if !ok || strings.TrimSpace(v) == "" {
						return fmt.Errorf("LDAP userMatcher requires non-empty userAttr and groupAttr")
					}
				}
			}
		} else if _, ok := item.(string); !ok {
			return fmt.Errorf("LDAP search field %s.%s must be a string", parent, key)
		}
	}
	return nil
}

func allowedSAMLNameID(value string) bool {
	for _, allowed := range []string{"persistent", "transient", "emailAddress", "unspecified", "urn:oasis:names:tc:SAML:2.0:nameid-format:persistent", "urn:oasis:names:tc:SAML:2.0:nameid-format:transient", "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress", "urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified"} {
		if value == allowed {
			return true
		}
	}
	return false
}

func StringList(value any, allowEmpty bool) ([]string, error) {
	items := []string{}
	switch typed := value.(type) {
	case []string:
		items = append(items, typed...)
	case []any:
		for _, raw := range typed {
			text, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("not a string array")
			}
			items = append(items, text)
		}
	default:
		return nil, fmt.Errorf("not a string array")
	}
	if !allowEmpty && len(items) == 0 {
		return nil, fmt.Errorf("array is empty")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if strings.TrimSpace(item) == "" || strings.TrimSpace(item) != item || seen[item] {
			return nil, fmt.Errorf("array is not canonical and unique")
		}
		seen[item] = true
	}
	return items, nil
}

func ValidateStaticClients(clients []map[string]any) error {
	allowed := map[string]bool{"id": true, "name": true, "redirectURIs": true, "secret": true, "public": true, "trustedPeers": true}
	ids := map[string]bool{}
	peersByID := map[string][]string{}
	for _, client := range clients {
		for key := range client {
			if !allowed[key] && key != "secret_configured" && key != "secretConfigured" && key != "__secret_set" {
				return fmt.Errorf("static client contains an unsupported field")
			}
		}
		id, ok := client["id"].(string)
		if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id || ids[id] {
			return fmt.Errorf("static client id must be canonical and unique")
		}
		ids[id] = true
		if value, exists := client["name"]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("static client name must be a string")
			}
		}
		redirects, err := StringList(client["redirectURIs"], false)
		if err != nil {
			return fmt.Errorf("static client redirectURIs must be a non-empty unique string array")
		}
		for _, redirect := range redirects {
			if err := ValidateURL(redirect, true); err != nil {
				return fmt.Errorf("static client redirect URI: %w", err)
			}
		}
		secret, _ := client["secret"].(string)
		if value, exists := client["secret"]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("static client secret must be a string")
			}
		}
		public, _ := client["public"].(bool)
		if value, exists := client["public"]; exists {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("static client public must be a boolean")
			}
		}
		if public && secret != "" {
			return fmt.Errorf("public static clients must not have a secret")
		}
		if !public && secret == "" {
			return fmt.Errorf("confidential static clients require a secret")
		}
		if value, exists := client["trustedPeers"]; exists {
			peers, err := StringList(value, true)
			if err != nil {
				return fmt.Errorf("static client trustedPeers must be a unique string array")
			}
			peersByID[id] = peers
		}
	}
	for id, peers := range peersByID {
		for _, peer := range peers {
			if peer == id || !ids[peer] {
				return fmt.Errorf("static client trustedPeers references an invalid client")
			}
		}
	}
	return nil
}

func ValidateExpiry(expiry map[string]any) error {
	for key, value := range expiry {
		if key == "refreshTokens" {
			object, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("expiry.refreshTokens must be an object")
			}
			for nested, raw := range object {
				if nested != "reuseInterval" && nested != "validIfNotUsedFor" && nested != "absoluteLifetime" {
					return fmt.Errorf("expiry.refreshTokens contains an unsupported field")
				}
				if err := positiveDuration(raw); err != nil {
					return fmt.Errorf("expiry.refreshTokens.%s must be a positive duration", nested)
				}
			}
			continue
		}
		if key != "idTokens" && key != "signingKeys" {
			return fmt.Errorf("expiry contains an unsupported field")
		}
		if err := positiveDuration(value); err != nil {
			return fmt.Errorf("expiry.%s must be a positive duration", key)
		}
	}
	return nil
}

func positiveDuration(value any) error {
	text, ok := value.(string)
	if !ok || text == "" || strings.TrimSpace(text) != text {
		return fmt.Errorf("invalid duration")
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration <= 0 {
		return fmt.Errorf("invalid duration")
	}
	return nil
}

func ValidateExtra(extra map[string]any) error {
	allowedTop := map[string]map[string]string{"logger": {"level": "string", "format": "string"}, "frontend": {"issuer": "string", "logoURL": "url", "dir": "path", "theme": "string"}, "grpc": {"addr": "listener"}, "telemetry": {"http": "listener"}}
	for key, value := range extra {
		fields, ok := allowedTop[key]
		if !ok {
			return fmt.Errorf("extra contains an unsupported field")
		}
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("extra.%s must be an object", key)
		}
		for nested, raw := range object {
			kind, ok := fields[nested]
			if !ok {
				return fmt.Errorf("extra.%s contains an unsupported field", key)
			}
			text, ok := raw.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("extra.%s.%s must be a non-empty string", key, nested)
			}
			switch kind {
			case "url":
				if err := ValidateURL(text, false); err != nil {
					return fmt.Errorf("extra.%s.%s: %w", key, nested, err)
				}
			case "listener":
				if err := ValidateListener(text); err != nil {
					return fmt.Errorf("extra.%s.%s: %w", key, nested, err)
				}
			case "path":
				if !strings.HasPrefix(text, "/") || path.Clean(text) != text {
					return fmt.Errorf("extra.%s.%s must be an absolute canonical path", key, nested)
				}
			}
		}
		if key == "logger" {
			if level, ok := object["level"].(string); ok && !oneOf(level, "debug", "info", "warn", "error") {
				return fmt.Errorf("extra.logger.level is unsupported")
			}
			if format, ok := object["format"].(string); ok && !oneOf(format, "json", "text") {
				return fmt.Errorf("extra.logger.format is unsupported")
			}
		}
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func isEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

type runtimeDocument struct {
	Issuer  string `yaml:"issuer"`
	Storage struct {
		Type   string `yaml:"type"`
		Config struct {
			InCluster bool `yaml:"inCluster"`
		} `yaml:"config"`
	} `yaml:"storage"`
	Web struct {
		HTTP string `yaml:"http"`
	} `yaml:"web"`
	OAuth2 struct {
		SkipApprovalScreen bool `yaml:"skipApprovalScreen"`
	} `yaml:"oauth2"`
	StaticClients []map[string]any `yaml:"staticClients"`
	Connectors    []struct {
		Type   string         `yaml:"type"`
		ID     string         `yaml:"id"`
		Name   string         `yaml:"name"`
		Config map[string]any `yaml:"config"`
	} `yaml:"connectors"`
	Expiry    map[string]any `yaml:"expiry"`
	Logger    map[string]any `yaml:"logger"`
	Frontend  map[string]any `yaml:"frontend"`
	GRPC      map[string]any `yaml:"grpc"`
	Telemetry map[string]any `yaml:"telemetry"`
}

func ValidateRuntimeYAML(raw []byte, maxBytes int64) error {
	if int64(len(raw)) == 0 || int64(len(raw)) > maxBytes {
		return fmt.Errorf("Dex configuration exceeds the bounded input size")
	}
	var document runtimeDocument
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("Dex configuration is not valid closed-shape YAML")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("Dex configuration must contain exactly one YAML document")
	}
	if err := ValidateURL(document.Issuer, false); err != nil {
		return fmt.Errorf("issuer: %w", err)
	}
	if document.Storage.Type != "kubernetes" || !document.Storage.Config.InCluster {
		return fmt.Errorf("storage must be kubernetes with inCluster enabled")
	}
	if err := ValidateListener(document.Web.HTTP); err != nil {
		return fmt.Errorf("web.http: %w", err)
	}
	if err := ValidateStaticClients(document.StaticClients); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, connector := range document.Connectors {
		if connector.ID == "" || connector.Name == "" || ids[connector.ID] {
			return fmt.Errorf("connector ids and names must be non-empty and ids unique")
		}
		ids[connector.ID] = true
		if err := ValidateConnector(connector.Type, connector.Config); err != nil {
			return fmt.Errorf("connector %s is invalid: %w", connector.ID, err)
		}
	}
	if document.Expiry != nil {
		if err := ValidateExpiry(document.Expiry); err != nil {
			return err
		}
	}
	extra := map[string]any{}
	if document.Logger != nil {
		extra["logger"] = document.Logger
	}
	if document.Frontend != nil {
		extra["frontend"] = document.Frontend
	}
	if document.GRPC != nil {
		extra["grpc"] = document.GRPC
	}
	if document.Telemetry != nil {
		extra["telemetry"] = document.Telemetry
	}
	return ValidateExtra(extra)
}

func DecodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}
	err := json.Unmarshal(raw, &out)
	return out, err
}
