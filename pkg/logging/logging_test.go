package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"
)

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("line is not one JSON object: %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestNewWithWriterJSONDefaults(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, err := NewWithWriter(&buf, Config{Component: "server"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug().Msg("hidden")
	logger.Info().Str("route", "/x").Msg("visible")
	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("want 1 line at default info level, got %d: %s", len(lines), buf.String())
	}
	if lines[0]["component"] != "server" || lines[0]["level"] != "info" || lines[0]["message"] != "visible" || lines[0]["time"] == nil {
		t.Fatalf("unexpected line: %v", lines[0])
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	if _, err := NewWithWriter(&bytes.Buffer{}, Config{Level: "loud"}); err == nil {
		t.Fatal("expected invalid level error")
	}
	if _, err := NewWithWriter(&bytes.Buffer{}, Config{Format: "xml"}); err == nil {
		t.Fatal("expected invalid format error")
	}
	level, err := ParseLevel("WARNING")
	if err != nil || level != zerolog.WarnLevel {
		t.Fatalf("ParseLevel(WARNING) = %v, %v", level, err)
	}
}

func TestTextFormatUsesConsoleWriterAndStillRedacts(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, err := NewWithWriter(&buf, Config{Format: "text", Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug().Str("password", "hunter2").Msg("console line")
	out := buf.String()
	if strings.HasPrefix(out, "{") || !strings.Contains(out, "console line") || strings.Contains(out, "hunter2") {
		t.Fatalf("unexpected console output: %q", out)
	}
}

func TestRedactionPolicy(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, _ := NewWithWriter(&buf, Config{})
	logger.Info().
		Str("password", "p1").
		Dict("request_body", zerolog.Dict().Str("x", "p2")).
		Str("detail", "Authorization: Bearer abc.def").
		Str("dsn", "postgres://user:p3@db/x").
		Err(&pgconn.PgError{Code: "23505", Message: "duplicate key value p4"}).
		Msg("token=p5 failed")
	out := buf.String()
	for _, secret := range []string{"p1", "p2", "abc.def", "p3", "p4", "p5"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked: %s", secret, out)
		}
	}
	line := decodeLines(t, &buf)[0]
	if errField, _ := line["error"].(map[string]any); errField["sqlstate"] != "23505" {
		t.Fatalf("postgres error classification missing: %v", line["error"])
	}
}

func TestSlogBridge(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, _ := NewWithWriter(&buf, Config{Component: "worker"})
	log := slog.New(SlogHandler(logger))
	ctx := WithCorrelationID(context.Background(), "corr-1")
	type opaque struct{ Secret string }
	log.With("cluster_id", "c1").WithGroup("credentials").Info("ignored-group", "user", "u")
	log.InfoContext(ctx, "bridged", "count", 3, "err", errors.New("token=abc"), "obj", opaque{Secret: "zzz"}, slog.Group("g", "token", "t1", "ok", true))
	log.Debug("below level")
	lines := decodeLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %s", len(lines), buf.String())
	}
	if lines[0]["credentials.user"] != Redacted || lines[0]["cluster_id"] != "c1" {
		t.Fatalf("sensitive group not redacted: %v", lines[0])
	}
	second := lines[1]
	if second["component"] != "worker" || second["correlation_id"] != "corr-1" || second["message"] != "bridged" {
		t.Fatalf("bridge fields: %v", second)
	}
	if second["obj"] != omittedStructured || second["g.token"] != Redacted || second["g.ok"] != true {
		t.Fatalf("structured policy: %v", second)
	}
	if strings.Contains(buf.String(), "abc") || strings.Contains(buf.String(), "zzz") {
		t.Fatalf("secret leaked: %s", buf.String())
	}
}

func TestCtxAddsCorrelation(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	base, _ := NewWithWriter(&buf, Config{Component: "server"})
	Ctx(WithCorrelationID(context.Background(), "abc"), base).Info().Msg("x")
	line := decodeLines(t, &buf)[0]
	if line["correlation_id"] != "abc" || line["component"] != "server" {
		t.Fatalf("Ctx fields: %v", line)
	}
	if CorrelationID(context.Background()) != "" {
		t.Fatal("empty ctx must have no correlation id")
	}
}

func TestInboundCorrelationID(t *testing.T) {
	t.Parallel()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	cases := []struct {
		name   string
		header http.Header
		want   string
		ok     bool
	}{
		{"traceparent wins", http.Header{"Traceparent": {"00-" + traceID + "-00f067aa0ba902b7-01"}, "X-Correlation-Id": {"c"}}, traceID, true},
		{"invalid traceparent falls back", http.Header{"Traceparent": {"00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01"}, "X-Correlation-Id": {"c"}}, "c", true},
		{"uppercase traceparent rejected", http.Header{"Traceparent": {"00-" + strings.ToUpper(traceID) + "-00f067aa0ba902b7-01"}}, "", false},
		{"request id fallback", http.Header{"X-Request-Id": {"r"}}, "r", true},
		{"control chars rejected", http.Header{"X-Correlation-Id": {"a\nb"}}, "", false},
		{"none", http.Header{}, "", false},
	}
	for _, tc := range cases {
		got, ok := InboundCorrelationID(tc.header)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got %q,%v want %q,%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSetDefaultRoutesSlog(t *testing.T) {
	// Mutates process globals; intentionally not parallel.
	previous := Default()
	t.Cleanup(func() { SetDefault(*previous) })
	var buf bytes.Buffer
	logger, _ := NewWithWriter(&buf, Config{Component: "cli"})
	SetDefault(logger)
	slog.Info("through default")
	if lines := decodeLines(t, &buf); len(lines) != 1 || lines[0]["component"] != "cli" {
		t.Fatalf("slog default not routed: %s", buf.String())
	}
}
