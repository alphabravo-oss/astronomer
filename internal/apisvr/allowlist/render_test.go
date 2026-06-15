package allowlist

import (
	"os"
	"strings"
	"testing"
)

func TestRender_AddsAstronomerEgress(t *testing.T) {
	operator := []string{"10.0.0.0/8"}
	egress := []string{"54.10.0.0/16"}
	got := Render(operator, egress, nil)
	// Egress must appear in the rendered output even though the operator
	// list didn't carry it.
	if !contains(got, "54.10.0.0/16") {
		t.Fatalf("expected egress block in output, got %v", got)
	}
	if !contains(got, "10.0.0.0/8") {
		t.Fatalf("expected operator block in output, got %v", got)
	}
}

func TestRender_EgressIsReAddedEvenIfOperatorRemoved(t *testing.T) {
	// Operator deletes the egress block from their list; render should
	// silently re-add it. This is the "can't lock yourself out of the
	// tunnel" guarantee.
	operator := []string{"203.0.113.0/24"} // operator's own VPN
	egress := []string{"54.10.0.0/16"}
	got := Render(operator, egress, nil)
	if !contains(got, "54.10.0.0/16") {
		t.Fatalf("egress must always be present in desired state; got %v", got)
	}
}

func TestRender_DedupesOverlappingCIDRs(t *testing.T) {
	// Exact duplicates (operator listed the egress block) should dedupe.
	operator := []string{"54.10.0.0/16", "10.0.0.0/8"}
	egress := []string{"54.10.0.0/16"}
	got := Render(operator, egress, nil)
	count := 0
	for _, c := range got {
		if c == "54.10.0.0/16" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 54.10.0.0/16 to appear exactly once, got %d in %v", count, got)
	}
}

func TestRender_PreservesOrderStable(t *testing.T) {
	// The two renders below produce the same desired set; their string
	// slices must compare element-wise equal so SSA stops re-writing.
	a := Render([]string{"10.0.0.0/8", "192.168.0.0/16"}, []string{"54.10.0.0/16"}, nil)
	b := Render([]string{"192.168.0.0/16", "10.0.0.0/8"}, []string{"54.10.0.0/16"}, nil)
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %v vs %v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("element %d differs: %q vs %q (full a=%v b=%v)", i, a[i], b[i], a, b)
		}
	}
}

func TestRender_DropsInvalidCIDRsSilently(t *testing.T) {
	// The handler validates on the write path; render() is defensive
	// against a stored row that somehow holds a bad value.
	operator := []string{"not-a-cidr", "10.0.0.0/8"}
	got := Render(operator, nil, nil)
	if !contains(got, "10.0.0.0/8") {
		t.Fatalf("valid entry missing: %v", got)
	}
	if contains(got, "not-a-cidr") {
		t.Fatalf("invalid entry leaked through: %v", got)
	}
}

func TestParseCIDR_RejectsZeroSlash(t *testing.T) {
	// 0.0.0.0/0 is the explicit reject — it would disable the lockbox.
	if _, err := ParseCIDR("0.0.0.0/0"); err == nil {
		t.Fatalf("0.0.0.0/0 must be rejected")
	}
	if _, err := ParseCIDR("0.0.0.0/4"); err == nil {
		t.Fatalf("/4 prefixes must be rejected (below /8 floor)")
	}
	if _, err := ParseCIDR("10.0.0.0/8"); err != nil {
		t.Fatalf("/8 should be accepted; got %v", err)
	}
}

func TestParseCIDR_RejectsHostBits(t *testing.T) {
	if _, err := ParseCIDR("10.0.0.1/24"); err == nil {
		t.Fatalf("host bits should be rejected")
	}
}

func TestParseCIDR_RejectsIPv6(t *testing.T) {
	if _, err := ParseCIDR("2001:db8::/64"); err == nil {
		t.Fatalf("IPv6 should be rejected in v1")
	}
}

func TestParseCIDR_RejectsNonCIDR(t *testing.T) {
	for _, bad := range []string{"", "  ", "not-a-cidr", "10.0.0.0", "10.0.0.0/abc"} {
		if _, err := ParseCIDR(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestValidateCIDRs_Canonicalises(t *testing.T) {
	out, err := ValidateCIDRs([]string{"  10.0.0.0/8  ", "192.168.0.0/16"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0] != "10.0.0.0/8" {
		t.Fatalf("canonicalisation failed: %v", out)
	}
}

func TestValidateCIDRs_StopsAtFirstBad(t *testing.T) {
	_, err := ValidateCIDRs([]string{"10.0.0.0/8", "0.0.0.0/0"})
	if err == nil {
		t.Fatalf("expected error on /0 entry")
	}
	if !strings.Contains(err.Error(), "0.0.0.0/0") {
		t.Fatalf("error should mention the offending CIDR; got %v", err)
	}
}

func TestAstronomerEgressFromEnv(t *testing.T) {
	t.Setenv("ASTRONOMER_TUNNEL_EGRESS_CIDRS", "  54.10.0.0/16 ,  bad-cidr , 192.168.0.0/16  ")
	got := AstronomerEgressFromEnv()
	if len(got) != 2 {
		t.Fatalf("expected 2 valid entries, got %v", got)
	}
	if got[0] != "54.10.0.0/16" || got[1] != "192.168.0.0/16" {
		t.Fatalf("unexpected entries: %v", got)
	}
}

func TestAstronomerEgressFromEnv_Unset(t *testing.T) {
	_ = os.Unsetenv("ASTRONOMER_TUNNEL_EGRESS_CIDRS")
	got := AstronomerEgressFromEnv()
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestSameSet_OrderInsensitive(t *testing.T) {
	if !SameSet([]string{"10.0.0.0/8", "192.168.0.0/16"}, []string{"192.168.0.0/16", "10.0.0.0/8"}) {
		t.Fatalf("SameSet should be order-insensitive")
	}
	if SameSet([]string{"10.0.0.0/8"}, []string{"10.0.0.0/8", "192.168.0.0/16"}) {
		t.Fatalf("SameSet should detect size mismatch")
	}
}

func TestCanonicaliseEffective(t *testing.T) {
	// Cloud provider may return host bits or weird order; we normalise.
	got := CanonicaliseEffective([]string{"10.0.0.0/8", "192.168.0.0/16", "10.0.0.0/8"})
	if len(got) != 2 {
		t.Fatalf("dedupe failed: %v", got)
	}
	// Drops invalid entries.
	got = CanonicaliseEffective([]string{"10.0.0.0/8", "garbage"})
	if len(got) != 1 || got[0] != "10.0.0.0/8" {
		t.Fatalf("invalid entry not dropped: %v", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
