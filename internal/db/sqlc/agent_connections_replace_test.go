package sqlc

import (
	"strings"
	"testing"
)

func TestReplaceActiveAgentConnectionOrdersDisconnectBeforeInsert(t *testing.T) {
	query := replaceActiveAgentConnection
	if !strings.Contains(query, "RETURNING id") {
		t.Fatalf("replacement query does not expose the disconnected rows as an execution dependency:\n%s", query)
	}
	if !strings.Contains(query, "FROM (SELECT count(*) FROM disconnected) AS disconnect_barrier") {
		t.Fatalf("replacement insert can race its own disconnect CTE:\n%s", query)
	}
}
