package handler

import (
	"strings"
	"testing"

	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
)

func TestValidateArgoProjectSyncWindows(t *testing.T) {
	valid := []argocdclient.AppProjectSyncWindow{
		{
			Kind:         "deny",
			Schedule:     "0 22 * * 1-5",
			Duration:     "10h",
			Applications: []string{"*-prod"},
			ManualSync:   true,
			TimeZone:     "UTC",
			Description:  "weekday production blackout",
		},
		{
			Kind:        "allow",
			Schedule:    "0 9 * * 1-5",
			Duration:    "8h",
			Namespaces:  []string{"production"},
			Clusters:    []string{"prod-*"},
			SyncOverrun: true,
			TimeZone:    "America/New_York",
		},
	}
	if err := validateArgoProjectSyncWindows(valid); err != nil {
		t.Fatalf("valid sync windows rejected: %v", err)
	}

	tests := []struct {
		name    string
		window  argocdclient.AppProjectSyncWindow
		wantErr string
	}{
		{
			name:    "invalid kind",
			window:  argocdclient.AppProjectSyncWindow{Kind: "pause", Schedule: "0 0 * * *", Duration: "1h", Applications: []string{"*"}},
			wantErr: "kind",
		},
		{
			name:    "invalid schedule",
			window:  argocdclient.AppProjectSyncWindow{Kind: "deny", Schedule: "nightly", Duration: "1h", Applications: []string{"*"}},
			wantErr: "schedule",
		},
		{
			name:    "invalid duration",
			window:  argocdclient.AppProjectSyncWindow{Kind: "deny", Schedule: "0 0 * * *", Duration: "soon", Applications: []string{"*"}},
			wantErr: "duration",
		},
		{
			name:    "missing scope",
			window:  argocdclient.AppProjectSyncWindow{Kind: "deny", Schedule: "0 0 * * *", Duration: "1h"},
			wantErr: "selector",
		},
		{
			name:    "invalid timezone",
			window:  argocdclient.AppProjectSyncWindow{Kind: "allow", Schedule: "0 0 * * *", Duration: "1h", Applications: []string{"*"}, TimeZone: "Mars/Olympus"},
			wantErr: "timezone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateArgoProjectSyncWindows([]argocdclient.AppProjectSyncWindow{tt.window})
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateArgoProjectPatchAllowsPartialSyncWindowUpdates(t *testing.T) {
	if err := validateArgoProjectPatch([]byte(`{"description":"team project"}`)); err != nil {
		t.Fatalf("description-only patch rejected: %v", err)
	}
	if err := validateArgoProjectPatch([]byte(`{"syncWindows":[{"kind":"deny","schedule":"0 0 * * *","duration":"1h","clusters":["prod"]}]}`)); err != nil {
		t.Fatalf("sync window patch rejected: %v", err)
	}
	if err := validateArgoProjectPatch([]byte(`{"syncWindows":[{"kind":"deny","schedule":"0 0 * * *","duration":"bad","clusters":["prod"]}]}`)); err == nil {
		t.Fatalf("invalid sync window patch accepted")
	}
}

func TestValidateArgoProjectURLsAndPatterns(t *testing.T) {
	valid := argocdclient.AppProjectSpec{
		SourceRepos:  []string{"*", "https://git.example/team/*", "!https://git.example/team/private/*"},
		Destinations: []argocdclient.ApplicationDestination{{Server: "https://kube.example:6443", Namespace: "prod"}},
	}
	if err := validateArgoProjectSpec(valid); err != nil {
		t.Fatalf("valid project rejected: %v", err)
	}
	for _, raw := range []string{
		`{"sourceRepos":["https://user:pass@git.example/repo"]}`,
		`{"sourceRepos":["https://git.example/repo?X-Amz-Signature=secret"]}`,
		`{"destinations":[{"server":"https://kube.example:6443?token=secret","namespace":"prod"}]}`,
	} {
		if err := validateArgoProjectPatch([]byte(raw)); err == nil {
			t.Fatalf("unsafe project patch accepted: %s", raw)
		}
	}
}

func TestNormalizeSyncRequestRequiresOverrideReason(t *testing.T) {
	if _, err := normalizeSyncRequest(SyncRequest{SyncWindowOverride: true}); err == nil {
		t.Fatalf("override without reason accepted")
	}
	req, err := normalizeSyncRequest(SyncRequest{
		Revision:           " main ",
		Reason:             " maintenance approval ",
		SyncWindowOverride: true,
	})
	if err != nil {
		t.Fatalf("valid override rejected: %v", err)
	}
	if req.Revision != "main" || req.Reason != "maintenance approval" {
		t.Fatalf("request not normalized: %+v", req)
	}
	if _, err := normalizeSyncRequest(SyncRequest{Reason: strings.Repeat("a", 501)}); err == nil {
		t.Fatalf("oversized reason accepted")
	}
}
