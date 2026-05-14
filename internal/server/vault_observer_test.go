package server

// Audit row contract for the vault Observer (T6.067).
//
// Two guards:
//   * OnResolved / OnFailed must produce an audit row when an audit
//     writer is wired.
//   * The audit row must NEVER carry the secret value or a raw error
//     string — only the reference path, key, and a coarse reason.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/vault"
)

type fakeAuditQuerier struct {
	rows []sqlc.CreateAuditLogV1Params
}

func (f *fakeAuditQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.rows = append(f.rows, arg)
	return nil
}

func TestVaultObserver_ResolvedWritesAudit(t *testing.T) {
	q := &fakeAuditQuerier{}
	obs := newVaultMetricsObserver(q)
	obs.OnResolved(context.Background(), "prod-conn",
		vault.Reference{Raw: "${vault://prod/kv/foo#bar}", Engine: "kv", Path: "foo", Key: "bar"},
		25*time.Millisecond)
	if len(q.rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(q.rows))
	}
	if q.rows[0].Action != "vault.reference.resolved" {
		t.Errorf("action=%q, want vault.reference.resolved", q.rows[0].Action)
	}
}

func TestVaultObserver_FailedClassifiesReason(t *testing.T) {
	q := &fakeAuditQuerier{}
	obs := newVaultMetricsObserver(q)
	cases := []struct {
		err    error
		reason string
	}{
		{errors.New("dial tcp: connection refused"), "connectivity"},
		{errors.New("403 forbidden"), "permission_denied"},
		{errors.New("404 not found"), "not_found"},
		{errors.New("401 unauthorized"), "unauthorized"},
		{errors.New("unexpected"), "other"},
	}
	for _, c := range cases {
		q.rows = nil
		obs.OnFailed(context.Background(), "c", vault.Reference{Raw: "ref", Path: "p", Key: "k"}, c.err)
		if len(q.rows) != 1 {
			t.Fatalf("expected 1 row for err %v", c.err)
		}
		// Decode JSON detail.
		detail := string(q.rows[0].Detail)
		if !strings.Contains(detail, `"reason":"`+c.reason+`"`) {
			t.Errorf("err %v: detail %s should contain reason=%s", c.err, detail, c.reason)
		}
		// Defensive: the raw error string must NOT appear in the detail.
		if strings.Contains(detail, c.err.Error()) {
			t.Errorf("err %v: detail leaked raw error string: %s", c.err, detail)
		}
	}
}

func TestVaultObserver_NilAuditNoOp(t *testing.T) {
	obs := newVaultMetricsObserver(nil)
	// Should not panic.
	obs.OnResolved(context.Background(), "c", vault.Reference{}, 0)
	obs.OnFailed(context.Background(), "c", vault.Reference{}, errors.New("x"))
}
