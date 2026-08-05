package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type mcpRuntimeFakeQueries struct {
	connection sqlc.CharlieConnection
	user       sqlc.User
	role       sqlc.GlobalRole
}

func (f *mcpRuntimeFakeQueries) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	return nil
}

func (f *mcpRuntimeFakeQueries) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	if !f.connection.Active {
		return sqlc.CharlieConnection{}, errors.New("inactive")
	}
	return f.connection, nil
}
func (f *mcpRuntimeFakeQueries) GetCharlieConnectionByDeploymentID(context.Context, string) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *mcpRuntimeFakeQueries) GetCharlieSessionByCentralID(context.Context, string) (sqlc.CharlieSession, error) {
	return sqlc.CharlieSession{}, nil
}
func (f *mcpRuntimeFakeQueries) ListCharlieSessionResources(context.Context, uuid.UUID) ([]sqlc.CharlieSessionResource, error) {
	return nil, nil
}
func (f *mcpRuntimeFakeQueries) GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error) {
	return sqlc.CharlieDelegation{}, nil
}
func (f *mcpRuntimeFakeQueries) GetActiveCharlieActionApproval(context.Context, sqlc.GetActiveCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error) {
	return sqlc.CharlieActionApproval{}, nil
}
func (f *mcpRuntimeFakeQueries) ConsumeCharlieActionApproval(context.Context, sqlc.ConsumeCharlieActionApprovalParams) (sqlc.CharlieActionApproval, error) {
	return sqlc.CharlieActionApproval{}, nil
}
func (f *mcpRuntimeFakeQueries) ClaimCharlieActionReceipt(context.Context, sqlc.ClaimCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error) {
	return sqlc.CharlieActionReceipt{}, nil
}
func (f *mcpRuntimeFakeQueries) GetCharlieActionReceipt(context.Context, string) (sqlc.CharlieActionReceipt, error) {
	return sqlc.CharlieActionReceipt{}, nil
}
func (f *mcpRuntimeFakeQueries) TransitionCharlieActionReceipt(context.Context, sqlc.TransitionCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error) {
	return sqlc.CharlieActionReceipt{}, nil
}
func (f *mcpRuntimeFakeQueries) ClaimCharlieAmbiguousReceipt(context.Context, sqlc.ClaimCharlieAmbiguousReceiptParams) (sqlc.CharlieActionReceipt, error) {
	return sqlc.CharlieActionReceipt{}, pgx.ErrNoRows
}
func (f *mcpRuntimeFakeQueries) GetUserByUsername(context.Context, string) (sqlc.User, error) {
	return f.user, nil
}
func (f *mcpRuntimeFakeQueries) CreateServiceUser(context.Context, sqlc.CreateServiceUserParams) (sqlc.User, error) {
	return sqlc.User{}, errors.New("unexpected create")
}
func (f *mcpRuntimeFakeQueries) GetCharlieAutomationRole(context.Context) (sqlc.GlobalRole, error) {
	return f.role, nil
}
func (f *mcpRuntimeFakeQueries) EnsureCharlieAutomationBinding(context.Context, sqlc.EnsureCharlieAutomationBindingParams) (sqlc.GlobalRoleBinding, error) {
	return sqlc.GlobalRoleBinding{}, nil
}

func mcpRuntimeFixture(t *testing.T) (*MCPRuntime, *mcpRuntimeFakeQueries) {
	t.Helper()
	installationID := uuid.New()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caPEM, _, ca, caKey, err := createLocalCA(now)
	if err != nil {
		t.Fatal(err)
	}
	serverCert, serverKey, err := issueLeaf(ca, caKey, now, "mcp-server", []string{"mcp.test.svc"}, "spiffe://astronomer.local/test/mcp", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	paths := map[string][]byte{
		"tls.crt": []byte(serverCert), "tls.key": []byte(serverKey),
		"ca.crt": []byte(caPEM), "action.key": publicKey,
	}
	for name, value := range paths {
		if err := os.WriteFile(filepath.Join(directory, name), value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	userID := uuid.New()
	queries := &mcpRuntimeFakeQueries{
		connection: sqlc.CharlieConnection{
			ID: uuid.New(), InstallationID: installationID, ProductID: "product-a", ProductSlug: "astronomer", Active: true,
			OnboardingState: "active", RequestedMode: string(ModeReadOnly), VerifiedMode: string(ModeReadOnly),
			SigningKeyFingerprint: digestBytes(publicKey),
		},
		user: sqlc.User{ID: userID, Username: AutomationUsername, IsActive: true, IsService: true},
		role: safeAutomationRole(t),
	}
	bindings := fakeBindings{values: map[uuid.UUID][]rbac.RoleBinding{userID: {}}, active: map[uuid.UUID]bool{userID: true}}
	runtime, err := NewMCPRuntime(MCPRuntimeConfig{
		Listener: MCPListenerConfig{
			Address: "127.0.0.1:0", Certificate: filepath.Join(directory, "tls.crt"),
			PrivateKey: filepath.Join(directory, "tls.key"), ClientCA: filepath.Join(directory, "ca.crt"),
		},
		ActionSigningKeyFile: filepath.Join(directory, "action.key"), LeaseOwner: "server-test",
		ReceiptCipher: receiptTestCipher{},
	}, gateFeature(true), queries, bindings, DenyAutoSafety{}, UnavailableCapabilityExecutor{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, queries
}

func TestMCPRuntimeBindsOnlyWhileLiveAndStopsOnEmergencyDisable(t *testing.T) {
	runtime, queries := mcpRuntimeFixture(t)
	if err := runtime.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	serving := runtime.listener != nil
	runtime.mu.Unlock()
	if !serving {
		t.Fatal("active Charlie integration did not bind the private MCP listener")
	}
	queries.connection.EmergencyDisabled = true
	if err := runtime.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	serving = runtime.listener != nil
	runtime.mu.Unlock()
	if serving {
		t.Fatal("emergency-disabled Charlie integration retained an MCP listener")
	}
}

func TestMCPRuntimeRejectsSigningKeyThatDoesNotMatchOnboarding(t *testing.T) {
	runtime, queries := mcpRuntimeFixture(t)
	queries.connection.SigningKeyFingerprint = digestBytes([]byte("different"))
	if err := runtime.reconcile(context.Background()); err == nil {
		t.Fatal("MCP runtime accepted action signing trust with a different onboarding fingerprint")
	}
	runtime.mu.Lock()
	serving := runtime.listener != nil
	runtime.mu.Unlock()
	if serving {
		t.Fatal("MCP listener bound with invalid signing trust")
	}
}

func TestMCPRuntimeInactiveGateDoesNotResolveMountedTrustOrBind(t *testing.T) {
	runtime, _ := mcpRuntimeFixture(t)
	runtime.features = gateFeature(false)
	if err := os.Remove(runtime.config.ActionSigningKeyFile); err != nil {
		t.Fatal(err)
	}
	if err := runtime.reconcile(context.Background()); err != nil {
		t.Fatalf("inactive runtime touched unavailable trust: %v", err)
	}
	runtime.mu.Lock()
	serving := runtime.listener != nil
	runtime.mu.Unlock()
	if serving {
		t.Fatal("feature-disabled integration bound an MCP listener")
	}
}
