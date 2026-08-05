package charlie

import (
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestCredentialExpiryDiagnosticUsesExactPersistedExpiries(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		connection  sqlc.CharlieConnection
		wantState   string
		wantSummary string
	}{
		{
			name: "healthy certificate is earliest",
			connection: sqlc.CharlieConnection{
				CertificateExpiresAt:        now.Add(30 * 24 * time.Hour),
				ArtifactCredentialExpiresAt: now.Add(60 * 24 * time.Hour),
			},
			wantState: "healthy", wantSummary: "earliest active certificate expires",
		},
		{
			name: "expiring artifact credential is degraded",
			connection: sqlc.CharlieConnection{
				CertificateExpiresAt:        now.Add(30 * 24 * time.Hour),
				ArtifactCredentialExpiresAt: now.Add(2 * 24 * time.Hour),
			},
			wantState: "degraded", wantSummary: "earliest active artifact credential expires",
		},
		{
			name: "expired exact credential is not unknown",
			connection: sqlc.CharlieConnection{
				CertificateExpiresAt:        now.Add(30 * 24 * time.Hour),
				ArtifactCredentialExpiresAt: now.Add(-time.Minute),
			},
			wantState: "degraded", wantSummary: "artifact credential expired",
		},
		{
			name: "missing exact metadata remains unknown",
			connection: sqlc.CharlieConnection{
				CertificateExpiresAt: now.Add(30 * 24 * time.Hour),
			},
			wantState: "unknown", wantSummary: "expiry metadata is unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, summary := credentialExpiryDiagnostic(test.connection, now)
			if state != test.wantState || !strings.Contains(summary, test.wantSummary) {
				t.Fatalf("credentialExpiryDiagnostic() = %q, %q; want state %q and summary containing %q", state, summary, test.wantState, test.wantSummary)
			}
		})
	}
}

func TestAdminAutoReadinessExplainsEveryFailClosedPrerequisite(t *testing.T) {
	readiness := adminAutoReadiness(ModePrerequisites{}, nil)
	if readiness.Ready || len(readiness.Blockers) != 4 {
		t.Fatalf("empty prerequisites readiness=%+v", readiness)
	}
	want := map[string]bool{
		"disclosure_unacknowledged":        false,
		"automation_identity_unconfigured": false,
		"allowlist_unreviewed":             false,
		"target_grants_missing":            false,
	}
	for _, blocker := range readiness.Blockers {
		if _, ok := want[blocker.Code]; !ok || blocker.Message == "" || blocker.NextAction == "" {
			t.Fatalf("unsafe or unknown blocker=%+v", blocker)
		}
		want[blocker.Code] = true
	}
	for code, found := range want {
		if !found {
			t.Fatalf("missing blocker %q", code)
		}
	}
	ready := adminAutoReadiness(ModePrerequisites{DisclosureAcknowledged: true, AutomationIdentityReady: true, AutomationAllowlistReady: true, AutomationTargetReady: true}, nil)
	if !ready.Ready || len(ready.Blockers) != 0 {
		t.Fatalf("complete prerequisites readiness=%+v", ready)
	}
}

func TestDiagnosticNextActionIsFixedAndSafe(t *testing.T) {
	if got := diagnosticNextAction("mcp_tls_discovery", "degraded"); !strings.Contains(got, "acknowledge") || strings.Contains(strings.ToLower(got), "secret") {
		t.Fatalf("MCP next action=%q", got)
	}
	if got := diagnosticNextAction("credential_expiry", "healthy"); got != "No operator action is required." {
		t.Fatalf("healthy next action=%q", got)
	}
}
