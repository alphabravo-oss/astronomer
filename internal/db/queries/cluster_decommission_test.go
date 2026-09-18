package queries_test

import (
	"os"
	"strings"
	"testing"
)

func TestClusterDecommissionClaimsAreFairAndFenced(t *testing.T) {
	raw, err := os.ReadFile("cluster_decommission.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	claim := namedQuery(t, text, "-- name: ClaimPendingClusterDecommissions :many")
	for _, required := range []string{
		"decommission_lease_until <= now()",
		"ORDER BY updated_at ASC, created_at ASC, id ASC",
		"FOR UPDATE SKIP LOCKED",
		"decommission_claim_token = sqlc.arg(claim_token)",
	} {
		if !strings.Contains(claim, required) {
			t.Errorf("cluster decommission batch claim missing %q", required)
		}
	}
	if strings.Index(claim, "decommission_lease_until <= now()") > strings.Index(claim, "LIMIT sqlc.arg(query_limit)") {
		t.Error("live-lease exclusion must happen before the batch LIMIT")
	}

	for _, marker := range []string{
		"-- name: RenewClusterDecommissionClaim :execrows",
		"-- name: ReleaseClusterDecommissionClaim :execrows",
		"-- name: UpdateClusterDecommissionPhases :one",
		"-- name: MarkClusterDecommissionSucceeded :one",
		"-- name: MarkClusterDecommissionFailed :one",
	} {
		query := namedQuery(t, text, marker)
		if !strings.Contains(query, "decommission_claim_token = sqlc.arg(claim_token)") {
			t.Errorf("%s is not ownership-token fenced", marker)
		}
	}
}
