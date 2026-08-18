package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fakeOnboardingStore struct {
	installationID uuid.UUID
	connection     *sqlc.CharlieConnection
	failAdvance    int
	advanceCalls   int
	created        []sqlc.CreateCharlieConnectionParams
	automationUser sqlc.User
	triggerRules   []sqlc.CreateCharlieTriggerRuleParams
	transactions   int
}

func TestOnboardingExpiryMetadataUsesEarliestCredentialAndCertificateDeadline(t *testing.T) {
	fixture := newOnboardingFixture(t)
	validated, err := ValidateOnboardingPackage(fixture.signed(t), fixture.confirmation)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, artifact, err := onboardingCredentialExpiries(validated.Package)
	if err != nil {
		t.Fatal(err)
	}
	wantCredentialExpiry := fixture.now.Add(30 * time.Minute)
	if !enrollment.Equal(wantCredentialExpiry) || !artifact.Equal(wantCredentialExpiry) {
		t.Fatalf("credential expiries enrollment=%s artifact=%s", enrollment, artifact)
	}
	localExpiry := fixture.now.Add(90 * 24 * time.Hour)
	certificate, err := onboardingCertificateExpiry(validated.Package, localExpiry)
	if err != nil || !certificate.Equal(localExpiry) {
		t.Fatalf("certificate expiry=%s err=%v, want earliest local deadline %s", certificate, err, localExpiry)
	}
}

func (s *fakeOnboardingStore) WithinOnboardingTransaction(ctx context.Context, callback func(OnboardingTransaction) error) error {
	s.transactions++
	previous := s.connection
	previousAdvanceCalls := s.advanceCalls
	previousCreated := len(s.created)
	previousUser := s.automationUser
	previousRules := len(s.triggerRules)
	if err := callback((*fakeOnboardingTx)(s)); err != nil {
		s.connection = previous
		s.advanceCalls = previousAdvanceCalls
		s.created = s.created[:previousCreated]
		s.automationUser = previousUser
		s.triggerRules = s.triggerRules[:previousRules]
		return err
	}
	return nil
}

func (tx *fakeOnboardingTx) GetUserByUsername(context.Context, string) (sqlc.User, error) {
	if tx.automationUser.ID == uuid.Nil {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return tx.automationUser, nil
}

func (tx *fakeOnboardingTx) CreateServiceUser(_ context.Context, params sqlc.CreateServiceUserParams) (sqlc.User, error) {
	tx.automationUser = sqlc.User{ID: uuid.New(), Email: params.Email, Username: params.Username, IsActive: true, IsService: true}
	return tx.automationUser, nil
}

func (tx *fakeOnboardingTx) GetCharlieAutomationRole(context.Context) (sqlc.GlobalRole, error) {
	return sqlc.GlobalRole{ID: uuid.New(), Name: "Charlie Automation", IsBuiltin: true, Rules: json.RawMessage(`[{"resource":"charlie","verbs":["create","read"]}]`)}, nil
}

func (tx *fakeOnboardingTx) EnsureCharlieAutomationBinding(context.Context, sqlc.EnsureCharlieAutomationBindingParams) (sqlc.GlobalRoleBinding, error) {
	return sqlc.GlobalRoleBinding{}, nil
}

func (tx *fakeOnboardingTx) CreateCharlieTriggerRule(_ context.Context, params sqlc.CreateCharlieTriggerRuleParams) (sqlc.CharlieTriggerRule, error) {
	tx.triggerRules = append(tx.triggerRules, params)
	return sqlc.CharlieTriggerRule{ID: uuid.New(), ConnectionID: params.ConnectionID, Name: params.Name, Enabled: params.Enabled}, nil
}

type fakeOnboardingTx fakeOnboardingStore

func (tx *fakeOnboardingTx) GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{InstanceID: tx.installationID}, nil
}

func (tx *fakeOnboardingTx) GetCharlieConnectionByPackageID(_ context.Context, packageID string) (sqlc.CharlieConnection, error) {
	if tx.connection != nil && tx.connection.OnboardingPackageID == packageID {
		return *tx.connection, nil
	}
	return sqlc.CharlieConnection{}, pgx.ErrNoRows
}

func (tx *fakeOnboardingTx) CreateCharlieConnection(_ context.Context, params sqlc.CreateCharlieConnectionParams) (sqlc.CharlieConnection, error) {
	tx.created = append(tx.created, params)
	connection := sqlc.CharlieConnection{ID: uuid.New(), OnboardingPackageID: params.OnboardingPackageID, OnboardingState: params.OnboardingState}
	tx.connection = &connection
	return connection, nil
}

func (tx *fakeOnboardingTx) AdvanceCharlieOnboardingState(_ context.Context, params sqlc.AdvanceCharlieOnboardingStateParams) (sqlc.CharlieConnection, error) {
	tx.advanceCalls++
	if tx.failAdvance == tx.advanceCalls {
		return sqlc.CharlieConnection{}, errors.New("injected state failure")
	}
	if tx.connection == nil || tx.connection.OnboardingState != params.ExpectedState {
		return sqlc.CharlieConnection{}, pgx.ErrNoRows
	}
	tx.connection.OnboardingState = params.NextState
	tx.connection.AgentSecretHmac = params.AgentSecretHmac
	return *tx.connection, nil
}

func (tx *fakeOnboardingTx) LockCharlieConnectionActivation(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	if tx.connection == nil {
		return nil, pgx.ErrNoRows
	}
	return []uuid.UUID{tx.connection.ID}, nil
}

func (tx *fakeOnboardingTx) DeactivateCharlieConnectionsForReplacement(context.Context, uuid.UUID) error {
	return nil
}

func (tx *fakeOnboardingTx) ActivateCharlieConnection(_ context.Context, params sqlc.ActivateCharlieConnectionParams) (sqlc.CharlieConnection, error) {
	if tx.connection == nil || tx.connection.ID != params.ID || tx.connection.OnboardingState != "consumed" {
		return sqlc.CharlieConnection{}, pgx.ErrNoRows
	}
	tx.connection.Active = true
	tx.connection.OnboardingState = "active"
	tx.connection.HealthState = params.HealthState
	return *tx.connection, nil
}

type fakeAgentSecretWriter struct {
	writes    int
	rollbacks int
	bundles   []AgentSecretBundle
	events    *[]string
}

func (w *fakeAgentSecretWriter) WriteAgentSecret(_ context.Context, bundle AgentSecretBundle) (SecretWriteReceipt, error) {
	w.writes++
	if w.events != nil {
		*w.events = append(*w.events, "write_secret")
	}
	w.bundles = append(w.bundles, bundle)
	return SecretWriteReceipt{IntegrityHMAC: "safe-integrity-hmac", Rollback: func(context.Context) error {
		w.rollbacks++
		return nil
	}}, nil
}

type fakeAgentInstaller struct {
	prepares  int
	activates int
}

func (f *fakeAgentInstaller) Prepare(context.Context) error { f.prepares++; return nil }
func (f *fakeAgentInstaller) Activate(context.Context, ActivationRequest) error {
	f.activates++
	return nil
}

func TestOnboardingConsumerRollsBackRetriesAndReplaysIdempotently(t *testing.T) {
	fixture := newOnboardingFixture(t)
	validated, err := ValidateOnboardingPackage(fixture.signed(t), fixture.confirmation)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := auth.GenerateKey()
	encryptor, _ := auth.NewEncryptor(key)
	store := &fakeOnboardingStore{installationID: uuid.MustParse("3c608d44-848c-45d6-bd86-246be0b880af"), failAdvance: 2}
	events := []string{}
	secrets := &fakeAgentSecretWriter{events: &events}
	runtime := &fakeAgentInstaller{}
	consumer := &OnboardingConsumer{
		Store: store, Secrets: secrets, Encryptor: encryptor,
		BridgeServerDNS: "charlie-agent-bridge.astronomer-charlie.svc",
		MCPServerDNS:    "astronomer-charlie-mcp.astronomer.svc",
		Runtime:         runtime,
		Now:             func() time.Time { return fixture.now },
		Auditor:         &authorityAuditFake{},
	}

	if _, err := consumer.Consume(context.Background(), validated, uuid.New()); err == nil {
		t.Fatal("injected DB failure was accepted")
	}
	if store.connection != nil || secrets.rollbacks != 1 {
		t.Fatalf("partial consume was not rolled back: connection=%+v secret_rollbacks=%d", store.connection, secrets.rollbacks)
	}
	if len(events) != 1 || events[0] != "write_secret" {
		t.Fatalf("unsafe onboarding side-effect order: %v", events)
	}

	store.failAdvance = 0
	status, err := consumer.Consume(context.Background(), validated, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "active" || status.Idempotent || store.connection == nil || store.connection.OnboardingState != "active" || !store.connection.Active || store.connection.HealthState != "installing" {
		t.Fatalf("retry did not consume package: status=%+v connection=%+v", status, store.connection)
	}
	if runtime.prepares < 1 || runtime.activates != 1 {
		t.Fatalf("Charlie agent activation was not triggered by consume: prepares=%d activates=%d", runtime.prepares, runtime.activates)
	}
	if len(store.created) != 1 || store.created[0].ReplicaCount != int32(validated.Package.ReplicaCount) {
		t.Fatalf("signed replica count was not persisted: creates=%+v", store.created)
	}
	if !store.automationUser.IsService || len(store.triggerRules) != len(DefaultTriggerRules()) {
		t.Fatalf("onboarding did not seed isolated automation defaults: user=%#v rules=%d", store.automationUser, len(store.triggerRules))
	}
	for _, rule := range store.triggerRules {
		if rule.Enabled || rule.ModeCeiling != string(ModeReadOnly) {
			t.Fatalf("default rule silently enabled authority: %#v", rule)
		}
	}
	writesAfterSuccess := secrets.writes
	replay, err := consumer.Consume(context.Background(), validated, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Idempotent || replay.State != "active" || secrets.writes != writesAfterSuccess || len(store.created) != 1 {
		t.Fatalf("replay recreated state or Secret: replay=%+v writes=%d creates=%d", replay, secrets.writes, len(store.created))
	}
	if len(secrets.bundles) != 2 || secrets.bundles[0].Name != "charlie-agent-bootstrap" ||
		string(secrets.bundles[0].OnboardingPackage) != string(validated.RawPackage) {
		t.Fatalf("Flux bootstrap Secret was not transactionally integrated: bundles=%d", len(secrets.bundles))
	}

	serialized, err := json.Marshal(struct {
		Status     OnboardingStatus                   `json:"status"`
		Connection sqlc.CreateCharlieConnectionParams `json:"connection"`
	}{Status: status, Connection: store.created[0]})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{validated.EnrollmentCredentials[0], validated.EnrollmentCredentials[1], validated.ArtifactCredential, secrets.bundles[0].BridgeServerPrivateKey, secrets.bundles[0].MCPClientPrivateKey} {
		if stringContains(string(serialized), secret) {
			t.Fatal("onboarding secret/private key leaked into DB metadata or API serialization")
		}
	}
}

func TestOnboardingAuditFailureCreatesNoStateOrExternalMaterial(t *testing.T) {
	fixture := newOnboardingFixture(t)
	validated, err := ValidateOnboardingPackage(fixture.signed(t), fixture.confirmation)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := auth.GenerateKey()
	encryptor, _ := auth.NewEncryptor(key)
	store := &fakeOnboardingStore{installationID: uuid.New()}
	events := []string{}
	consumer := &OnboardingConsumer{
		Store: store, Secrets: &fakeAgentSecretWriter{events: &events}, Encryptor: encryptor,
		BridgeServerDNS: "charlie-agent-bridge.astronomer-charlie.svc", MCPServerDNS: "astronomer-charlie-mcp.astronomer.svc",
		Auditor: &authorityAuditFake{err: errors.New("database-SENTINEL")},
	}

	_, err = consumer.Consume(context.Background(), validated, uuid.New())
	if err == nil || strings.Contains(err.Error(), "database-SENTINEL") || OnboardingFailureCode(err) != "onboarding.audit_unavailable" {
		t.Fatalf("onboarding audit failure was not bounded: code=%s err=%v", OnboardingFailureCode(err), err)
	}
	if store.transactions != 0 || store.connection != nil || len(events) != 0 {
		t.Fatalf("audit failure changed onboarding state: transactions=%d connection=%+v events=%v", store.transactions, store.connection, events)
	}
}
