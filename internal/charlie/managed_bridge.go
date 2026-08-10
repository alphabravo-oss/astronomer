package charlie

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type ManagedBridgeConfig struct {
	AgentNamespace string
	Certificate    string
	PrivateKey     string
	ServerCA       string
	SigningKey     string
}

// ManagedBridge is a dormant-by-default proxy to the fixed cluster-local
// Product Bridge. It creates no transport until a request observes the feature
// and connection as live, discards the transport immediately on a gate or trust
// change, and can never accept a Charlie Central URL.
type ManagedBridge struct {
	config   ManagedBridgeConfig
	features featureReader
	queries  activeConnectionReader

	mu                        sync.Mutex
	bridge                    *RuntimeBridge
	runtime                   *contract.Runtime
	connectionID              uuid.UUID
	material                  string
	configurationBridge       *RuntimeBridge
	configurationRuntime      *contract.Runtime
	configurationConnectionID uuid.UUID
	configurationMaterial     string
	activationChanged         func(context.Context)
}

// SetActivationChanged connects signed mode mutations to the process runtime
// owner. The callback is invoked only after local durable authority changes.
func (m *ManagedBridge) SetActivationChanged(changed func(context.Context)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activationChanged = changed
}

func (m *ManagedBridge) notifyActivationChanged(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	changed := m.activationChanged
	m.mu.Unlock()
	if changed != nil {
		changed(ctx)
	}
}

func (b *managedModeBridge) activationChanged(ctx context.Context) {
	if b != nil && b.bridge != nil {
		b.bridge.notifyActivationChanged(ctx)
	}
}

func NewManagedBridge(config ManagedBridgeConfig, features featureReader, queries activeConnectionReader) (*ManagedBridge, error) {
	if strings.TrimSpace(config.AgentNamespace) == "" || strings.TrimSpace(config.Certificate) == "" || strings.TrimSpace(config.PrivateKey) == "" || strings.TrimSpace(config.ServerCA) == "" || strings.TrimSpace(config.SigningKey) == "" || features == nil || queries == nil {
		return nil, fmt.Errorf("Charlie Product Bridge configuration is incomplete")
	}
	return &ManagedBridge{config: config, features: features, queries: queries}, nil
}

func (m *ManagedBridge) Active(ctx context.Context) bool {
	return EvaluateActivation(ctx, m.features, m.queries).Runnable
}

func (m *ManagedBridge) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
}

func (m *ManagedBridge) runtimeBridge(ctx context.Context) (*RuntimeBridge, error) {
	activation := EvaluateActivation(ctx, m.features, m.queries)
	if !activation.Runnable {
		m.notifyActivationChanged(ctx)
		m.Close()
		return nil, fmt.Errorf("Charlie runtime is inactive")
	}
	digest, err := bridgeMaterialDigest(m.config)
	if err != nil {
		m.Close()
		return nil, fmt.Errorf("Charlie Product Bridge trust is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bridge != nil && m.connectionID == activation.Connection.ID && m.material == digest {
		return m.bridge, nil
	}
	m.closeLocked()
	tlsConfig, err := loadBridgeTLS(m.config)
	if err != nil {
		return nil, err
	}
	identities, err := ExpectedLocalIdentityURIs(activation.Connection.InstallationID.String())
	if err != nil {
		return nil, err
	}
	connectionID := activation.Connection.ID
	runtime, err := contract.NewRuntimeWithClientTLS(managedBridgeAvailability{allowed: true}, m.config.AgentNamespace, identities.BridgeServer, tlsConfig, func() bool {
		current := EvaluateActivation(context.Background(), m.features, m.queries)
		return current.Runnable && current.Connection.ID == connectionID
	})
	if err != nil {
		return nil, fmt.Errorf("Charlie Product Bridge is unavailable")
	}
	bridge, err := NewRuntimeBridge(runtime)
	if err != nil {
		runtime.Close()
		return nil, err
	}
	m.runtime, m.bridge, m.connectionID, m.material = runtime, bridge, connectionID, digest
	return bridge, nil
}

func (m *ManagedBridge) closeLocked() {
	if m.runtime != nil {
		m.runtime.Close()
	}
	if m.configurationRuntime != nil {
		m.configurationRuntime.Close()
	}
	m.runtime, m.bridge, m.connectionID, m.material = nil, nil, uuid.Nil, ""
	m.configurationRuntime, m.configurationBridge = nil, nil
	m.configurationConnectionID, m.configurationMaterial = uuid.Nil, ""
}

func (m *ManagedBridge) configurationConnection(ctx context.Context) (sqlc.CharlieConnection, bool) {
	if m == nil {
		return sqlc.CharlieConnection{}, false
	}
	activation := EvaluateActivation(ctx, m.features, m.queries)
	if !activation.Configurable {
		m.notifyActivationChanged(ctx)
		return sqlc.CharlieConnection{}, false
	}
	connection := activation.Connection
	if connection.OnboardingState != "active" ||
		connection.HealthState == "disconnected" || strings.TrimSpace(connection.LocalTrustMaterialEncrypted) == "" {
		return sqlc.CharlieConnection{}, false
	}
	return connection, true
}

// configurationRuntimeBridge is deliberately distinct from runtimeBridge:
// health, activation, and mode must remain reachable while Charlie is disabled,
// but this transport cannot be used for sessions, evidence, findings, or tools.
func (m *ManagedBridge) configurationRuntimeBridge(ctx context.Context) (*RuntimeBridge, error) {
	connection, ok := m.configurationConnection(ctx)
	if !ok {
		m.Close()
		return nil, fmt.Errorf("Charlie configuration transport is inactive")
	}
	digest, err := bridgeMaterialDigest(m.config)
	if err != nil {
		m.Close()
		return nil, fmt.Errorf("Charlie Product Bridge trust is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configurationBridge != nil && m.configurationConnectionID == connection.ID && m.configurationMaterial == digest {
		return m.configurationBridge, nil
	}
	if m.configurationRuntime != nil {
		m.configurationRuntime.Close()
	}
	tlsConfig, err := loadBridgeTLS(m.config)
	if err != nil {
		return nil, err
	}
	identities, err := ExpectedLocalIdentityURIs(connection.InstallationID.String())
	if err != nil {
		return nil, err
	}
	connectionID := connection.ID
	runtime, err := contract.NewConfigurationRuntimeWithClientTLS(AvailabilityAvailableInactive, m.config.AgentNamespace, identities.BridgeServer, tlsConfig, func() bool {
		current, configured := m.configurationConnection(context.Background())
		return configured && current.ID == connectionID
	})
	if err != nil {
		return nil, fmt.Errorf("Charlie Product Bridge configuration is unavailable")
	}
	bridge, err := NewRuntimeBridge(runtime)
	if err != nil {
		runtime.Close()
		return nil, err
	}
	m.configurationRuntime, m.configurationBridge = runtime, bridge
	m.configurationConnectionID, m.configurationMaterial = connectionID, digest
	return bridge, nil
}

type managedBridgeAvailability struct{ allowed bool }

func (a managedBridgeAvailability) AllowsConfiguration() bool { return a.allowed }
func (a managedBridgeAvailability) AllowsRuntime() bool       { return a.allowed }

func loadBridgeTLS(config ManagedBridgeConfig) (*tls.Config, error) {
	pair, err := tls.LoadX509KeyPair(config.Certificate, config.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("Charlie Product Bridge client identity is unavailable")
	}
	caPEM, err := os.ReadFile(config.ServerCA)
	if err != nil {
		return nil, fmt.Errorf("Charlie Product Bridge CA is unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("Charlie Product Bridge CA is invalid")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{pair}}, nil
}

func bridgeMaterialDigest(config ManagedBridgeConfig) (string, error) {
	hash := sha256.New()
	for _, path := range []string{config.Certificate, config.PrivateKey, config.ServerCA, config.SigningKey} {
		value, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(value)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func withBridge[T any](m *ManagedBridge, ctx context.Context, name string, fn func(*RuntimeBridge) (T, error)) (result T, err error) {
	started := time.Now()
	defer func() { observeBridgeCall(name, started, err) }()
	bridge, err := m.runtimeBridge(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return fn(bridge)
}

func withBridgeErr(m *ManagedBridge, ctx context.Context, name string, fn func(*RuntimeBridge) error) (err error) {
	started := time.Now()
	defer func() { observeBridgeCall(name, started, err) }()
	bridge, err := m.runtimeBridge(ctx)
	if err != nil {
		return err
	}
	return fn(bridge)
}

func (m *ManagedBridge) CreateSession(ctx context.Context, input BridgeSessionRequest, key string) (BridgeSessionReceipt, error) {
	return withBridge(m, ctx, "create_session", func(bridge *RuntimeBridge) (BridgeSessionReceipt, error) {
		return bridge.CreateSession(ctx, input, key)
	})
}

func (m *ManagedBridge) GetSession(ctx context.Context, sessionID, authorizationRef string) (json.RawMessage, error) {
	return withBridge(m, ctx, "get_session", func(bridge *RuntimeBridge) (json.RawMessage, error) {
		return bridge.GetSession(ctx, sessionID, authorizationRef)
	})
}

func (m *ManagedBridge) ListFindings(ctx context.Context, authorizationRef string) ([]BridgeFindingSummary, error) {
	return withBridge(m, ctx, "list_findings", func(bridge *RuntimeBridge) ([]BridgeFindingSummary, error) {
		return bridge.ListFindings(ctx, authorizationRef)
	})
}

func (m *ManagedBridge) GetHistory(ctx context.Context, sessionID, authorizationRef, cursor string, limit int) (json.RawMessage, error) {
	return withBridge(m, ctx, "get_history", func(bridge *RuntimeBridge) (json.RawMessage, error) {
		return bridge.GetHistory(ctx, sessionID, authorizationRef, cursor, limit)
	})
}

func (m *ManagedBridge) CreateMessage(ctx context.Context, sessionID, authorizationRef string, messageID uuid.UUID, message string) (json.RawMessage, error) {
	return withBridge(m, ctx, "create_message", func(bridge *RuntimeBridge) (json.RawMessage, error) {
		return bridge.CreateMessage(ctx, sessionID, authorizationRef, messageID, message)
	})
}

func (m *ManagedBridge) AbortSession(ctx context.Context, sessionID, authorizationRef string, requestID uuid.UUID) error {
	return withBridgeErr(m, ctx, "abort_session", func(bridge *RuntimeBridge) error {
		return bridge.AbortSession(ctx, sessionID, authorizationRef, requestID)
	})
}

func (m *ManagedBridge) StreamSessionEvents(ctx context.Context, sessionID, authorizationRef, lastEventID string, handle func(contract.Event) error) error {
	return withBridgeErr(m, ctx, "stream_events", func(bridge *RuntimeBridge) error {
		return bridge.StreamSessionEvents(ctx, sessionID, authorizationRef, lastEventID, handle)
	})
}

func (m *ManagedBridge) ListApprovals(ctx context.Context, authorizationRef string) ([]contract.Approval, error) {
	return withBridge(m, ctx, "list_approvals", func(bridge *RuntimeBridge) ([]contract.Approval, error) {
		return bridge.ListApprovals(ctx, authorizationRef)
	})
}

func (m *ManagedBridge) DecideApproval(ctx context.Context, approvalID, authorizationRef string, input BridgeApprovalDecision) (contract.Approval, error) {
	return withBridge(m, ctx, "decide_approval", func(bridge *RuntimeBridge) (contract.Approval, error) {
		return bridge.DecideApproval(ctx, approvalID, authorizationRef, input)
	})
}

func (m *ManagedBridge) CreateInvestigation(ctx context.Context, request BridgeInvestigationRequest, key string) (BridgeSessionReceipt, error) {
	return withBridge(m, ctx, "create_investigation", func(bridge *RuntimeBridge) (BridgeSessionReceipt, error) {
		return bridge.CreateInvestigation(ctx, request, key)
	})
}

func (m *ManagedBridge) GetFinding(ctx context.Context, findingID, authorizationRef string) (FindingAdvisoryDetail, error) {
	return withBridge(m, ctx, "get_finding", func(bridge *RuntimeBridge) (FindingAdvisoryDetail, error) {
		return bridge.GetFinding(ctx, findingID, authorizationRef)
	})
}

func (m *ManagedBridge) GetFindingScope(ctx context.Context, findingID, authorizationRef string) (BridgeFindingScope, error) {
	return withBridge(m, ctx, "get_finding_scope", func(bridge *RuntimeBridge) (BridgeFindingScope, error) {
		return bridge.GetFindingScope(ctx, findingID, authorizationRef)
	})
}

func (m *ManagedBridge) TransitionFinding(ctx context.Context, findingID, authorizationRef string, requestID uuid.UUID, transition, actorRef string) (BridgeFindingSummary, error) {
	return withBridge(m, ctx, "transition_finding", func(bridge *RuntimeBridge) (BridgeFindingSummary, error) {
		return bridge.TransitionFinding(ctx, findingID, authorizationRef, requestID, transition, actorRef)
	})
}
