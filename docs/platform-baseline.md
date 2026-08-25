# Platform baseline

`deploy/bundles/catalog.json` is the sole membership and artifact contract for
the v1.1 platform baseline. A component is part of the baseline only when it is
present in that catalog with `default_enabled: true`. The catalog pins the chart
version, chart archive SHA-256, enabled workload image digests, target namespace,
release name, Kubernetes range, and required delivery capabilities.

The current v1.1 baseline contains exactly two components:

| Component | Chart version | Namespace | Release name |
| --- | --- | --- | --- |
| `kube-state-metrics` | `8.0.0` | `astronomer-monitoring` | `kube-state-metrics` |
| `prometheus-node-exporter` | `4.56.1` | `astronomer-monitoring` | `prometheus-node-exporter` |

The release manifest, built-in bundle archive, air-gap image inventory, and
runtime provisioner all consume this catalog. Database `cluster_tools` rows,
Helm repository seeds, cluster templates, documentation lists, and UI labels do
not add components to the baseline.

## Registration and reconciliation

Astronomer imports an existing Kubernetes cluster; it does not provision
clusters. The registration API records an explicit `install_baseline` choice.
When that choice is `true`, baseline delivery waits until the authenticated
cluster agent reports a Ready, compatible local Flux inventory. The management
plane then creates ordinary immutable delivery sources, bundle versions,
targets, and rollouts for the catalog's enabled components.

The agent materializes the assignments as Flux Helm resources in the managed
cluster. Flux performs in-cluster reconciliation and continues enforcing the
last accepted generation during management-plane outages. Astronomer does not
use Rancher Fleet, does not auto-attach a platform-default cluster template,
and does not install the baseline through an imperative Helm-over-tunnel path.

Operators inspect the resulting resources through the normal delivery APIs:

- `GET /api/v1/delivery/bundles/`
- `GET /api/v1/delivery/targets/`
- `GET /api/v1/delivery/rollouts/`
- `GET /api/v1/delivery/deployments/`
- `GET /api/v1/delivery/clusters/{clusterId}/inventory/`

Registration reaches `ready` only after every enabled built-in target reports a
successful deployment. A failed immutable rollout is visible through the same
delivery and registration status APIs and requires an explicit retry.

## Optional tools are not baseline components

The Tools catalog and UI also expose optional integrations such as
`trivy-operator`, `fluent-bit`, `cert-manager`, `ingress-nginx`, and
`gatekeeper`. Their presence in the database tool catalog is not an implicit
installation promise, and registration does not install them unless they are
promoted into `deploy/bundles/catalog.json` as reviewed, default-enabled
components.

Promotion is a release-engineering change. It requires an exact chart version,
verified chart archive SHA-256, immutable multi-platform digest coverage for
every image enabled by the shipped values, supported-Kubernetes qualification,
license and vulnerability review, reproducible bundle verification, complete
air-gap inventory, and regeneration of the signed release manifest. Guessed
versions, mutable tags, or documentation-only membership changes are not valid
baseline updates.

## Release verification

Build and verify the catalog-backed artifact with:

```bash
./scripts/build-builtin-bundles.sh --output dist/astronomer-builtin-bundles-v1.1.0.tar.gz
./scripts/build-builtin-bundles.sh --check dist/astronomer-builtin-bundles-v1.1.0.tar.gz
make release-contract-check
```

The fresh-cluster qualification lane must derive its expected release names and
namespaces from `deploy/bundles/catalog.json`. Component-specific checks, such
as waiting for Trivy vulnerability reports, are valid only when that component
is default-enabled in the signed catalog or when the test explicitly installs
it as an optional tool.
