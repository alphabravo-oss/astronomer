package charlie

import (
	"bytes"
	"log/slog"
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
