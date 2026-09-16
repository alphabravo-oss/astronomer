package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/principal"
)

type principalTestAdapter struct{ resolved principal.ExternalPrincipal }

func (principalTestAdapter) Type() string { return "test-directory" }
func (a principalTestAdapter) Search(_ context.Context, connector principal.Connector, _ string, _ int) ([]principal.ExternalPrincipal, error) {
	value := a.resolved
	value.ConnectorID, value.ConnectorName, value.ConnectorType = connector.ID, connector.Name, connector.Type
	return []principal.ExternalPrincipal{value}, nil
}
func (a principalTestAdapter) Resolve(_ context.Context, connector principal.Connector, subject string) (principal.ExternalPrincipal, error) {
	if subject != a.resolved.Subject {
		return principal.ExternalPrincipal{}, errors.New("unknown subject")
	}
	value := a.resolved
	value.ConnectorID, value.ConnectorName, value.ConnectorType = connector.ID, connector.Name, connector.Type
	return value, nil
}

type principalTestQueries struct {
	local      []sqlc.SearchPrincipalUsersRow
	connectors []sqlc.DexConnector
	audits     []sqlc.CreateAuditLogV1Params
	auditErr   error
}

func (q *principalTestQueries) SearchPrincipalUsers(context.Context, sqlc.SearchPrincipalUsersParams) ([]sqlc.SearchPrincipalUsersRow, error) {
	return q.local, nil
}
func (q *principalTestQueries) ListEnabledDexConnectors(context.Context) ([]sqlc.DexConnector, error) {
	return q.connectors, nil
}
func (q *principalTestQueries) GetDexConnectorByID(_ context.Context, id uuid.UUID) (sqlc.DexConnector, error) {
	for _, connector := range q.connectors {
		if connector.ID == id {
			return connector, nil
		}
	}
	return sqlc.DexConnector{}, pgx.ErrNoRows
}
func (q *principalTestQueries) CreateAuditLogV1(_ context.Context, params sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, params)
	return q.auditErr
}

type principalTestTx struct {
	materialized sqlc.MaterializeExternalPrincipalRow
	params       sqlc.MaterializeExternalPrincipalParams
	outboxes     []sqlc.UpsertAuditOutboxParams
}

func (tx *principalTestTx) MaterializeExternalPrincipal(_ context.Context, params sqlc.MaterializeExternalPrincipalParams) (sqlc.MaterializeExternalPrincipalRow, error) {
	tx.params = params
	return tx.materialized, nil
}
func (tx *principalTestTx) UpsertAuditOutbox(_ context.Context, params sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	tx.outboxes = append(tx.outboxes, params)
	return sqlc.AuditOutbox{ID: params.ID}, nil
}

func TestPrincipalSearchCombinesLocalExternalAndReportsUnsupported(t *testing.T) {
	localID := uuid.New()
	supportedID := uuid.New()
	unsupportedID := uuid.New()
	queries := &principalTestQueries{
		local: []sqlc.SearchPrincipalUsersRow{{UserID: localID, Email: "local@example.com", Username: "local", FirstName: "Local", LastName: "User"}},
		connectors: []sqlc.DexConnector{
			{ID: supportedID, Name: "employees", Type: "test-directory", DisplayName: "Employees", Config: json.RawMessage(`{}`), Enabled: true},
			{ID: unsupportedID, Name: "partners", Type: "oidc", DisplayName: "Partners", Config: json.RawMessage(`{}`), Enabled: true},
		},
	}
	directory := principal.NewDirectory(principalTestAdapter{resolved: principal.ExternalPrincipal{Subject: "stable-42", Email: "external@example.com", Username: "external", DisplayName: "External User"}})
	handler := NewPrincipalHandler(queries, directory)

	recorder := httptest.NewRecorder()
	handler.Search(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/rbac/principals/?q=exam", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data principalSearchResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Principals) != 2 || response.Data.Principals[0].Kind != "local" || response.Data.Principals[1].Kind != "external" {
		t.Fatalf("principals = %#v", response.Data.Principals)
	}
	if len(response.Data.Connectors) != 2 || response.Data.Connectors[1].Supported {
		t.Fatalf("connector statuses = %#v", response.Data.Connectors)
	}
	if len(queries.audits) != 1 || queries.audits[0].Action != "principal.search" {
		t.Fatalf("search audit = %#v", queries.audits)
	}
}

func TestPrincipalSearchRejectsAmbiguousOrShortQueries(t *testing.T) {
	handler := NewPrincipalHandler(&principalTestQueries{}, principal.NewDirectory())
	for _, target := range []string{"/api/v1/rbac/principals/?q=ab", "/api/v1/rbac/principals/?q=alice&q=bob"} {
		recorder := httptest.NewRecorder()
		handler.Search(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", target, recorder.Code)
		}
	}
}

func TestPrincipalSearchFailsClosedWhenAuditStorageIsUnavailable(t *testing.T) {
	handler := NewPrincipalHandler(&principalTestQueries{auditErr: errors.New("audit unavailable")}, principal.NewDirectory())
	recorder := httptest.NewRecorder()
	handler.Search(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/rbac/principals/?q=alice", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
}

func TestMaterializePrincipalRevalidatesSubjectAndAuditsTransactionally(t *testing.T) {
	connectorID := uuid.New()
	userID := uuid.New()
	principalID := uuid.New()
	queries := &principalTestQueries{connectors: []sqlc.DexConnector{{ID: connectorID, Name: "employees", Type: "test-directory", Config: json.RawMessage(`{}`), Enabled: true}}}
	directory := principal.NewDirectory(principalTestAdapter{resolved: principal.ExternalPrincipal{Subject: "stable-42", Email: "Ada@Example.COM", Username: "ada", DisplayName: "Ada Lovelace"}})
	tx := &principalTestTx{materialized: sqlc.MaterializeExternalPrincipalRow{ID: principalID, ConnectorID: connectorID, Subject: "stable-42", Email: "ada@example.com", Username: "ada", DisplayName: "Ada Lovelace", UserID: userID, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	handler := NewPrincipalHandler(queries, directory)
	handler.SetRunTx(func(ctx context.Context, fn func(PrincipalMutationTx) error) error { return fn(tx) })

	body := bytes.NewBufferString(`{"connector_id":"` + connectorID.String() + `","subject":"stable-42"}`)
	recorder := httptest.NewRecorder()
	handler.Materialize(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/rbac/principals/materialize/", body))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if tx.params.PrincipalEmail != "ada@example.com" || tx.params.TargetSubject != "stable-42" {
		t.Fatalf("materialization params = %#v", tx.params)
	}
	if len(tx.outboxes) != 1 || tx.outboxes[0].Action != "principal.materialized" {
		t.Fatalf("outbox = %#v", tx.outboxes)
	}
}

func TestMaterializePrincipalDoesNotRevealUnknownOrDisabledConnector(t *testing.T) {
	knownID := uuid.New()
	handler := NewPrincipalHandler(&principalTestQueries{connectors: []sqlc.DexConnector{{ID: knownID, Enabled: false}}}, principal.NewDirectory())
	handler.SetRunTx(func(context.Context, func(PrincipalMutationTx) error) error { return nil })
	for _, connectorID := range []uuid.UUID{knownID, uuid.New()} {
		body := bytes.NewBufferString(`{"connector_id":"` + connectorID.String() + `","subject":"stable-42"}`)
		recorder := httptest.NewRecorder()
		handler.Materialize(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/rbac/principals/materialize/", body))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("connector %s status = %d, want 404", connectorID, recorder.Code)
		}
	}
}

func TestSSOClaimsPrematerializedPrincipalBeforeEmailLookup(t *testing.T) {
	claimed := sqlc.User{ID: uuid.New(), Email: "ada@example.com", Username: "ext_ada", IsActive: true}
	queries := &fakeTTLSSOQueries{claimUser: claimed}
	user, provisioned, linked, err := findOrCreateSSOUser(context.Background(), queries, &auth.SSOUserInfo{
		Email: "Ada@Example.COM", Username: "ada", FirstName: "Ada", LastName: "Lovelace",
		ConnectorID: "employees", Subject: "stable-42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != claimed.ID || provisioned || !linked {
		t.Fatalf("user=%#v provisioned=%v linked=%v", user, provisioned, linked)
	}
}
