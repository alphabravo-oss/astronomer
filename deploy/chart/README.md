# Astronomer management-plane chart

This chart installs the Astronomer 1.0.0 API, worker, web application, database
migration job, and optional supporting services. It is the only supported
management-plane installation path for the v1 release line.

The chart is dependency-free. Flux runs in managed clusters and is distributed
as a separately signed, digest-pinned release artifact. Installing this chart
never installs delivery controllers in the management cluster.

## Support contract

| Contract | Supported value |
|---|---|
| Chart and application | 1.0.0 |
| Kubernetes | 1.33 through 1.35 |
| Agent protocol | 2 |
| Flux | 2.9.3 |
| Database | Empty PostgreSQL database, or an existing clean v1 schema |
| Upgrade source | A previous v1 tagged release |

Astronomer v1 is a greenfield release. It does not upgrade a pre-v1 database,
translate old delivery state, or adopt old delivery custom resources. Keep an
old installation intact for rollback and audit, take a verified backup, then
install v1 with a new release name and an empty database.

The compatibility declaration in `deploy/release/compatibility.yaml` is the
release authority. Packaging checks must keep this chart's admission bounds in
sync with that file.

## Install

Create the secrets before invoking Helm. This first command is a runnable local
or evaluation install using the bundled single-node data services:

```bash
kubectl create namespace astronomer
kubectl -n astronomer create secret generic astronomer-core \
  --from-literal=SECRET_KEY='<jwt-signing-secret>' \
  --from-literal=ASTRONOMER_ENCRYPTION_KEY='<fernet-key>'
kubectl -n astronomer create secret generic astronomer-bootstrap \
  --from-literal=password='<initial-admin-password>'

helm upgrade --install astronomer ./deploy/chart \
  --namespace astronomer \
  --set secrets.existingSecret=astronomer-core \
  --set bootstrap.existingSecret=astronomer-bootstrap \
  --set gateway.enabled=false \
  --wait --timeout 15m
```

With routing disabled, use a local port-forward or install the supported
Gateway stack before enabling `gateway`. The production profile enables
Gateway routing and requires its hostname and TLS inputs.

The exact Secret key names accepted by `secrets.existingSecret` are documented
beside the `secrets` values. Prefer an external secret controller or an
encrypted secret workflow; do not commit plaintext production values.

Production must use managed or highly available PostgreSQL and Redis,
externally managed TLS, digest-pinned first-party images, signed delivery
artifacts, narrow dependency/source egress, enterprise identity, and working
backup/key-custody settings. Start with `values-production.yaml`; its schema is
deliberately incomplete until every site- and release-specific requirement is
provided. The bundled PostgreSQL and Valkey StatefulSets are not a production
topology.

## Clean v1 upgrades

Tagged v1 releases are normal Helm upgrades. The supported helper verifies the
release provenance, checks capacity and disk, backs up state, performs a
server-side dry run, and then upgrades atomically:

```bash
./scripts/upgrade-release.sh v1.0.1
./scripts/upgrade-release.sh --yes v1.0.1
```

See `docs/upgrade-runbook.md` for external-database confirmations, rollback,
capacity policy, and acceptance evidence.

Release automation must publish immutable image digests, the chart archive,
the downstream controller distribution, compatibility manifest, SBOMs,
signatures, and checksums as one atomic release. Before promotion, render with
the exact production values, verify every signature and digest, run the
preflight job, then canary the management plane. Rollback stays inside the v1
release line and must use a database snapshot compatible with the target tag.

Do not use `--no-hooks`: the preflight and migration hooks are release safety
controls. Do not bypass schema skew validation except for a reviewed release
that contains no database change.

## Fresh-install preflight

`preflight.enabled=true` and `preflight.freshInstall.required=true` are fixed
v1 safety settings. For external PostgreSQL, the read-only hook:

1. proves the configured DSN is reachable;
2. accepts a truly empty `public` schema;
3. accepts one clean migration row at schema version 1 only when all required
   v1 delivery tables exist;
4. rejects dirty, unknown, multi-row, pre-v1, or recognizable old delivery
   schemas; and
5. never mutates the database or deletes a persistent volume.

The hook also validates required Kubernetes APIs, ingress or Gateway API
prerequisites, TLS inputs, and configured proxy or CA Secret keys. A failure is
actionable and leaves existing data untouched.

The bundled development database cannot be queried by a pre-install hook
because its StatefulSet does not exist yet. The migration job initializes that
new volume at v1.

## Flux-native delivery contract

`delivery.enabled` is always true. It is an enablement invariant, not an engine
selector. Astronomer resolves declared sources centrally, creates immutable
deployment assignments, and receives normalized controller status through the
agent protocol. Agents install and manage the pinned Flux distribution in each
managed cluster.

The management plane does not require direct Kubernetes credentials for
managed clusters. Provider lifecycle hooks are typed operations implemented by
the agent/provider boundary—such as infrastructure readiness, load balancer
discovery, DNS publication, certificate readiness, and deprovision approval.
They are orchestrated through durable delivery state rather than by installing
a second cluster controller in the management plane.

### Compatibility

```yaml
delivery:
  compatibility:
    kubernetes:
      minimumMinor: "1.33"
      maximumMinor: "1.35"
    agentProtocol:
      minimum: 2
      maximum: 2
    flux:
      version: v2.9.3
```

Enrollment must refuse clusters outside those bounds before installing any
controller assets. Version changes belong in a tagged release and compatibility
manifest, never in an ad-hoc live override.

### Signed artifacts

Online installations use OCI repositories; disconnected installations use a
release-bundled file. Both forms require an exact `sha256` digest and a verified
signature identity.

```yaml
delivery:
  artifacts:
    fluxDistribution:
      ociRepository: ghcr.io/example/astronomer/flux-distribution
      digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      disconnectedAssetPath: ""
      trustPolicy:
        requireSignature: true
        certificateIdentity: https://github.com/example/repo/.github/workflows/release.yaml@refs/tags/v1.0.0
        oidcIssuer: https://token.actions.githubusercontent.com
    builtInBundles:
      ociRepository: ghcr.io/example/astronomer/bundles
      digest: sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789
      trustPolicy:
        requireSignature: true
        certificateIdentity: https://github.com/example/repo/.github/workflows/release.yaml@refs/tags/v1.0.0
        oidcIssuer: https://token.actions.githubusercontent.com
```

Mutable OCI tags, unsigned artifact policies, non-HTTPS issuers, and malformed
digests fail values-schema validation. Artifact credentials are accepted only
through Secret references in the runtime API; this chart has no plaintext
artifact-password values.

For disconnected media, set `disconnectedAssetPath` to an absolute path in the
verified release payload and retain the same digest and trust policy. Release
preparation is responsible for mounting that asset where the worker can read
it.

### Registry mirrors

Registry rewrites are exact host-to-host mappings. Wildcards, repository-prefix
rewrites, embedded credentials, and URL paths are rejected.

```yaml
delivery:
  artifacts:
    publicRegistry: ghcr.io
    privateRegistry: registry.internal.example.com
    registryRewrites:
      ghcr.io: registry.internal.example.com
      docker.io: registry.internal.example.com
```

For a tagged release, generate these values with `scripts/mirror-release.py`
instead of writing them by hand. The utility consumes the signed release
manifest, mirrors every management/downstream/Charlie subject, verifies exact
destination digests, emits the mapping plus digest-pinned Helm overrides, and
signs the mapping after successful copy. See
[`docs/airgapped-install.md`](../../docs/airgapped-install.md).

`image.registry` remains a development convenience. Production mirror values
use per-image registry/repository/digest fields so Docker Hub library paths and
other upstream layouts cannot be accidentally rewritten to the wrong target.

### Source egress, proxy, and private CAs

Source resolution runs in the worker. NetworkPolicy egress CIDRs are an
additional containment boundary; application-level URL validation remains the
authority for SSRF, redirects, DNS rebinding, and reserved address ranges.
Production rejects world-open CIDRs.

```yaml
delivery:
  sourceResolution:
    workerConcurrency: 8
    maxArtifactBytes: 536870912
    maxHelmChartBytes: 104857600
    egressCIDRs:
      - 203.0.113.0/24
    allowSSH: false
    proxy:
      enabled: true
      urlSecretRef:
        name: source-proxy
        key: url
    ca:
      existingSecret: source-ca
      key: ca.crt
```

The preflight verifies referenced Secret keys without printing their values.
The proxy URL is projected directly from the Secret. The CA bundle is mounted
read-only. Enabling SSH opens port 22 only to the declared CIDRs.

### Rollout safety and status retention

```yaml
delivery:
  rollout:
    workerConcurrency: 16
    maxActiveRollouts: 100
    maxClustersPerRollout: 5000
    maxConcurrentClustersGlobal: 200
    defaultFailureBudget: 1
  status:
    retentionDays: 30
    historyPerDeployment: 100
    coalesceWindowSeconds: 5
  monitoring:
    assignmentAckSecondsP99: 60
    fluxTransitionVisibilitySecondsP99: 30
    rolloutSchedulerSecondsP99: 15
    staleClusterStatusSeconds: 180
```

Keep concurrency below the tested database, queue, and provider rate limits.
Use explicit waves, approvals, and failure budgets for large or high-risk
rollouts. Status coalescing reduces event volume but does not change durable
assignment or audit semantics.

## Networking and TLS

Use either Gateway API (`gateway.enabled`) or Ingress (`ingress.enabled`), not
both. Production should supply an existing TLS Secret or a supported
cert-manager issuer. Set `config.serverURL`, allowed hosts, and CORS origins to
the same externally reachable HTTPS origin.

The supported Gateway stack is the Gateway API v1.5.1 standard CRD bundle
paired with NGINX Gateway Fabric 2.6.0. Install those exact versions before the
chart when Gateway routing is enabled; the preflight verifies CRD and
GatewayClass existence.

Default-deny NetworkPolicies cover the management components. Add only the
documented database, cache, DNS, identity-provider, object-storage, source, and
observability destinations. Validate policies against the actual CNI because
NetworkPolicy behavior is implementation dependent.

## CRD ownership

The chart owns only the generic management CRDs `Cluster`, `Project`, and
`AgentProfile` when `crds.install=true`. Delivery sources, bundles, targets,
rollouts, assignments, and status are management-plane API/database resources;
they are not management-cluster CRDs. Downstream Flux resources are owned by
the agent-managed controller installation in each managed cluster.

Disable `crds.install` only when a platform team manages those three generic
CRDs separately. Their installed version must match the chart release before
the server and worker are upgraded.

## Air-gapped operation

`images.txt` is the authoritative management-plane image inventory and also
records the digest-pinned downstream controller images shipped with the
release. Mirror the complete list, preserve digests, verify signatures, and set
`image.registry` plus the exact delivery registry rewrite map.

The chart itself needs no repository download. Package and validate it with:

```bash
helm dependency list deploy/chart
helm lint deploy/chart \
  --set bootstrap.existingSecret=bootstrap-credentials \
  --set secrets.existingSecret=core-credentials
helm template astronomer deploy/chart \
  --namespace astronomer \
  --set bootstrap.existingSecret=bootstrap-credentials \
  --set secrets.existingSecret=core-credentials >/tmp/astronomer-rendered.yaml
```

For production validation, layer `values-production.yaml` and provide the
required external service, hostname, TLS, identity, and signed artifact values.

## Operational verification

After install or upgrade:

```bash
kubectl -n astronomer rollout status deployment/astronomer-server --timeout=10m
kubectl -n astronomer rollout status deployment/astronomer-worker --timeout=10m
kubectl -n astronomer get pods,jobs
curl --fail https://astronomer.example.com/health/
curl --fail https://astronomer.example.com/readyz
```

Confirm assignment acknowledgement latency, controller-transition visibility,
rollout scheduler latency, stale cluster status, worker queue depth, database
pool saturation, API errors, and agent connectivity before promoting the
release. Backups and restore drills must include the management database,
encryption keys, release metadata, and external secret-manager references.

## Chart development checks

```bash
jq empty deploy/chart/values.schema.json
helm lint deploy/chart \
  --set bootstrap.existingSecret=bootstrap-credentials \
  --set secrets.existingSecret=core-credentials
helm template astronomer deploy/chart \
  --set bootstrap.existingSecret=bootstrap-credentials \
  --set secrets.existingSecret=core-credentials >/tmp/astronomer.yaml
go test ./deploy -run 'TestAstronomerChart'
git diff --check -- deploy/chart deploy/chartrepo.go deploy/chartrepo_test.go
```

A release is not complete until online and disconnected installs have both
passed, a newly enrolled test cluster reaches Ready using the pinned downstream
distribution, an application rollout converges, status reaches the UI, and a
clean Helm upgrade from the previous v1 tag succeeds.
