# Astronomer starter Gatekeeper policy bundle

A small, safe set of OPA Gatekeeper `ConstraintTemplate` + `Constraint` manifests
the platform ships so that an installed Gatekeeper engine actually enforces a
baseline of policy instead of admitting everything.

Every constraint defaults to `enforcementAction: dryrun` (audit only): violations
are recorded in the constraint's `status.violations` and surface in the platform,
but nothing is blocked. Operators flip individual constraints to `deny` once they
have reviewed the audit results, so turning the engine on can never break a
running cluster by surprise.

Policies:

| Template | Constraint | What it flags |
|----------|-----------|---------------|
| `K8sPSPPrivilegedContainer` | `no-privileged-containers` | Pods running a privileged container |
| `K8sDisallowLatestTag` | `disallow-latest-tag` | Containers using `:latest` or an untagged image |
| `K8sRequiredLabels` | `ns-require-team-label` | Namespaces missing a `team` label |

## Delivery

The bundle is embedded (`bundle.go`) and applied automatically by the
`gatekeeper:policy_apply` reconciler (`internal/worker/tasks`), which runs on the
tunnel queue every 5 minutes: for each connected cluster that has Gatekeeper
installed (detected via the constraint-template API) it server-side-applies each
manifest through the agent tunnel. Templates apply before constraints; a
constraint whose generated CRD isn't ready yet applies on the next sweep
(idempotent). Clusters without Gatekeeper are skipped.

To apply manually instead: `kubectl apply -f internal/gatekeeperpolicy/bundle/`
