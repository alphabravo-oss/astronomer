package sqlc

import (
	"regexp"
	"strings"
	"testing"
)

// taskOutboxColumnCount is the number of columns scanTaskOutboxRow reads.
const taskOutboxColumnCount = 17

// TestClaimDueTaskOutboxReturningIsAliasQualified pins the fix for a bug that
// silently disabled the ENTIRE task outbox for ~30 days.
//
// claimDueTaskOutbox is `UPDATE task_outbox AS t ... FROM picked ... RETURNING`.
// Both `t` and the `picked` CTE expose `id`, so an unqualified RETURNING list
// makes `id` ambiguous and Postgres rejects the statement with SQLSTATE 42702
// ("column reference \"id\" is ambiguous") on EVERY call. The error fires before
// any row is touched, so nothing is ever claimed, attempt_count never
// increments, and no failure is ever logged against a row — the outbox just
// silently delivers nothing. Live impact when found: 613 rows stranded at
// attempt_count=0 (537 argocd:auto_register_cluster, 76 cluster:decommission).
//
// There is no live-Postgres unit harness in this package, so this locks the
// statement string (same approach as the ArgoCD operation claim tests).
func TestClaimDueTaskOutboxReturningIsAliasQualified(t *testing.T) {
	sql := claimDueTaskOutbox

	// Precondition: this really is the multi-table shape that creates the
	// ambiguity. If the query is ever rewritten to a single-table form this
	// guard should be revisited rather than blindly satisfied.
	if !strings.Contains(sql, "UPDATE task_outbox AS t") || !strings.Contains(sql, "FROM picked") {
		t.Fatalf("expected the `UPDATE task_outbox AS t ... FROM picked` shape; got:\n%s", sql)
	}

	idx := strings.Index(sql, "RETURNING")
	if idx < 0 {
		t.Fatalf("expected a RETURNING clause; got:\n%s", sql)
	}
	returning := sql[idx:]

	if !strings.Contains(returning, "t.id") {
		t.Errorf("RETURNING must select `t.id`, not a bare `id` (ambiguous with the picked CTE, SQLSTATE 42702); got:\n%s", returning)
	}
	// A bare column reference — one not preceded by the `t.` alias — is the bug.
	if regexp.MustCompile(`(^|[\s,])id\b`).MatchString(returning) {
		t.Errorf("RETURNING contains an unqualified `id`; both task_outbox and picked expose it, so every claim fails with 42702. Use taskOutboxSelectColumnsT:\n%s", returning)
	}
	if got := strings.Count(returning, "t."); got != taskOutboxColumnCount {
		t.Errorf("expected all %d columns alias-qualified, found %d `t.` prefixes:\n%s", taskOutboxColumnCount, got, returning)
	}
}

// TestSingleTableTaskOutboxStatementsStayUnqualified is the mirror guard: the
// single-table statements have no `t` alias in scope, so the alias-qualified
// column list would be invalid there.
func TestSingleTableTaskOutboxStatementsStayUnqualified(t *testing.T) {
	for name, statement := range map[string]string{
		"getTaskOutbox":    getTaskOutbox,
		"retryTaskOutbox":  retryTaskOutbox,
		"upsertTaskOutbox": upsertTaskOutbox,
	} {
		if strings.Contains(statement, "t.id") {
			t.Errorf("%s is single-table (no `t` alias in scope) and must use the unqualified column list:\n%s", name, statement)
		}
	}
}

// TestTaskOutboxColumnListsAgree ensures the qualified and unqualified column
// lists never drift apart — scanTaskOutboxRow reads them positionally, so a
// mismatch in order or count would scan the wrong values into the struct.
func TestTaskOutboxColumnListsAgree(t *testing.T) {
	norm := func(s string) []string {
		out := []string{}
		for _, f := range strings.Split(strings.ReplaceAll(s, "\n", " "), ",") {
			f = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(f), "t."))
			if f != "" {
				out = append(out, f)
			}
		}
		return out
	}
	plain, qualified := norm(taskOutboxSelectColumns), norm(taskOutboxSelectColumnsT)
	if len(plain) != taskOutboxColumnCount {
		t.Fatalf("taskOutboxSelectColumns has %d columns, want %d", len(plain), taskOutboxColumnCount)
	}
	if len(plain) != len(qualified) {
		t.Fatalf("column list length mismatch: unqualified=%d qualified=%d", len(plain), len(qualified))
	}
	for i := range plain {
		if plain[i] != qualified[i] {
			t.Errorf("column %d differs: unqualified=%q qualified=%q (scanTaskOutboxRow reads positionally)", i, plain[i], qualified[i])
		}
	}
}
