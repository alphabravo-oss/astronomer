# Production installation runbook

This runbook installs one immutable Astronomer release into a production
management cluster. It covers the management plane only: Astronomer adopts
existing Kubernetes clusters and does not provision them.

## Change boundary and owners

- Platform engineering owns the Helm release, Gateway, NetworkPolicy, and
  capacity decision.
- Database and cache owners provide HA PostgreSQL and Redis endpoints, TLS,
  backup/PITR policy, and tested credentials.
- Security owns signing-key and Fernet-key custody, release verification,
  identity integration, and break-glass access.
- The application owner supplies DNS, TLS, external identity, object storage,
  outbound proxy/CA policy, and initial RBAC mappings.

Record an approved change, maintenance window, rollback owner, and incident
channel before changing the cluster. Use the disconnected procedure in
[Air-gapped installation](../airgapped-install.md) when public release subjects
cannot be verified and pulled from the management environment.

## Preconditions

1. Select an exact stable tag and confirm it is admitted by
   `deploy/release/compatibility.yaml`. The release, chart, six first-party
   images, Flux distribution, built-in bundles, checksums, SBOMs, signatures,
   and provenance must all come from that same tag.
2. Confirm the management cluster Kubernetes minor is supported and that the
   release-qualified Gateway API CRDs and GatewayClass are installed and
   Accepted.
3. Provide HA external PostgreSQL and Redis for production. Confirm TLS,
   network reachability, connection limits, storage alerts, PostgreSQL PITR,
   and Redis recovery behavior. Bundled data services are development and
   smoke-test profiles, not an enterprise topology.
4. Reserve at least three schedulable nodes across failure domains, then size
   replicas, requests, limits, connection pools, queue concurrency, and PDBs
   from a retained scale report. Do not copy example sizing into production
   without measured evidence.
5. Prepare DNS, an externally managed TLS certificate, enterprise identity,
   an object-storage destination for management backup, and any approved
   source-resolution proxy or private CA.
6. Install Helm, `kubectl`, Cosign, GitHub CLI, `jq`, and `sha256sum` on the
   controlled release host. Authenticate with the narrow permissions required
   for this release.

## Verify the release unit

Download the exact tag into a private directory and verify checksums and the
release manifest before using any digest from it:

```bash
export ASTRONOMER_RELEASE=v1.1.0
mkdir "astronomer-${ASTRONOMER_RELEASE}"
cd "astronomer-${ASTRONOMER_RELEASE}"
gh release download "$ASTRONOMER_RELEASE" --repo alphabravo-oss/astronomer
sha256sum --check SHA256SUMS

cosign verify-blob \
  --bundle release-manifest.sigstore.json \
  --certificate-identity \
    "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/${ASTRONOMER_RELEASE}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  release-manifest.json
```

Follow [image verification](../verify-images.md) for every image and OCI
artifact. Reject mutable tags, digest mismatches, a different workflow
identity, or a manifest whose release version differs from the requested tag.

## Create secrets without exposing them

Generate the JWT signing key and Fernet encryption key once in an approved
secret system. Losing or replacing the Fernet key makes encrypted database
columns unreadable. Never put these values, database DSNs, bootstrap
passwords, registry credentials, or SSO secrets in a committed values file.

Materialize secrets through an external-secrets controller or create the
required Kubernetes Secrets from a protected operator host. Also create a
separately held wrapping-passphrase Secret for the encrypted key backup. The
wrapping secret must not share custody with the database dump and S3
credentials; see [backup and restore](management-backup-and-restore.md).

Before installation, prove the secret objects and required keys exist without
printing their values:

```bash
kubectl -n astronomer get secret astronomer-core astronomer-bootstrap \
  astronomer-key-wrap -o name
```

## Render and review

Start from the matching tag's `deploy/chart/values-production.yaml` and keep
site-specific overrides in a protected configuration repository. Set external
PostgreSQL/Redis TLS inputs, Gateway hostname/TLS, replicas, topology, resource
budgets, backup destination, key backup, restore drill, identity, and egress
policy. Use only Secret references for credentials.

Render before applying:

```bash
helm lint ./deploy/chart -f ./deploy/chart/values-production.yaml \
  -f ./production-values.yaml
helm template astronomer ./deploy/chart --namespace astronomer \
  -f ./deploy/chart/values-production.yaml \
  -f ./production-values.yaml > ./astronomer-rendered.yaml
```

Review the rendered image digests, ServiceAccounts, cluster-scoped RBAC,
NetworkPolicies, Gateway/TLS, PDBs, topology spread, resource limits, migration
and preflight Jobs, external endpoint Secret references, backup/restore-drill
CronJobs, and the absence of plaintext credentials. Run the chart verification
gate from the exact source tag when a source checkout is part of the release
evidence:

```bash
VERIFY_SCOPE=helm make verify-enterprise
```

## Install

Install the exact verified chart and wait for preflight, migration, and all
workloads. Do not use `--no-hooks` and do not bypass schema or production-value
validation.

```bash
helm upgrade --install astronomer \
  oci://ghcr.io/alphabravo-oss/charts/astronomer \
  --version 1.1.0 \
  --namespace astronomer \
  --create-namespace \
  -f ./deploy/chart/values-production.yaml \
  -f ./production-values.yaml \
  --atomic --cleanup-on-fail --wait --wait-for-jobs --timeout 20m
```

The preflight must reject an incompatible or dirty database, absent required
Kubernetes APIs, malformed secret references, unsafe production settings, and
unsupported release skew. Investigate a rejection with
[failed migration](failed-migration.md); never force the migration version.

## Validate

```bash
helm -n astronomer status astronomer
kubectl -n astronomer get deploy,statefulset,pod,job,cronjob,pdb
kubectl -n astronomer rollout status deployment/astronomer-server --timeout=10m
kubectl -n astronomer rollout status deployment/astronomer-worker --timeout=10m
curl --fail https://astronomer.example.com/health/
curl --fail https://astronomer.example.com/readyz
```

Then validate login, password rotation, SSO and group mapping, least-privilege
RBAC denial, API token scope, audit persistence, management backup destination
and on-demand backup, restore-drill scheduling, SMTP/webhook/SIEM delivery,
logging/monitoring connectivity, and a redacted support bundle.

Adopt one canary cluster with a scoped agent profile. Apply its manifest using
server-side apply, verify agent/Flux compatibility, publish one immutable
canary delivery assignment, observe acknowledgement and convergence, and then
remove the test workload. Do not onboard the production fleet until this path
and the observation window are green.

## Rollback and acceptance record

For a failed fresh install, preserve preflight/migration logs and Helm history;
let `--atomic` remove the failed revision. Do not delete an external database,
Secret, PVC, or backup as part of cleanup. For an upgrade, use the dedicated
[upgrade runbook](../upgrade-runbook.md) because application and database
rollback must be coordinated.

Retain the exact tag, chart checksum, image/artifact digests, signature results,
rendered manifest checksum, protected values reference, database/Redis/TLS
endpoints, secret custody references, capacity report, preflight and migration
results, restore proof, canary evidence, approvers, timestamps, and accepted
known limitations.
