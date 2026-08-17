package rbac

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The WebSocket exec/logs relays moved off clusters:update / clusters:read and
// onto pods:exec / pods:logs, matching the HTTP k8s-proxy path. That changes
// what every shipped built-in role can stream. This file is the compatibility
// ledger for that move: every seeded role that loses access must either be
// present in the canonical greenfield seed or appear below with a written
// reason.

// intentionalPodStreamReductions names every seeded built-in role that
// deliberately loses WS log and/or exec access, keyed "scope::name". A role
// that starts losing access without an entry here fails the test: either
// backfill it in a migration or record why the loss is correct.
var intentionalPodStreamReductions = map[string]string{
	"global::Platform Operator": "AUTHORIZED privilege reduction. Day-2 platform workflows without destructive " +
		"platform administration; holds clusters:update and ZERO pods rules, yet that alone gave it per-pod " +
		"exec and every container's logs on every adopted cluster. Bind 'Cluster Operator' or " +
		"'Cluster Troubleshooter' for pod access — they declare pods:exec on purpose. NOTE: this removes the " +
		"WS exec/logs half only; the break-glass kubectl shell is a separate path still gated on " +
		"clusters:update (tracked as a separate residual gap).",
	"global::Standard User": "clusters:[read,list] with no pods rule. 'Can view clusters and manage assigned " +
		"resources' never included reading pod logs, which carry bearer tokens and PII.",
	"global::Read Only": "'*':[read,list] with no pods rule. The modern catalog expresses fleet-wide " +
		"read-only as 'Audit Viewer' / 'Security Auditor' / 'Monitoring Viewer', none of which grants " +
		"pods:logs; 'Logging Viewer' is the designated pod-log reader.",
	"global::Security Auditor":          "clusters:[read,list], no pods rule. Posture review, not log access.",
	"global::Compliance Manager":        "clusters:[read,list], no pods rule. Compliance baselines, not log access.",
	"global::GitOps Admin":              "clusters:[read,list], no pods rule. Delivery administration, not log access.",
	"global::GitOps Viewer":             "clusters:[read,list], no pods rule. Sync/drift visibility, not log access.",
	"global::Monitoring Admin":          "clusters:[read,list], no pods rule. Monitoring config, not log access.",
	"global::Monitoring Viewer":         "clusters:[read,list], no pods rule. Metrics and alert state, not log access.",
	"global::Restore Operator":          "clusters:[read,list], no pods rule. Restore workflows, not log access.",
	"global::Support Bundle Operator":   "clusters:[read,list], no pods rule. Bundles are redacted by design.",
	"global::Audit Viewer":              "clusters:[read,list], no pods rule; its own description says it cannot exec into pods.",
	"global::Catalog Admin":             "clusters:[read,list], no pods rule. Chart curation, not log access.",
	"global::Backup Operator":           "Backup lifecycle administration does not require access to workload logs.",
	"global::Alerts Manager":            "Alert lifecycle administration does not require access to workload logs.",
	"global::Catalog Maintainer":        "Catalog curation does not require access to workload logs.",
	"global::Cluster Registrar":         "Cluster enrollment and agent lifecycle do not grant workload log or exec access.",
	"cluster::Cluster Backup Operator":  "clusters:[read], no pods rule. Backup schedules and runs, not log access.",
	"cluster::Cluster Monitoring Admin": "Monitoring configuration does not require access to workload logs.",
	"cluster::Node Operator": "pods:[read,list,watch] with `logs` omitted on purpose — the row and " +
		"templates/node-operator.yaml already agree. Cordon/drain/taint needs no log stream.",
	"cluster::Service Mesh Operator": "clusters:[read], no pods rule. Mesh policy and health, not log access.",
	"cluster::Storage Manager": "pods:[read,list] with `logs` omitted on purpose — the row and " +
		"templates/storage-manager.yaml already agree.",
}

// canonicalPodStreamGrants are roles whose shipped template intentionally
// declares pods:logs in the canonical greenfield seed.
var canonicalPodStreamGrants = []string{"Cluster Member", "Cluster Viewer", "Project Viewer"}

func grants(role seededRole, resource, verb string) bool {
	for _, rule := range role.rules {
		if rule.Resource != resource && rule.Resource != "*" {
			continue
		}
		for _, candidate := range rule.Verbs {
			if candidate == verb || candidate == "*" {
				return true
			}
		}
	}
	return false
}

// TestBuiltinRolePodStreamReductionsAreDocumented is the compatibility gate for
// the exec/logs verb move: no shipped built-in role may quietly lose pod log or
// exec streaming. A loss is legitimate only if it is present in the canonical
// seed or justified in intentionalPodStreamReductions.
func TestBuiltinRolePodStreamReductionsAreDocumented(t *testing.T) {
	repaired := make(map[string]bool, len(canonicalPodStreamGrants))
	for _, name := range canonicalPodStreamGrants {
		repaired[name] = true
	}

	seen := make(map[string]bool)
	for key, role := range loadSeededRoles(t) {
		// Project roles are NOT skipped. A project binding does reach the WS
		// exec/logs consumers: with namespace_scoped_rbac_enabled on,
		// SQLCRBACQuerier.expandProjectBindings rewrites it into synthetic
		// namespace-scoped CLUSTER bindings and the consumers pass the pod's
		// real namespace, so bindingApplies matches. See the project-roles
		// paragraph in migration 142.
		lostLogs := grants(role, "clusters", "read") && !grants(role, "pods", "logs")
		lostExec := grants(role, "clusters", "update") && !grants(role, "pods", "exec")
		if !lostLogs && !lostExec {
			continue
		}
		if repaired[role.name] {
			continue
		}
		if reason, ok := intentionalPodStreamReductions[key]; ok {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s: reduction is listed but has no reason", key)
			}
			seen[key] = true
			continue
		}
		t.Errorf("built-in role %s loses WS pod streaming (logs=%v exec=%v) with no repair and no "+
			"documented justification: either backfill it in a migration or add it to "+
			"intentionalPodStreamReductions with a reason", key, lostLogs, lostExec)
	}

	for key := range intentionalPodStreamReductions {
		if !seen[key] {
			t.Errorf("stale entry in intentionalPodStreamReductions: %s no longer loses pod streaming", key)
		}
	}
}

// TestBuiltinRolesKeepDeclaredPodStreamVerbs pins the parity half. A role whose
// rules declare pods:exec or pods:logs must actually be able to use the
// corresponding WS relay now — that is the whole point of the move, and it is
// the half that used to 403 ('Cluster Troubleshooter' holds pods:exec but not
// clusters:update).
//
// It runs the real Engine.CheckPermission the consumers call, with the exact
// argument shape they use (projectID = uuid.Nil, concrete namespace), rather
// than re-deriving "declared" from the same rules twice — a check written that
// way is a tautology that passes even with the consumers reverted.
func TestBuiltinRolesKeepDeclaredPodStreamVerbs(t *testing.T) {
	engine := NewEngine()
	clusterID := uuid.New()
	checked := 0

	for key, role := range loadSeededRoles(t) {
		binding := RoleBinding{
			RoleRules: role.rules,
			RoleName:  role.name,
			Scope:     "cluster",
			ClusterID: clusterID.String(),
			Namespace: "team-a",
		}
		for _, verb := range []Verb{VerbLogs, VerbExec} {
			declared := false
			for _, rule := range role.rules {
				if rule.Resource != "pods" {
					continue
				}
				for _, candidate := range rule.Verbs {
					if candidate == string(verb) {
						declared = true
					}
				}
			}
			if !declared {
				continue
			}
			checked++
			if !engine.CheckPermission(
				[]RoleBinding{binding}, ResourcePods, verb, clusterID, uuid.Nil, "team-a",
			) {
				t.Errorf("built-in role %s declares pods:%s but CheckPermission — the call "+
					"ExecConsumer/LogsConsumer.authorizeCluster makes — denies it", key, verb)
			}
		}
	}

	// Guard against the fixture silently stopping to exercise the contract
	// (a seed-parser change, a renamed migration). 032's 'Project Operator' /
	// 'Project Troubleshooter' and 098's 'Logging Viewer' alone clear this.
	if checked < 6 {
		t.Fatalf("only %d declared pods:logs/pods:exec grants were checked; the seeded-role "+
			"fixture is no longer exercising the parity contract", checked)
	}
}

// TestInitialSchemaContainsCanonicalPodStreamGrants ties the compatibility
// ledger directly to the final seed rather than deleted historical patches.
func TestInitialSchemaContainsCanonicalPodStreamGrants(t *testing.T) {
	roles := loadSeededRoles(t)
	for _, name := range canonicalPodStreamGrants {
		found := false
		for _, role := range roles {
			if role.name != name {
				continue
			}
			found = true
			if !grants(role, "pods", "logs") {
				t.Errorf("canonical role %q is missing pods:logs", name)
			}
		}
		if !found {
			t.Errorf("canonical role %q is not seeded", name)
		}
	}
}
