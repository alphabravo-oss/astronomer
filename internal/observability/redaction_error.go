package observability

import (
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func diagnosticError(err error) slog.Value {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		// Message, Detail, Where and SQL can echo user-supplied literals.
		// Preserve the server error category without those fields.
		return slog.GroupValue(slog.String("type", "postgres"), slog.String("sqlstate", postgres.Code))
	}
	var kubernetes *apierrors.StatusError
	if errors.As(err, &kubernetes) {
		// Status.Message/Details can embed rejected Secret manifests.
		return slog.GroupValue(slog.String("type", "kubernetes"), slog.Int("http_status", int(kubernetes.ErrStatus.Code)), slog.String("reason", diagnosticKubernetesReason(string(kubernetes.ErrStatus.Reason))))
	}
	return slog.StringValue(ScrubDiagnosticText(err.Error()))
}

func diagnosticKubernetesReason(reason string) string {
	switch reason {
	case "Unauthorized", "Forbidden", "NotFound", "AlreadyExists", "Conflict", "Invalid", "Expired", "Gone", "ServerTimeout", "Timeout", "TooManyRequests", "BadRequest", "MethodNotAllowed", "NotAcceptable", "UnsupportedMediaType", "InternalError", "ServiceUnavailable", "RequestEntityTooLarge":
		return reason
	default:
		return "Unknown"
	}
}
