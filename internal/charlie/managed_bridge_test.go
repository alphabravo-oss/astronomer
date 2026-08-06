package charlie

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

func TestManagedBridgeCreatesNoTransportUntilRuntimeIsLiveAndDropsItOnDisable(t *testing.T) {
	installationID := uuid.New()
	connection := sqlc.CharlieConnection{ID: uuid.New(), InstallationID: installationID, Active: true, OnboardingState: "active", RequestedMode: "read_only", VerifiedMode: "read_only"}
	features := gateFeature(false)
	connections := &mutableBridgeConnection{row: connection}
	bridge, err := NewManagedBridge(ManagedBridgeConfig{AgentNamespace: "astronomer-charlie", Certificate: "/does/not/exist", PrivateKey: "/does/not/exist", ServerCA: "/does/not/exist", SigningKey: "/does/not/exist"}, features, connections)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.runtimeBridge(context.Background()); err == nil || bridge.runtime != nil {
		t.Fatal("disabled bridge read trust files or constructed a transport")
	}

	key, _ := auth.GenerateKey()
	encryptor, _ := auth.NewEncryptor(key)
	trust, err := GenerateLocalTrust(encryptor, LocalTrustConfig{InstallationID: installationID.String(), BridgeServerDNS: "charlie-agent-bridge.astronomer-charlie.svc", MCPServerDNS: "astronomer-charlie-mcp.astronomer.svc"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	paths := ManagedBridgeConfig{AgentNamespace: "astronomer-charlie", Certificate: filepath.Join(dir, "tls.crt"), PrivateKey: filepath.Join(dir, "tls.key"), ServerCA: filepath.Join(dir, "ca.crt"), SigningKey: filepath.Join(dir, "signing.pub")}
	for path, content := range map[string]string{paths.Certificate: trust.Public.BridgeClientCertificate, paths.PrivateKey: trust.Astronomer.BridgeClientPrivateKey, paths.ServerCA: trust.Public.CACertificatePEM, paths.SigningKey: strings.Repeat("k", ed25519.PublicKeySize)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bridge, _ = NewManagedBridge(paths, gateFeature(true), connections)
	first, err := bridge.runtimeBridge(context.Background())
	if err != nil || first == nil || bridge.runtime == nil || bridge.connectionID != connection.ID {
		t.Fatalf("live bridge not constructed: bridge=%#v err=%v", bridge, err)
	}
	connections.row.EmergencyDisabled = true
	if _, err := bridge.runtimeBridge(context.Background()); err == nil || bridge.runtime != nil || bridge.bridge != nil {
		t.Fatal("emergency disable retained Product Bridge transport")
	}
}

type mutableBridgeConnection struct{ row sqlc.CharlieConnection }

func (m *mutableBridgeConnection) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return m.row, nil
}

func (m *mutableBridgeConnection) GetLatestCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return m.row, nil
}

func TestManagedBridgeRejectsPartialConfiguration(t *testing.T) {
	if _, err := NewManagedBridge(ManagedBridgeConfig{AgentNamespace: "astronomer-charlie", Certificate: "cert"}, gateFeature(true), &mutableBridgeConnection{}); err == nil {
		t.Fatal("partial Product Bridge configuration was accepted")
	}
}

func TestManagedBridgeOperationalDisabledPermitsOnlyConfigurationPlane(t *testing.T) {
	connection := sqlc.CharlieConnection{
		ID: uuid.New(), InstallationID: uuid.New(), Active: true, OnboardingState: "active",
		RequestedMode: string(ModeDisabled), VerifiedMode: string(ModeDisabled),
		LocalTrustMaterialEncrypted: "present",
	}
	connections := &mutableBridgeConnection{row: connection}
	bridge, err := NewManagedBridge(ManagedBridgeConfig{
		AgentNamespace: "astronomer-charlie", Certificate: "/does/not/exist", PrivateKey: "/does/not/exist",
		ServerCA: "/does/not/exist", SigningKey: "/does/not/exist",
	}, gateFeature(true), connections)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.runtimeBridge(context.Background()); err == nil || bridge.runtime != nil {
		t.Fatal("wire-disabled integration constructed an intelligence/work transport")
	}
	if configured, ok := bridge.configurationConnection(context.Background()); !ok || configured.ID != connection.ID {
		t.Fatal("wire-disabled enabled connection lost its signed configuration control plane")
	}
	connections.row.Active = false
	if _, ok := bridge.configurationConnection(context.Background()); ok {
		t.Fatal("inactive connection retained a configuration network path")
	}
}
