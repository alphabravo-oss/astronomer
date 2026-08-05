package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FeatureAgentLifecycle interface {
	Suspend(context.Context, AgentInstallSpec) error
	Resume(context.Context, AgentInstallSpec) error
}

type RuntimeShutdown interface{ Shutdown(context.Context) error }

// FeatureLifecycle turns feature.charlie into a runtime lifecycle boundary,
// not merely an HTTP route flag.
type FeatureLifecycle struct {
	queries   *sqlc.Queries
	installer FeatureAgentLifecycle
	mode      *ModeController
	runtime   RuntimeShutdown
	writes    *WriteFence
	timeout   time.Duration
}

func NewFeatureLifecycle(pool *pgxpool.Pool, installer FeatureAgentLifecycle, bridge *ManagedBridge, runtime RuntimeShutdown, writes *WriteFence) (*FeatureLifecycle, error) {
	if pool == nil || writes == nil {
		return nil, fmt.Errorf("Charlie feature lifecycle requires durable state and write admission")
	}
	var mode *ModeController
	if bridge != nil {
		var err error
		mode, err = NewModeController(PGModeStore{Pool: pool}, NewManagedModeBridge(bridge))
		if err != nil {
			return nil, err
		}
		mode.SetWriteFence(writes)
	}
	return &FeatureLifecycle{queries: sqlc.New(pool), installer: installer, mode: mode, runtime: runtime, writes: writes, timeout: 30 * time.Second}, nil
}

func (l *FeatureLifecycle) Disable(parent context.Context, actorID string) error {
	if l == nil || l.queries == nil || l.writes == nil {
		return fmt.Errorf("Charlie feature disable is unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, l.timeout)
	defer cancel()
	l.writes.Close()
	if l.runtime != nil {
		if err := l.runtime.Shutdown(ctx); err != nil {
			return fmt.Errorf("stop Charlie MCP listener: %w", err)
		}
	}
	connection, err := l.queries.GetLatestCharlieConnection(ctx)
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
	if connection.HealthState == "inactive" || connection.HealthState == "disconnected" {
		return nil
	}
	if l.installer == nil {
		return fmt.Errorf("Charlie agent suspension is unavailable")
	}
	if err := l.installer.Suspend(ctx, adminInstallSpec(connection)); err != nil {
		return fmt.Errorf("suspend Charlie agent runtime: %w", err)
	}
	return nil
}

func (l *FeatureLifecycle) Enable(parent context.Context, _ string) error {
	if l == nil || l.queries == nil {
		return fmt.Errorf("Charlie feature enable is unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, l.timeout)
	defer cancel()
	connection, err := l.queries.GetLatestCharlieConnection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		// A fresh feature enable is valid before onboarding; no runtime exists to
		// restore and the write fence remains closed until activation.
		return nil
	}
	if err != nil {
		return fmt.Errorf("load Charlie connection for enable: %w", err)
	}
	if connection.HealthState != "inactive" {
		return nil
	}
	if l.installer == nil {
		return fmt.Errorf("Charlie agent resume is unavailable")
	}
	if err := l.installer.Resume(ctx, adminInstallSpec(connection)); err != nil {
		return fmt.Errorf("resume Charlie agent runtime: %w", err)
	}
	return nil
}
