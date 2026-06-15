package observability

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const instanceIDLabel = "astronomer_instance_id"

var (
	instanceMu sync.RWMutex
	instanceID = "unknown"
)

type platformConfigQuerier interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	UpsertPlatformConfig(ctx context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error)
}

func SetInstanceID(id string) {
	if id == "" {
		return
	}
	instanceMu.Lock()
	instanceID = id
	instanceMu.Unlock()
}

func InstanceID() string {
	instanceMu.RLock()
	defer instanceMu.RUnlock()
	return instanceID
}

func MetricLabels(labels ...string) []string {
	return append([]string{instanceIDLabel}, labels...)
}

func MetricValues(values ...string) []string {
	return append([]string{InstanceID()}, values...)
}

func Logger(log *slog.Logger) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	return log.With(instanceIDLabel, InstanceID())
}

func EnsureInstanceID(ctx context.Context, q platformConfigQuerier) (string, error) {
	if q == nil {
		return InstanceID(), nil
	}

	cfg, err := q.GetPlatformConfig(ctx)
	switch {
	case err == nil:
		if cfg.InstanceID != uuid.Nil {
			SetInstanceID(cfg.InstanceID.String())
			return cfg.InstanceID.String(), nil
		}
	case errors.Is(err, pgx.ErrNoRows):
		cfg = sqlc.PlatformConfiguration{
			PlatformName: "Astronomer",
		}
	default:
		return "", err
	}

	newID := uuid.New()
	updated, err := q.UpsertPlatformConfig(ctx, sqlc.UpsertPlatformConfigParams{
		ServerUrl:        cfg.ServerUrl,
		PlatformName:     cmp.Or(cfg.PlatformName, "Astronomer"),
		TelemetryEnabled: cfg.TelemetryEnabled,
		BootstrappedAt:   cfg.BootstrappedAt,
		InstanceID:       newID,
	})
	if err != nil {
		return "", err
	}
	SetInstanceID(updated.InstanceID.String())
	return updated.InstanceID.String(), nil
}

func EmptyTimestamp() pgtype.Timestamptz {
	return pgtype.Timestamptz{}
}
