package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestShutdownHooksRunInRegistrationOrderAndAggregateErrors(t *testing.T) {
	srv := New(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var order []string
	srv.AddShutdownHook("audit", func(context.Context) error {
		order = append(order, "audit")
		return errors.New("flush failed")
	})
	srv.AddShutdownHook("telemetry", func(context.Context) error {
		order = append(order, "telemetry")
		return nil
	})

	err := srv.Shutdown(t.Context())
	if !reflect.DeepEqual(order, []string{"audit", "telemetry"}) {
		t.Fatalf("hook order = %v", order)
	}
	if err == nil || !strings.Contains(err.Error(), "audit: flush failed") {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestAddShutdownHookIgnoresNil(t *testing.T) {
	srv := New(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.AddShutdownHook("nil", nil)
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
