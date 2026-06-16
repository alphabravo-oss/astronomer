// Package allowlist implements the apiserver allow-list renderer +
// CIDR parsing/validation utilities used by both the handler (migration
// 070) and the reconciler worker.
//
// The renderer takes three inputs:
//
//   - operatorCIDRs   : the CIDR list the operator stored on the row.
//   - astronomerEgress: the tunnel-egress block the platform always
//     needs to keep open (otherwise the agent loses
//     its outbound path back to the control plane).
//   - emergency       : the global emergency-access block (optional;
//     set by the operator as a "break-glass" CIDR
//     in chart values).
//
// And returns the desired-state list with:
//
//   - Astronomer egress and emergency blocks stamped ON TOP of operator
//     entries every render (operators cannot remove them — silent
//     re-add by design; the UI surfaces them as read-only).
//   - Duplicates de-duped by canonical text form.
//   - Stable output order so SSA patches reach steady state without
//     reorder-only churn.
//
// CIDR validation is centralised here so the handler, the renderer, and
// the reconciler all reject the same set of bad inputs (zero-/8 / non-
// CIDR / IPv6 — see ParseCIDR for the full rule set).
package allowlist

import (
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
)

// MinIPv4PrefixBits is the operator-defined CIDR floor. Anything broader
// (/0..../7) is rejected because an operator-supplied 0.0.0.0/0 would
// effectively disable the lockbox — the explicit reject pushes back on
// "I'll just put /0 in there" reflexes.
const MinIPv4PrefixBits = 8

// ParseCIDR canonicalises an operator-supplied string into a netip.Prefix
// and applies the validation rules every layer of the allow-list system
// agrees on:
//
//   - The string MUST parse as a CIDR prefix (no host bits set — the
//     parser rejects "10.0.0.1/24"; operators get a clear error rather
//     than silent masking).
//   - IPv4 prefixes /0..../7 are rejected (see MinIPv4PrefixBits).
//   - IPv6 is REJECTED at v1. None of the cloud-provider APIs ship IPv6
//     support uniformly yet (EKS shipped IPv6 auth ranges in 2024, GKE
//     still doesn't; we'd have to feature-flag per provider). Deferred
//     to a future sprint.
//
// Returned prefix is the canonicalised String() form — every layer
// re-renders it the same way so set equality is by-string.
func ParseCIDR(raw string) (netip.Prefix, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return netip.Prefix{}, fmt.Errorf("CIDR is empty")
	}
	p, err := netip.ParsePrefix(trimmed)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: %w", raw, err)
	}
	if !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: IPv6 is not supported in v1", raw)
	}
	if p.Bits() < MinIPv4PrefixBits {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: prefix length %d is broader than the /%d floor", raw, p.Bits(), MinIPv4PrefixBits)
	}
	if p.Addr() != p.Masked().Addr() {
		// Host bits set — reject so operators don't lose silent /32 host
		// bits when the LB normalises to the network address.
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: host bits are set", raw)
	}
	return p, nil
}

// ValidateCIDRs parses + validates every entry. Returns the canonical
// string slice (each entry .String() of the parsed prefix) and the first
// validation error encountered.
func ValidateCIDRs(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		p, err := ParseCIDR(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, p.String())
	}
	return out, nil
}

// Render merges operator CIDRs with the Astronomer tunnel egress and
// emergency-access blocks into the canonical desired-state list. Bad
// input is silently dropped here (the handler already validated on the
// write path; if we somehow stored a bad CIDR we don't want to crash
// the reconciler — log + skip is the right move).
//
// Output ordering: parsed-prefix-sorted. The desired set is a SET so
// the order is arbitrary semantically; the sort gives steady state for
// SSA writes and a clean "list-no-changes" diff in the UI.
func Render(operatorCIDRs, astronomerEgress, emergency []string) []string {
	set := make(map[string]struct{}, len(operatorCIDRs)+len(astronomerEgress)+len(emergency))
	add := func(raw string) {
		p, err := ParseCIDR(raw)
		if err != nil {
			return
		}
		set[p.String()] = struct{}{}
	}
	for _, c := range operatorCIDRs {
		add(c)
	}
	for _, c := range astronomerEgress {
		add(c)
	}
	for _, c := range emergency {
		add(c)
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// AstronomerEgressFromEnv reads ASTRONOMER_TUNNEL_EGRESS_CIDRS from env
// (comma-separated). Returns empty when unset so the reconciler can
// fall back to operator-only. The chart-values path normally sets this
// via deploy/chart/templates/* on every operator install; this env-
// based fallback is the dev-laptop / disconnected-test escape hatch.
//
// Unparseable entries are silently dropped (logged elsewhere; we don't
// crash the worker on a typo in chart values).
func AstronomerEgressFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("ASTRONOMER_TUNNEL_EGRESS_CIDRS"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, err := ParseCIDR(p); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// SameSet reports whether two CIDR lists represent the same desired
// state. Used by the reconciler to decide "no patch needed" (mode=
// enforce + identical sets) and to compute drift (mode=monitor).
//
// SameSet is order-insensitive but case-sensitive on canonical form —
// every CIDR in both slices MUST already have been canonicalised via
// ParseCIDR.String() (the renderer does this; the cloud-provider GET
// path normalises before comparing).
func SameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := make(map[string]struct{}, len(a))
	for _, c := range a {
		as[c] = struct{}{}
	}
	for _, c := range b {
		if _, ok := as[c]; !ok {
			return false
		}
	}
	return true
}

// CanonicaliseEffective takes a list as returned by a cloud-provider
// API (which may carry whitespace, lowercase variants, different
// ordering, or host bits set) and normalises it through ParseCIDR so
// SameSet can compare apples to apples.
//
// Invalid entries are dropped silently — a future cloud provider that
// added a non-CIDR sentinel to the auth-IP-ranges field shouldn't
// crash the reconciler.
func CanonicaliseEffective(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		p, err := ParseCIDR(raw)
		if err != nil {
			continue
		}
		s := p.String()
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
