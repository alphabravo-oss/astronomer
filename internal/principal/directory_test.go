package principal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeAdapter struct {
	typeName string
	results  []ExternalPrincipal
	err      error
}

func (a fakeAdapter) Type() string { return a.typeName }
func (a fakeAdapter) Search(_ context.Context, connector Connector, _ string, limit int) ([]ExternalPrincipal, error) {
	if a.err != nil {
		return nil, a.err
	}
	results := append([]ExternalPrincipal(nil), a.results...)
	for index := range results {
		results[index].ConnectorID = connector.ID
		results[index].ConnectorName = connector.Name
		results[index].ConnectorType = connector.Type
	}
	return results[:min(limit, len(results))], nil
}
func (a fakeAdapter) Resolve(_ context.Context, connector Connector, subject string) (ExternalPrincipal, error) {
	for _, candidate := range a.results {
		if candidate.Subject == subject {
			candidate.ConnectorID = connector.ID
			candidate.ConnectorName = connector.Name
			candidate.ConnectorType = connector.Type
			return candidate, nil
		}
	}
	return ExternalPrincipal{}, errors.New("not found")
}

func TestDirectorySearchReportsCapabilitiesAndCapsResults(t *testing.T) {
	ldapID := uuid.New()
	oidcID := uuid.New()
	directory := NewDirectory(fakeAdapter{typeName: "ldap", results: []ExternalPrincipal{
		{Subject: "2", Email: "z@example.com", DisplayName: "Zed"},
		{Subject: "1", Email: "a@example.com", DisplayName: "Ada"},
	}})

	results, statuses := directory.Search(context.Background(), []Connector{
		{ID: ldapID, Name: "employees", Type: "ldap"},
		{ID: oidcID, Name: "partners", Type: "oidc"},
	}, "example", 1)

	if len(results) != 1 || results[0].ConnectorID != ldapID {
		t.Fatalf("results = %#v, want one LDAP result", results)
	}
	if len(statuses) != 2 || !statuses[0].Supported || statuses[1].Supported {
		t.Fatalf("statuses = %#v, want supported LDAP and unsupported OIDC", statuses)
	}
	if statuses[1].Error != ErrUnsupported.Error() {
		t.Fatalf("unsupported error = %q", statuses[1].Error)
	}
}

func TestDirectoryResolveRejectsUnsupportedAndBlankSubjects(t *testing.T) {
	directory := NewDirectory(fakeAdapter{typeName: "ldap"})
	if _, err := directory.Resolve(context.Background(), Connector{Type: "oidc"}, "stable-id"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported resolve error = %v", err)
	}
	if _, err := directory.Resolve(context.Background(), Connector{Type: "ldap"}, " "); err == nil {
		t.Fatal("blank subject unexpectedly resolved")
	}
}

func TestParseLDAPConfigRequiresSearchAndCredentials(t *testing.T) {
	_, err := parseLDAPConfig(map[string]any{"host": "ldap.example:636"})
	if err == nil {
		t.Fatal("incomplete LDAP config unexpectedly accepted")
	}
	config, err := parseLDAPConfig(map[string]any{
		"host": "ldap.example:636", "bindDN": "cn=reader", "bindPW": "secret",
		"userSearch": map[string]any{"baseDN": "ou=people,dc=example", "username": "uid", "idAttr": "entryUUID", "emailAttr": "mail"},
	})
	if err != nil || config.idAttr != "entryUUID" {
		t.Fatalf("valid LDAP config: config=%#v err=%v", config, err)
	}
}
