package charlie

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestAuthorityLogIsBoundedAndContentFree(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	LogAuthorityDecision(logger, DecisionLog{
		SessionID:  strings.Repeat("s", 200),
		ActionID:   strings.Repeat("a", 200),
		Capability: "astronomer.tunnel.health",
		Mode:       ModeReadOnly,
		Effect:     EffectRead,
		Decision:   AuthorityDecision{Code: DeniedAuthorization},
	})

	line := output.String()
	for _, required := range []string{"charlie_authority_decision", "authorization_denied", "read_only", "read"} {
		if !strings.Contains(line, required) {
			t.Errorf("log missing %q: %s", required, line)
		}
	}
	if strings.Contains(line, strings.Repeat("s", 129)) || strings.Contains(line, strings.Repeat("a", 129)) {
		t.Fatalf("unbounded correlation value in log: %s", line)
	}
	for _, forbidden := range []string{"prompt", "evidence", "arguments", "authorization_ref", "credential", "secret", "url", "error"} {
		if strings.Contains(strings.ToLower(line), forbidden) {
			t.Fatalf("content/secret-shaped field %q in log: %s", forbidden, line)
		}
	}
}

func TestAuthorityLogHashesOpaqueIDsAndRejectsArbitraryCapabilityLabel(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	LogAuthorityDecision(logger, DecisionLog{SessionID: "session-SENTINEL", ActionID: "action-SENTINEL", Capability: "secret-SENTINEL", Decision: AuthorityDecision{Code: DeniedAuthorization}})
	line := output.String()
	if strings.Contains(line, "SENTINEL") || !strings.Contains(line, `"capability":"unknown"`) || !strings.Contains(line, "session_digest") || !strings.Contains(line, "action_digest") {
		t.Fatalf("authority log was not content-free: %s", line)
	}
}

func TestCharlieOperationalSerializerUsesClosedSchemas(t *testing.T) {
	tests := []struct {
		name        string
		write       func(*slog.Logger)
		event       string
		allowedKeys []string
	}{
		{
			name: "authority", event: "charlie_authority_decision",
			write: func(logger *slog.Logger) {
				LogAuthorityDecision(logger, DecisionLog{SessionID: "session", ActionID: "action", Capability: "astronomer.tunnel.health", Mode: ModeReadOnly, Effect: EffectRead, Decision: AuthorityDecision{Code: DeniedAuthorization}})
			},
			allowedKeys: []string{"time", "level", "msg", "event", "session_digest", "action_digest", "capability", "mode", "effect", "result", "code"},
		},
		{
			name: "failure", event: "charlie.operational.failure",
			write: func(logger *slog.Logger) {
				LogOperationalFailure(context.Background(), logger, "charlie.action_audit_persist_failed", "correlation")
			},
			allowedKeys: []string{"time", "level", "msg", "event", "failure_code", "correlation_digest"},
		},
		{
			name: "http", event: "charlie.http.mutation",
			write: func(logger *slog.Logger) {
				LogHTTPAudit(context.Background(), logger, "POST", 403, 2, "request", "correlation")
			},
			allowedKeys: []string{"time", "level", "msg", "event", "outcome_code", "method", "status_code", "duration_ms", "request_digest", "correlation_digest"},
		},
		{
			name: "runtime", event: "charlie.runtime.lifecycle",
			write: func(logger *slog.Logger) {
				LogRuntimeEvent(context.Background(), logger, "activated")
			},
			allowedKeys: []string{"time", "level", "msg", "event", "state"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			test.write(slog.New(slog.NewJSONHandler(&output, nil)))
			var row map[string]any
			if err := json.Unmarshal(output.Bytes(), &row); err != nil {
				t.Fatalf("decode operational row: %v", err)
			}
			if row["event"] != test.event {
				t.Fatalf("event=%v want=%s", row["event"], test.event)
			}
			got := make([]string, 0, len(row))
			for key := range row {
				got = append(got, key)
			}
			sort.Strings(got)
			want := append([]string(nil), test.allowedKeys...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("operational schema escaped allowlist: got=%v want=%v row=%s", got, want, output.String())
			}
		})
	}
}

func TestCharlieOperationalSerializerBoundsUnknownValuesAndHashesCorrelation(t *testing.T) {
	const canary = "caller-provider-secret-SENTINEL"
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	LogOperationalFailure(context.Background(), logger, canary, canary)
	LogHTTPAudit(context.Background(), logger, canary, -1, -1, canary, canary)
	LogAuthorityDecision(logger, DecisionLog{SessionID: canary, ActionID: canary, Capability: canary, Mode: Mode(canary), Effect: Effect(canary), Decision: AuthorityDecision{Code: DenialCode(canary)}})
	LogRuntimeEvent(context.Background(), logger, canary)
	serialized := output.String()
	if strings.Contains(serialized, canary) {
		t.Fatalf("operational serializer leaked caller content: %s", serialized)
	}
	for _, expected := range []string{"charlie.unclassified_failure", `"method":"UNKNOWN"`, `"status_code":500`, `"duration_ms":0`, `"capability":"unknown"`, `"code":"authorization_denied"`, `"state":"unclassified"`} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("bounded fallback %s absent: %s", expected, serialized)
		}
	}
}

func TestCharlieOperationalFailureVocabularySerializesExactly(t *testing.T) {
	for code := range operationalFailureCodes {
		t.Run(code, func(t *testing.T) {
			var output bytes.Buffer
			LogOperationalFailure(context.Background(), slog.New(slog.NewJSONHandler(&output, nil)), code, "correlation-SENTINEL")
			var row map[string]any
			if err := json.Unmarshal(output.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			if row["failure_code"] != code || strings.Contains(output.String(), "correlation-SENTINEL") {
				t.Fatalf("failure vocabulary escaped serializer: %s", output.String())
			}
		})
	}
}

func FuzzCharlieOperationalSerializerNeverEmitsCallerCanary(f *testing.F) {
	for _, seed := range []string{"", "provider error", "secret", strings.Repeat("x", 4096)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		canary := "CALLER-SENTINEL-" + value
		var output bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&output, nil))
		LogOperationalFailure(context.Background(), logger, canary, canary)
		LogHTTPAudit(context.Background(), logger, canary, 403, 1, canary, canary)
		LogAuthorityDecision(logger, DecisionLog{SessionID: canary, ActionID: canary, Capability: canary, Mode: Mode(canary), Effect: Effect(canary), Decision: AuthorityDecision{Code: DenialCode(canary)}})
		LogRuntimeEvent(context.Background(), logger, canary)
		if strings.Contains(output.String(), canary) {
			t.Fatalf("operational serializer leaked caller canary: %s", output.String())
		}
	})
}

func TestCharlieOperationalLogsHaveOneSerializationSink(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	directCalls := []string{
		"slog.Info(", "slog.InfoContext(", "slog.Warn(", "slog.WarnContext(", "slog.Error(", "slog.ErrorContext(", "slog.Debug(", "slog.DebugContext(",
		"logger.Info(", "logger.InfoContext(", "logger.Warn(", "logger.WarnContext(", "logger.Error(", "logger.ErrorContext(", "logger.Debug(", "logger.DebugContext(", "logger.Log(", "logger.LogAttrs(",
		".logger.Info(", ".logger.Warn(", ".logger.Error(", ".logger.Debug(", ".logger.Log(",
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "observability.go" {
			continue
		}
		content, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, call := range directCalls {
			if strings.Contains(string(content), call) {
				t.Fatalf("%s contains direct operational log call %q; route it through observability.go", name, call)
			}
		}
	}
	for _, pattern := range []string{"../handler/charlie*.go", "../server/middleware/audit.go", "../server/server.go"} {
		paths, globErr := filepath.Glob(pattern)
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, forbidden := range []string{"slog.WarnContext(", "log.Info(\"Charlie", "log.Warn(\"Charlie", "log.Error(\"Charlie", "log.Debug(\"Charlie"} {
				if strings.Contains(string(content), forbidden) {
					t.Fatalf("%s contains direct Charlie operational logging %q", path, forbidden)
				}
			}
		}
	}
}
