package middleware

import (
	"context"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestReadAuditTierAddsCoverageWithoutWeakeningPolicies(t *testing.T) {
	store := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{policy("credentials", "/credentials", "GET", 1)}}
	eval := NewPolicyEvaluator(store)
	tier := "standard"
	eval.SetReadTierReader(func(context.Context) string { return tier })
	ctx := context.Background()
	if eval.Match(ctx, "/clusters", "GET") != nil {
		t.Fatal("standard unexpectedly added coverage")
	}
	tier = "diagnostic"
	if p := eval.Match(ctx, "/clusters", "GET"); p == nil || p.SampleRate != 0.1 {
		t.Fatalf("diagnostic policy = %+v", p)
	}
	if p := eval.Match(ctx, "/credentials", "GET"); p == nil || p.SampleRate != 1 {
		t.Fatalf("credential policy weakened: %+v", p)
	}
	tier = "incident"
	if p := eval.Match(ctx, "/clusters", "GET"); p == nil || p.SampleRate != 1 {
		t.Fatalf("incident policy = %+v", p)
	}
	if eval.Match(ctx, "/clusters", "POST") != nil {
		t.Fatal("read tier must not replace mutation auditing")
	}
	tier = "standard"
	if eval.Match(ctx, "/clusters", "GET") != nil {
		t.Fatal("return to standard did not remove added coverage")
	}
}
