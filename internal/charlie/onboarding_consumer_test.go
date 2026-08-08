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

type fakeAgentSecretWriter struct {
	writes    int
	rollbacks int
	bundles   []AgentSecretBundle
	events    *[]string
}

type fakeAgentInstaller struct {
	prepares  int
	installs  int
	prunes    int
	rollbacks int
	last      AgentInstallSpec
	events    *[]string
}

func (i *fakeAgentInstaller) PruneSupersededRepositories(_ context.Context, spec AgentInstallSpec) error {
	i.prunes++
	i.last = spec
	if i.events != nil {
		*i.events = append(*i.events, "prune_repositories")
	}
	return nil
}

func (i *fakeAgentInstaller) PruneSupersededSecrets(context.Context, AgentInstallSpec) error {
	return nil
}

func (i *fakeAgentInstaller) PrepareNamespace(_ context.Context, installationID uuid.UUID) (func(context.Context) error, error) {
	if installationID == uuid.Nil {
		return nil, errors.New("missing installation identity")
	}
	i.prepares++
	if i.events != nil {
		*i.events = append(*i.events, "prepare_namespace")
	}
	return func(context.Context) error { return nil }, nil
}

func (i *fakeAgentInstaller) Install(_ context.Context, spec AgentInstallSpec) (AgentInstallReceipt, error) {
	i.installs++
	if i.events != nil {
		*i.events = append(*i.events, "install")
	}
	i.last = spec
	return AgentInstallReceipt{Rollback: func(context.Context) error { i.rollbacks++; return nil }}, nil
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
	installer := &fakeAgentInstaller{events: &events}
	consumer := &OnboardingConsumer{
		Store: store, Secrets: secrets, Installer: installer, Encryptor: encryptor,
		BridgeServerDNS: "charlie-agent-bridge.astronomer-charlie.svc",
		MCPServerDNS:    "astronomer-charlie-mcp.astronomer.svc",
		Now:             func() time.Time { return fixture.now },
		Auditor:         &authorityAuditFake{},
	}

	if _, err := consumer.Consume(context.Background(), validated, uuid.New()); err == nil {
		t.Fatal("injected DB failure was accepted")
	}
	if store.connection != nil || secrets.rollbacks != 1 || installer.rollbacks != 1 {
		t.Fatalf("partial consume was not rolled back: connection=%+v secret_rollbacks=%d install_rollbacks=%d", store.connection, secrets.rollbacks, installer.rollbacks)
	}
	if len(events) < 4 || events[0] != "prepare_namespace" || events[1] != "write_secret" || events[2] != "prune_repositories" || events[3] != "install" {
		t.Fatalf("unsafe onboarding side-effect order: %v", events)
	}

	store.failAdvance = 0
	status, err := consumer.Consume(context.Background(), validated, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "consumed" || status.Idempotent || store.connection == nil || store.connection.OnboardingState != "consumed" {
		t.Fatalf("retry did not consume package: status=%+v connection=%+v", status, store.connection)
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
	if !replay.Idempotent || replay.State != "consumed" || secrets.writes != writesAfterSuccess || len(store.created) != 1 {
		t.Fatalf("replay recreated state or Secret: replay=%+v writes=%d creates=%d", replay, secrets.writes, len(store.created))
	}
	if installer.prepares != 2 || installer.installs != 2 || installer.prunes != 2 || installer.last.CentralCAPEM == "" || installer.last.Trust.Astronomer.MCPServerPrivateKey == "" ||
		len(installer.last.ActionSigningPublicKey) != 32 || installer.last.ActionSigningKeyFingerprint == "" ||
		installer.last.ReplicaCount != 2 || string(installer.last.OnboardingPackage) != string(validated.RawPackage) ||
		string(secrets.bundles[0].OnboardingPackage) != string(validated.RawPackage) {
		t.Fatalf("Argo installation was not transactionally integrated: installs=%d", installer.installs)
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
		Store: store, Secrets: &fakeAgentSecretWriter{events: &events}, Installer: &fakeAgentInstaller{events: &events}, Encryptor: encryptor,
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
