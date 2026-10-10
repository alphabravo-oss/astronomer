package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestSnapshotDiagnosticsRetainTypedCategoryAndOmitServerPayload(t *testing.T) {
	const rawValue = "synthetic-unlabelled-snapshot-value"
	cases := []struct {
		name string
		err error
		category string
	}{
		{name: "postgres", err: &pgconn.PgError{Code: "23505", Message: rawValue}, category: "postgres"},
		{name: "wrapped_postgres", err: fmt.Errorf("snapshot write: %w", &pgconn.PgError{Code: "23505", Message: rawValue}), category: "postgres"},
		{name: "kubernetes", err: &apierrors.StatusError{ErrStatus: metav1.Status{Code: 422, Reason: metav1.StatusReasonInvalid, Message: "Secret rejected: " + rawValue}}, category: "kubernetes"},
		{name: "wrapped_kubernetes", err: fmt.Errorf("snapshot apply: %w", &apierrors.StatusError{ErrStatus: metav1.Status{Code: 422, Reason: metav1.StatusReasonInvalid, Message: "Secret rejected: " + rawValue}}), category: "kubernetes"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(observability.NewJSONHandler(&output, nil)).With("correlation_id", "safe-snapshot-request")
			logSnapshotErr(logger, "poll_snapshot", test.err)
			if strings.Contains(output.String(), rawValue) {
				t.Error("typed server payload escaped shipped snapshot diagnostic handler")
			}
			var row map[string]any
			if err := json.Unmarshal(output.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			category, ok := row["error"].(map[string]any)
			if !ok || category["type"] != test.category || row["phase"] != "poll_snapshot" || row["correlation_id"] != "safe-snapshot-request" {
				t.Fatalf("snapshot category/correlation missing: %#v", row)
			}
		})
	}
}

func TestSnapshotDiagnosticsKeepCancellationSuppressed(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(observability.NewJSONHandler(&output, nil))
	for _, err := range []error{nil, context.Canceled, fmt.Errorf("poll: %w", context.DeadlineExceeded), errors.Join(context.Canceled, &pgconn.PgError{Code: "23505", Message: "synthetic-canceled-row"})} {
		logSnapshotErr(logger, "poll_snapshot", err)
	}
	logSnapshotErr(nil, "poll_snapshot", errors.New("synthetic-nil-logger"))
	if output.Len() != 0 {
		t.Fatal("expected cancellation/nil logger paths to remain silent")
	}
}
