package server

import (
	"testing"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
)

func TestLocalAgentPrivilegeProfileMatchesManagedClusterAuthority(t *testing.T) {
	t.Parallel()

	if localAgentPrivilegeProfile != agenttemplate.PrivilegeProfileAdmin {
		t.Fatalf("local agent privilege profile = %q, want %q", localAgentPrivilegeProfile, agenttemplate.PrivilegeProfileAdmin)
	}
}
