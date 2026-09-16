package principal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	ldap "github.com/go-ldap/ldap/v3"
)

type LDAPAdapter struct {
	dial func(string, ...ldap.DialOpt) (*ldap.Conn, error)
}

var ldapAttributePattern = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9-]*|[0-9]+(?:\.[0-9]+)+)(?:;[A-Za-z0-9-]+)*$`)

func NewLDAPAdapter() *LDAPAdapter { return &LDAPAdapter{dial: ldap.DialURL} }

func (a *LDAPAdapter) Type() string { return "ldap" }

type ldapConfig struct {
	host, bindDN, bindPW, baseDN                string
	usernameAttr, idAttr, emailAttr, nameAttr   string
	insecureNoSSL, insecureSkipVerify, startTLS bool
	rootCAData                                  string
}

func parseLDAPConfig(raw map[string]any) (ldapConfig, error) {
	search, ok := raw["userSearch"].(map[string]any)
	if !ok {
		return ldapConfig{}, errors.New("LDAP userSearch is missing")
	}
	str := func(values map[string]any, key string) string {
		value, _ := values[key].(string)
		return strings.TrimSpace(value)
	}
	config := ldapConfig{
		host: str(raw, "host"), bindDN: str(raw, "bindDN"), bindPW: str(raw, "bindPW"),
		baseDN: str(search, "baseDN"), usernameAttr: str(search, "username"),
		idAttr: str(search, "idAttr"), emailAttr: str(search, "emailAttr"), nameAttr: str(search, "nameAttr"),
		rootCAData: str(raw, "rootCAData"),
	}
	config.insecureNoSSL, _ = raw["insecureNoSSL"].(bool)
	config.insecureSkipVerify, _ = raw["insecureSkipVerify"].(bool)
	config.startTLS, _ = raw["startTLS"].(bool)
	if config.host == "" || config.bindDN == "" || config.bindPW == "" || config.baseDN == "" || config.usernameAttr == "" || config.idAttr == "" || config.emailAttr == "" {
		return ldapConfig{}, errors.New("LDAP directory configuration is incomplete")
	}
	for _, attribute := range []string{config.usernameAttr, config.idAttr, config.emailAttr, config.nameAttr} {
		if attribute != "" && !ldapAttributePattern.MatchString(attribute) {
			return ldapConfig{}, errors.New("LDAP directory contains an invalid attribute name")
		}
	}
	return config, nil
}

func (a *LDAPAdapter) Search(ctx context.Context, connector Connector, query string, limit int) ([]ExternalPrincipal, error) {
	config, err := parseLDAPConfig(connector.Config)
	if err != nil {
		return nil, err
	}
	escaped := ldap.EscapeFilter(strings.TrimSpace(query))
	attributes := uniqueStrings(config.idAttr, config.emailAttr, config.usernameAttr, config.nameAttr)
	filter := fmt.Sprintf("(|(%s=*%s*)(%s=*%s*)", config.usernameAttr, escaped, config.emailAttr, escaped)
	if config.nameAttr != "" {
		filter += fmt.Sprintf("(%s=*%s*)", config.nameAttr, escaped)
	}
	filter += ")"
	return a.run(ctx, connector, config, filter, min(max(limit, 1), MaxResults), attributes)
}

func (a *LDAPAdapter) Resolve(ctx context.Context, connector Connector, subject string) (ExternalPrincipal, error) {
	config, err := parseLDAPConfig(connector.Config)
	if err != nil {
		return ExternalPrincipal{}, err
	}
	filter := fmt.Sprintf("(%s=%s)", config.idAttr, ldap.EscapeFilter(subject))
	rows, err := a.run(ctx, connector, config, filter, 2, uniqueStrings(config.idAttr, config.emailAttr, config.usernameAttr, config.nameAttr))
	if err != nil {
		return ExternalPrincipal{}, err
	}
	if len(rows) != 1 {
		return ExternalPrincipal{}, errors.New("external principal was not found uniquely")
	}
	return rows[0], nil
}

func (a *LDAPAdapter) run(ctx context.Context, connector Connector, config ldapConfig, filter string, limit int, attributes []string) ([]ExternalPrincipal, error) {
	tlsConfig, err := ldapTLSConfig(config)
	if err != nil {
		return nil, err
	}
	scheme := "ldaps"
	if config.insecureNoSSL || config.startTLS {
		scheme = "ldap"
	}
	conn, err := a.dial(scheme+"://"+config.host, ldap.DialWithDialer(&net.Dialer{Timeout: 4 * time.Second}), ldap.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, fmt.Errorf("connect LDAP directory: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetTimeout(time.Until(deadline))
	} else {
		conn.SetTimeout(4 * time.Second)
	}
	if config.startTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			return nil, fmt.Errorf("start LDAP TLS: %w", err)
		}
	}
	if err := conn.Bind(config.bindDN, config.bindPW); err != nil {
		return nil, fmt.Errorf("bind LDAP directory: %w", err)
	}
	request := ldap.NewSearchRequest(config.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, limit, 4, false, filter, attributes, nil)
	response, err := conn.Search(request)
	if err != nil {
		return nil, fmt.Errorf("search LDAP directory: %w", err)
	}
	results := make([]ExternalPrincipal, 0, len(response.Entries))
	for _, entry := range response.Entries {
		subject := strings.TrimSpace(entry.GetAttributeValue(config.idAttr))
		email := strings.ToLower(strings.TrimSpace(entry.GetAttributeValue(config.emailAttr)))
		username := strings.TrimSpace(entry.GetAttributeValue(config.usernameAttr))
		if subject == "" || email == "" || username == "" {
			continue
		}
		displayName := strings.TrimSpace(entry.GetAttributeValue(config.nameAttr))
		if displayName == "" {
			displayName = username
		}
		results = append(results, ExternalPrincipal{ConnectorID: connector.ID, ConnectorName: connector.Name, ConnectorType: connector.Type, Subject: subject, Email: email, Username: username, DisplayName: displayName})
	}
	return results, nil
}

func ldapTLSConfig(config ldapConfig) (*tls.Config, error) {
	host, _, err := net.SplitHostPort(config.host)
	if err != nil {
		return nil, errors.New("LDAP host must include an explicit port")
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if config.rootCAData != "" && !pool.AppendCertsFromPEM([]byte(config.rootCAData)) {
		return nil, errors.New("LDAP rootCAData contains no certificates")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: pool, InsecureSkipVerify: config.insecureSkipVerify}, nil // #nosec G402 -- explicit connector policy mirrors Dex.
}

func uniqueStrings(values ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
