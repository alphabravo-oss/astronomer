package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Ported from internal/observability (the former slog JSON handler).

func testSlog(w io.Writer, level string) *slog.Logger {
	logger, err := NewWithWriter(w, Config{Level: level})
	if err != nil {
		panic(err)
	}
	return Slog(logger)
}

func TestSlogRedactsSensitiveDiagnostics(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := testSlog(&output, "").With("operation_id", "safe-operation", "actor_id", "safe-actor")
	logger.WithGroup("credentials").Info("rejected", "name", "synthetic-group-secret")
	logger.Info("Bearer synthetic-bearer rejected", "cookie", "synthetic-cookie", "manifest", "synthetic-manifest", "private_key", "synthetic-private-key", "payload", map[string]any{"value": "synthetic-payload"}, "err", errors.New("postgres://user:synthetic-db-secret@localhost/db\npassword=synthetic-error-secret\n-----BEGIN PRIVATE KEY-----\nsynthetic-pem\n-----END PRIVATE KEY-----"), "unknown_object", struct{ Value string }{"synthetic-object"}, "token_kind", "refresh", "state", "failed")
	raw := output.String()
	for _, secret := range []string{"synthetic-group-secret", "synthetic-bearer", "synthetic-cookie", "synthetic-manifest", "synthetic-private-key", "synthetic-payload", "synthetic-db-secret", "synthetic-error-secret", "synthetic-pem", "synthetic-object"} {
		if strings.Contains(raw, secret) {
			t.Errorf("sensitive diagnostic escaped: %s", secret)
		}
	}
	for _, safe := range []string{"safe-operation", "safe-actor", "refresh", "failed"} {
		if !strings.Contains(raw, safe) {
			t.Errorf("safe correlation/category lost: %s", safe)
		}
	}
}

func TestWrappedDatabaseAndKubernetesErrorsOmitServerPayload(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := testSlog(&output, "")
	logger.Error("write failed", "err", fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505", Message: "synthetic-pg-message", Detail: "Key(token)=(synthetic-pg-detail) already exists", Where: "synthetic-pg-where"}))
	logger.Error("apply failed", "err", fmt.Errorf("wrapped: %w", &apierrors.StatusError{ErrStatus: metav1.Status{Code: 422, Reason: metav1.StatusReasonInvalid, Message: "synthetic-kube-manifest", Details: &metav1.StatusDetails{Name: "synthetic-kube-details"}}}))
	for _, secret := range []string{"synthetic-pg-message", "synthetic-pg-detail", "synthetic-pg-where", "synthetic-kube-manifest", "synthetic-kube-details"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("server payload exported: %s", secret)
		}
	}
	for _, safe := range []string{"23505", "422", "postgres", "kubernetes", "Invalid"} {
		if !strings.Contains(output.String(), safe) {
			t.Errorf("safe error category absent: %s", safe)
		}
	}
}

func TestSlogPreservesSeverity(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := testSlog(&output, "debug")
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		logger.Log(context.Background(), level, "safe diagnostic")
		if !strings.Contains(output.String(), `"level":"`+strings.ToLower(level.String())+`"`) {
			t.Errorf("missing severity %s: %s", level, output.String())
		}
	}
}
