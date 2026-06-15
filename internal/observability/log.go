package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/strutil"
	"go.opentelemetry.io/otel/trace"
)

func WithEvent(log *slog.Logger, event string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if event == "" {
		return log
	}
	return log.With("event", event)
}

func WithCorrelationID(log *slog.Logger, correlationID string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if correlationID == "" {
		return log
	}
	return log.With("correlation_id", correlationID)
}

func WithRequestID(log *slog.Logger, requestID string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if requestID == "" {
		return log
	}
	return log.With("request_id", requestID)
}

func WithTraceID(log *slog.Logger, ctx context.Context) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.HasTraceID() {
		return log
	}
	return log.With("trace_id", spanContext.TraceID().String())
}

func WithActorID(log *slog.Logger, actorID string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if actorID = strings.TrimSpace(actorID); actorID == "" {
		return log
	}
	return log.With("actor_id", actorID)
}

func WithActorAuthMethod(log *slog.Logger, authMethod string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if authMethod = strings.TrimSpace(authMethod); authMethod == "" {
		return log
	}
	return log.With("actor_auth_method", authMethod)
}

func WithClusterID(log *slog.Logger, clusterID string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if clusterID = strings.TrimSpace(clusterID); clusterID == "" {
		return log
	}
	return log.With("cluster_id", clusterID)
}

func WithOperationID(log *slog.Logger, operationID string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if operationID = strings.TrimSpace(operationID); operationID == "" {
		return log
	}
	return log.With("operation_id", operationID)
}

type LogIdentifiers struct {
	ActorID     string
	ClusterID   string
	OperationID string
}

func WithLogIdentifiers(log *slog.Logger, ids LogIdentifiers) *slog.Logger {
	log = WithActorID(log, ids.ActorID)
	log = WithClusterID(log, ids.ClusterID)
	return WithOperationID(log, ids.OperationID)
}

// ExtractLogIdentifiers pulls the common low-cardinality identifiers from a
// worker payload. It intentionally ignores arbitrary user_id fields because
// those are often operation targets, not the actor.
func ExtractLogIdentifiers(payload []byte) LogIdentifiers {
	if len(payload) == 0 {
		return LogIdentifiers{}
	}
	var probe struct {
		ActorID     string `json:"actor_id"`
		ActorUserID string `json:"actor_user_id"`
		ClusterID   string `json:"cluster_id"`
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return LogIdentifiers{}
	}
	return LogIdentifiers{
		ActorID:     strutil.FirstNonBlankTrimmed(probe.ActorID, probe.ActorUserID),
		ClusterID:   strings.TrimSpace(probe.ClusterID),
		OperationID: strings.TrimSpace(probe.OperationID),
	}
}
