package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FeatureAgentLifecycle interface {
	Suspend(context.Context, AgentInstallSpec) error
	Resume(context.Context, AgentInstallSpec) error
}

type RuntimeShutdown interface{ Shutdown(context.Context) error }
type RuntimeActivator interface{ Activate(context.Context) error }
type emergencyModeDisabler interface {
	EmergencyDisable(context.Context, string) (ModeState, error)
}

// FeatureLifecycle turns feature.charlie into a runtime lifecycle boundary,
// not merely an HTTP route flag.
type FeatureLifecycle struct {
	queries    *sqlc.Queries
	connection func(context.Context) (sqlc.CharlieConnection, error)
	installer  FeatureAgentLifecycle
	mode       emergencyModeDisabler
	runtime    RuntimeShutdown
	writes     *WriteFence
	auditor    AuthorityMutationAuditor
	timeout    time.Duration
}

func NewFeatureLifecycle(pool *pgxpool.Pool, installer FeatureAgentLifecycle, bridge *ManagedBridge, runtime RuntimeShutdown, writes *WriteFence) (*FeatureLifecycle, error) {
	if pool == nil || writes == nil {
		return nil, fmt.Errorf("Charlie feature lifecycle requires durable state and write admission")
	}
	queries := sqlc.New(pool)
	auditor := NewDBLifecycleAuditor(queries)
	var mode *ModeController
	if bridge != nil {
		var err error
		mode, err = NewModeController(PGModeStore{Pool: pool}, NewManagedModeBridge(bridge), auditor)
		if err != nil {
			return nil, err
		}
		if ceilingInstaller, ok := installer.(ModeCeilingInstaller); ok {
			mode.SetModeCeilingRollout(modeCeilingRollout{load: queries.GetLatestCharlieConnection, installer: ceilingInstaller})
		}
		mode.SetWriteFence(writes)
	}
	return &FeatureLifecycle{queries: queries, connection: queries.GetLatestCharlieConnection, installer: installer, mode: mode, runtime: runtime, writes: writes, auditor: auditor, timeout: 30 * time.Second}, nil
}

func (l *FeatureLifecycle) Disable(parent context.Context, actorID string) error {
	if l == nil || l.writes == nil || (l.queries == nil && l.connection == nil) {
		return fmt.Errorf("Charlie feature disable is unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, l.timeout)
	defer cancel()
	l.writes.Close()
	actor, _ := uuid.Parse(actorID)
	_ = requireAuthorityMutationAudit(ctx, l.auditor, AuthorityMutationAudit{
		Action: "charlie.feature.disabled", ResourceType: "charlie_feature", ResourceID: "feature.charlie", ActorID: actor,
		Fields: map[string]any{"enabled": false},
	})
	connection, err := l.latestConnection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = l.writes.CloseAndWait(ctx)
		return err
	}
	if err != nil {
		return fmt.Errorf("load Charlie connection for disable: %w", err)
	}
	if connection.Active {
		if l.mode == nil {
			return fmt.Errorf("Charlie emergency-disable controller is unavailable")
		}
		_, modeErr := l.mode.EmergencyDisable(ctx, actorID)
		if modeErr != nil && !strings.Contains(modeErr.Error(), "remote confirmation is pending") {
			return modeErr
		}
	} else if _, err := l.writes.CloseAndWait(ctx); err != nil {
		return err
	}
	// EmergencyDisable retains the distributed exclusive write fence through
	// its durable latch, all-replica ceiling, and remote readback. Only then may
	// local runtime teardown continue; this avoids a cross-replica admission gap
	// while feature disable is still being committed.
	if l.runtime != nil {
		if err := l.runtime.Shutdown(ctx); err != nil {
			return fmt.Errorf("stop Charlie MCP listener: %w", err)
		}
	}
	if connection.HealthState == "disconnected" {
		return nil
	}
	if l.installer == nil {
		return fmt.Errorf("Charlie agent suspension is unavailable")
	}
	// Repeat suspension for an already-inactive installation. A prior server can
	// have removed the Argo Application and workload before it learned how to
	// delete every chart-owned Service/PDB/NetworkPolicy. Idempotent disable must
	// converge that stale surface to zero instead of trusting metadata alone.
	if err := l.installer.Suspend(ctx, adminInstallSpec(connection)); err != nil {
		return fmt.Errorf("suspend Charlie agent runtime: %w", err)
	}
	return nil
}

func (l *FeatureLifecycle) Enable(parent context.Context, actorID string) error {
	if l == nil {
		return fmt.Errorf("Charlie feature enable is unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, l.timeout)
	defer cancel()
	actor, _ := uuid.Parse(actorID)
	if err := requireAuthorityMutationAudit(ctx, l.auditor, AuthorityMutationAudit{
		Action: "charlie.feature.enabled", ResourceType: "charlie_feature", ResourceID: "feature.charlie", ActorID: actor,
		Fields: map[string]any{"enabled": true},
	}); err != nil {
		return fmt.Errorf("Charlie feature enable audit is unavailable")
	}
	if l.queries == nil && l.connection == nil {
		return fmt.Errorf("Charlie feature enable is unavailable")
	}
	connection, err := l.latestConnection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		// A fresh feature enable is valid before onboarding; no runtime exists to
		// restore and the write fence remains closed until activation.
		return l.activateRuntime(ctx)
	}
	if err != nil {
		return fmt.Errorf("load Charlie connection for enable: %w", err)
	}
	if connection.HealthState != "inactive" {
		return l.activateRuntime(ctx)
	}
	if l.installer == nil {
		return fmt.Errorf("Charlie agent resume is unavailable")
	}
	if err := l.installer.Resume(ctx, adminInstallSpec(connection)); err != nil {
		return fmt.Errorf("resume Charlie agent runtime: %w", err)
	}
	return l.activateRuntime(ctx)
}

func (l *FeatureLifecycle) latestConnection(ctx context.Context) (sqlc.CharlieConnection, error) {
	if l.connection != nil {
		return l.connection(ctx)
	}
	if l.queries == nil {
		return sqlc.CharlieConnection{}, pgx.ErrNoRows
	}
	return l.queries.GetLatestCharlieConnection(ctx)
}

func (l *FeatureLifecycle) activateRuntime(ctx context.Context) error {
	activator, ok := l.runtime.(RuntimeActivator)
	if !ok || activator == nil {
		return nil
	}
	if err := activator.Activate(ctx); err != nil {
		return fmt.Errorf("start Charlie product runtime: %w", err)
	}
	return nil
}
