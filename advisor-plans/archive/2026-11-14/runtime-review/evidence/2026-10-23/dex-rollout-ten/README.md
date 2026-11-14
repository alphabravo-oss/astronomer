> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Dex real rollout — ten-task batch, 2026-10-23

> **Scope correction (2026-10-23): historical Dex work, superseded.** The owner
> requires Dex removal and adoption of the existing AlphaBravo Identity component.
> Follow [Plan 032 §20.13](../../../../032-technology-guide-alignment-and-latest-stable-master-plan.md#2013-shared-identity-adoption-and-dex-retirement--scope-correction-2026-10-23).
> These test results remain evidence of the tested source, not release requirements
> or permission to continue Dex hardening. Preserve uncommitted work for selective
> disposition; do not merge it wholesale.

Source: `/root/work/astronomer-fixes`, local uncommitted work on
`fix/2.1-runtime-verification`. Plan 032 §20.12 owns the task list.

Implemented:

1. Opt-in real Kubernetes/PostgreSQL worker harness, restricted to disposable `k3d-dex-contract-*` contexts and a unique namespace; namespace deletion uses UID preconditions.
2. Readiness recognizes the chart's Secret → init container → emptyDir → Dex mount chain.
3. Worker health routing and chart readiness/liveness probes follow the issuer path.
4. Queued registration rolls actual Dex and enables the matching provider.
5. Fresh unchanged-generation Apply makes no Kubernetes writes or rollout.
6. Metadata repair preserves foreign Secret labels and data.
7. A deliberately invalid Dex command makes rollout fail closed; repair and retry converge.
8. Connector mutation restores a previously enabled provider after rollout.
9. A manually disabled provider remains disabled after Apply.
10. Superseded operations write nothing to Kubernetes; coverage is reconciled, gates recorded, disposable infrastructure removed.

Readiness also requires total replicas to equal desired replicas: an old healthy
pod cannot mask an unready replacement during surge. The pre-fix regression
failed both chart mounting and old-pod surge checks; retained in
`readiness-before-fix.log`.

Verification:

- `real-rollout-fixed.log`: race-enabled real lifecycle, all seven scenarios pass,
  plus ten readiness cases. Kubernetes v1.35.7+k3s1; Dex v2.45.1;
  PostgreSQL 17.11. No successful transport responses are mocked.
- `focused-contracts.log`: handler and chart path/readiness tests pass.
- `make-check-final.log`: lint, vet, formatting, four production builds and
  race-enabled suite pass: **15,796 total, 15,782 passed, 14 skipped, zero failures/errors**.
- `security.log`: gosec zero findings; govulncheck zero reachable findings and
  one module-only finding. Full history scan (2,304 commits) and changed-source
  secret scan both pass.
- `source-manifest.json`: all 108 source hashes verified unchanged at completion.
  `verification-summary.json` records final counts. The live run preceded only
  a response-close lint fix and the sequential-subtest lint explanation; production
  source is identical to that live run.
- `cluster-delete.log`, `clusters-after.log`: owned disposable cluster deleted;
  identity-dev, site-demo, ui-demo and ui-demo-v211 preserved. Owned private kubeconfig removed.

The first two live harness attempts failed setup (ephemeral API port/fullname,
then admission-envelope decoding); these are retained, not counted as production
regression failures. The first full check identified unchecked response close and
missing documentation of intentionally sequential lifecycle subtests. Both test
harness lint issues were corrected; `make-check.log` is retained.

Scope: component qualification on this shared host, not independent-host estate
qualification or external IdP login. No source changes are committed, pushed or
deployed to management/demo clusters. See `coverage-map.md` for remaining first
registration, registration-specific failure and finalization fault scenarios.
The 174 lifecycle cases and prior release/DR, Charlie, and pagination blockers
are not closed by this batch.

## Reproducing the real component test

Create a fresh disposable k3d cluster whose context starts with
`k3d-dex-contract-`, using `rancher/k3s:v1.35.7-k3s1`, one server, no agents,
no load balancer and Traefik disabled. Do not switch the default kube context.
Export its kubeconfig to a private mode-0600 file. When using an ephemeral
`--api-port 127.0.0.1:0`, check the Docker published port: this k3d build emitted
port 0 in its kubeconfig and required correction to the actual published port.

Preload Dex `ghcr.io/dexidp/dex:v2.45.1` (resolved digest
`sha256:8499afd690c437f52301efd2b05b2455da5bd2dfc20332cd697dc9937f808462`)
and the chart's BusyBox image if necessary. This host's Docker-to-k3d multiarch
archive import reported success despite a missing child digest; a native
containerd pull inside the disposable server resolved the image correctly.
Both failed setup logs are retained alongside successful pull evidence.

Run from the source worktree, setting the private kubeconfig path:

```sh
DEX_ROLLOUT_TEST_KUBECONFIG=/path/to/private-owned-kubeconfig \
TMPDIR=/root/work/astronomer-test-tmp \
go test -race -tags integration ./internal/handler \
  -run '^TestDexWorkerRealChartRollout$' -count=1 -v
```

The test renders only Dex RBAC, Secret, Deployment and Service from the actual
chart, creates its own namespace, starts a PostgreSQL testcontainer, runs the
ordered lifecycle and deletes the namespace with a UID precondition. Cluster RBAC
is scoped by its random release name but remains until the entire disposable
cluster is deleted. Always delete that exact owned cluster after the test; remove
its private kubeconfig after confirming deletion. Never reuse a demo cluster.
