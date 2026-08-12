package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type mcpRuntimeQueries interface {
	activeConnectionReader
	liveAuthorityQueries
	actionReceiptQueries
	automationIdentityQuerier
	actionAuditQueries
	ambiguousReceiptQueries
}

type MCPRuntimeConfig struct {
	Listener             MCPListenerConfig
	ActionSigningKeyFile string
	LeaseOwner           string
	ReceiptCipher        actionReceiptCipher
	WriteFence           *WriteFence
	BridgeStatus         adminBridgeStatusReader
	FindingRecorder      BlockedFindingRecorder
	PollInterval         time.Duration
}

// MCPRuntime owns the private listener lifecycle. An installed non-emergency
// connection may bind a configuration-only discovery surface while operational
// authority is disabled; tools/call is independently live-gated on every
// request before its identity or body is resolved.
type MCPRuntime struct {
	config   MCPRuntimeConfig
	features featureReader
	queries  mcpRuntimeQueries
	bindings LiveBindingResolver
	safety   LiveActionSafety
	executor CapabilityExecutor
	receipts *AmbiguousReceiptReconciler
	logger   *slog.Logger

	lifecycle  sync.Mutex
	mu         sync.Mutex
	listener   *MCPListener
	connection string
	material   string
	ticker     func(time.Duration) runtimeTicker
}

func NewMCPRuntime(config MCPRuntimeConfig, features featureReader, queries mcpRuntimeQueries, bindings LiveBindingResolver, safety LiveActionSafety, executor CapabilityExecutor, logger *slog.Logger) (*MCPRuntime, error) {
	if strings.TrimSpace(config.Listener.Address) == "" {
		return nil, nil
	}
	if features == nil || queries == nil || bindings == nil || safety == nil || executor == nil ||
		config.Listener.Certificate == "" || config.Listener.PrivateKey == "" || config.Listener.ClientCA == "" ||
		config.ActionSigningKeyFile == "" || config.ReceiptCipher == nil || config.FindingRecorder == nil || strings.TrimSpace(config.LeaseOwner) == "" || len(config.LeaseOwner) > 128 {
		return nil, fmt.Errorf("Charlie MCP runtime dependencies are incomplete")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.WriteFence == nil {
		config.WriteFence = NewWriteFence()
	}
	runtime := &MCPRuntime{config: config, features: features, queries: queries, bindings: bindings, safety: safety, executor: executor, logger: logger, ticker: newRuntimeTicker}
	reconcileAudit, err := NewDBReceiptReconcileAuditor(queries)
	if err != nil {
		return nil, err
	}
	runtime.receipts, err = NewAmbiguousReceiptReconciler(queries, config.ReceiptCipher, executor, reconcileAudit, config.LeaseOwner, func() bool {
		return EvaluateActivation(context.Background(), features, queries).Runnable
	})
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *MCPRuntime) Run(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if !configurationDiscoveryAllowed(EvaluateActivation(ctx, r.features, r.queries)) {
		return r.Shutdown(ctx)
	}
	ticker := r.ticker(r.config.PollInterval)
	defer ticker.Stop()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Shutdown(shutdownCtx)
	}()
	for {
		if err := r.reconcile(ctx); err != nil {
			LogRuntimeEvent(ctx, r.logger, "inactive_reconciliation_failed")
		}
		activation := EvaluateActivation(ctx, r.features, r.queries)
		// The owning RuntimeLifecycle observes durable authority loss and cancels
		// this generation. Do not exit this inner loop on one unavailable feature
		// or connection read: doing so leaves the outer generation published but
		// with no MCP listener, so later Activate calls incorrectly see work as
		// already running. reconcile has already closed the listener fail-safe;
		// staying alive lets the next successful read restore it while a durable
		// gate fall is still torn down by the outer lifecycle watcher.
		if configurationDiscoveryAllowed(activation) && activation.Runnable && r.receipts != nil {
			if err := r.receipts.RunOnce(ctx); err != nil {
				LogRuntimeEvent(ctx, r.logger, "reconciliation_pending")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
		}
	}
}

func (r *MCPRuntime) reconcile(ctx context.Context) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	activation := EvaluateActivation(ctx, r.features, r.queries)
	if !configurationDiscoveryAllowed(activation) {
		r.config.WriteFence.Close()
		shutdownErr := r.shutdownLocked(ctx)
		_, drainErr := r.config.WriteFence.CloseAndWait(ctx)
		if shutdownErr != nil {
			return shutdownErr
		}
		return drainErr
	}
	// The product agent owns leader election. Mirror its current leader and
	// fencing epoch into product state before admitting MCP work so a failover
	// makes every ticket from the former leader stale within one reconcile
	// interval. A status/read failure leaves the existing guard fail-closed.
	if r.config.BridgeStatus != nil {
		status, statusErr := r.config.BridgeStatus.AdminStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		if writer, ok := r.queries.(agentStatusWriter); ok {
			connection, syncErr := syncAgentStatus(ctx, writer, activation.Connection, status, time.Now().UTC())
			if syncErr != nil {
				return syncErr
			}
			activation.Connection = connection
		}
	}
	effective := EffectiveMode(Mode(activation.Connection.RequestedMode), Mode(activation.Connection.VerifiedMode), activation.Connection.EmergencyDisabled)
	if effective != ModeApproval && effective != ModeAuto {
		r.config.WriteFence.Close()
	}
	// Runtime reconciliation owns listener lifecycle only. Write admission is
	// opened exclusively by ModeController after durable mode state, central
	// readback, and the all-replica workload ceiling are proven together.
	connectionKey := activation.Connection.ID.String()
	material, err := mcpMaterialDigest(r.config)
	if err != nil {
		_ = r.shutdownLocked(ctx)
		return err
	}
	r.mu.Lock()
	alreadyServing := r.listener != nil && r.connection == connectionKey && r.material == material
	hadListener := r.listener != nil
	r.mu.Unlock()
	if alreadyServing {
		return nil
	}
	if err := r.shutdownLocked(ctx); err != nil {
		return err
	}

	publicKey, err := os.ReadFile(r.config.ActionSigningKeyFile)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("load Charlie action signing trust")
	}
	keyDigest := sha256.Sum256(publicKey)
	if hex.EncodeToString(keyDigest[:]) != strings.ToLower(activation.Connection.SigningKeyFingerprint) {
		return fmt.Errorf("Charlie action signing trust does not match onboarding")
	}
	identity, err := EnsureAutomationIdentity(ctx, r.queries)
	if err != nil {
		return err
	}
	authority, err := NewProductLiveAuthorityWithFeatures(r.queries, r.bindings, r.safety, identity.ID, r.features)
	if err != nil {
		return err
	}
	receipts, err := NewDBActionReceiptStore(r.queries, r.config.ReceiptCipher, r.config.LeaseOwner)
	if err != nil {
		return err
	}
	auditor, err := NewDBActionAuditor(r.queries)
	if err != nil {
		return err
	}
	guard, err := NewActionGuard(ed25519.PublicKey(publicKey), authority, receipts, r.executor, auditor)
	if err != nil {
		return err
	}
	guard.SetLogger(r.logger)
	guard.SetWriteFence(r.config.WriteFence)
	guard.SetFindingRecorder(r.config.FindingRecorder, activation.Connection.InstallationID.String())
	identities, err := ExpectedLocalIdentityURIs(activation.Connection.InstallationID.String())
	if err != nil {
		return err
	}
	connectionID := activation.Connection.ID
	expectedFingerprint := activation.Connection.SigningKeyFingerprint
	handler, err := NewMCPHandler(guard, func(callCtx context.Context) bool {
		current := EvaluateActivation(callCtx, r.features, r.queries)
		return configurationDiscoveryAllowed(current) && current.Connection.ID == connectionID &&
			current.Connection.SigningKeyFingerprint == expectedFingerprint
	}, func(callCtx context.Context) bool {
		current := EvaluateActivation(callCtx, r.features, r.queries)
		return current.Runnable && current.Connection.ID == connectionID &&
			current.Connection.SigningKeyFingerprint == expectedFingerprint
	}, identities.MCPClient)
	if err != nil {
		return err
	}
	listener, err := NewMCPListener(r.config.Listener, handler)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.listener = listener
	r.connection = connectionKey
	r.material = material
	r.mu.Unlock()
	go r.serve(listener)
	if hadListener {
		observeMCPListener("restarted")
	} else {
		observeMCPListener("activated")
	}
	LogRuntimeEvent(ctx, r.logger, "activated")
	return nil
}

func (r *MCPRuntime) serve(listener *MCPListener) {
	err := listener.Serve()
	r.mu.Lock()
	if r.listener == listener {
		r.listener = nil
		r.connection = ""
		r.material = ""
	}
	r.mu.Unlock()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		observeMCPListener("serve_failed")
		LogRuntimeEvent(context.Background(), r.logger, "listener_stopped")
	}
}

func (r *MCPRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	return r.shutdownLocked(ctx)
}

func (r *MCPRuntime) shutdownLocked(ctx context.Context) error {
	r.mu.Lock()
	listener := r.listener
	r.listener = nil
	r.connection = ""
	r.material = ""
	r.mu.Unlock()
	if listener == nil {
		return nil
	}
	observeMCPListener("deactivated")
	return listener.Shutdown(ctx)
}

func mcpMaterialDigest(config MCPRuntimeConfig) (string, error) {
	hash := sha256.New()
	for _, path := range []string{config.Listener.Certificate, config.Listener.PrivateKey, config.Listener.ClientCA, config.ActionSigningKeyFile} {
		value, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(value)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// UnavailableCapabilityExecutor is the production-safe A6 seam. A7/A8 replace
// it with reviewed product adapters one capability at a time; it cannot reach a
// tunnel, Kubernetes client, queue, or database mutation on its own.
type UnavailableCapabilityExecutor struct{}

func (UnavailableCapabilityExecutor) Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("Charlie capability adapter is unavailable")
}

func (UnavailableCapabilityExecutor) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return false, fmt.Errorf("Charlie capability adapter is unavailable")
}
