# Image Vulnerability Scanning

Astronomer ingests VulnerabilityReport CRDs from
[Trivy-operator](https://aquasecurity.github.io/trivy-operator/) running
in each managed cluster, surface them as a per-cluster "Image scans"
tab, and roll them up across the fleet at `/dashboard/security/`.

We **do not** run scans from the management plane. The operator scans
workloads inside the cluster and writes its results as
`aquasecurity.github.io/v1alpha1/VulnerabilityReport` CRs. The CRD
mirror in our agent ships those CRs to the management plane, where
`internal/scanner.Ingester` persists them.

## Install

Remote clusters automatically receive the release-pinned Trivy Operator through
Flux, including existing Ready clusters and registrations with Quick Start off.
Installation waits for authenticated, ready, compatible Flux inventory. Uncheck
**Enable image vulnerability scanning** during registration to opt out. API
callers can set the `astronomer.io/image-scanning: disabled` cluster annotation
before installation. See [Platform baseline](platform-baseline.md).

On clusters that are already Ready, scanner installation runs as a normal
system delivery without reopening registration. Progress, failures, and retries
are visible in delivery. Opting out after installation prevents automatic
installation but does not remove existing releases or reports; remove the
scanner through its owning delivery workflow when removal is desired. The
management cluster currently does not expose Image Scans.

The baseline enables image vulnerability scanning only, with one concurrent
scan job and a 24-hour report TTL. It is not runtime intrusion prevention.
Scans require access to image registries and Trivy vulnerability databases;
sideloaded local images without a reachable registry may not be scanned.

Once installed, the operator emits `VulnerabilityReport` CRs as workloads are
reconciled and reports expire. The CRD mirror streams
them to the management plane; ingestion is idempotent on
`(cluster_id, report_name)`.

## Endpoints

Cluster-scoped (requires `clusters:read`):

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/clusters/{cluster_id}/vulnerabilities/summary/` | Severity rollup + last_scanned_at |
| GET | `/api/v1/clusters/{cluster_id}/vulnerabilities/images/` | Paginated top-vulnerable images (optional `?namespace=`) |
| GET | `/api/v1/clusters/{cluster_id}/vulnerabilities/reports/{id}/` | Single report + per-CVE list (filterable `?severity=`) |
| POST | `/api/v1/clusters/{cluster_id}/vulnerabilities/rescan/` | Nudges the trivy-operator service (nil-safe when operator is missing) |

Fleet-wide (requires `security:read`):

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/security/vulnerabilities/summary/` | Fleet severity rollup |
| GET | `/api/v1/security/vulnerabilities/top-clusters/` | N worst clusters by Critical+High |

## Schema

The scanner persists:

- `image_vulnerability_reports` — one row per (cluster, namespace,
  workload, container, image-digest). Aggregate counts stored eagerly
  so the rollup queries are a single index scan.
- `image_vulnerabilities` — per-CVE rows. A re-ingest of the same
  `report_name` REPLACES the CVE rows inside a single tx — operators
  never see a mix of stale + fresh CVEs for the same report.

## Metrics

- `astronomer_image_vulns_total{cluster, severity}` — gauge refreshed on every ingest.
- `astronomer_image_vuln_reports_ingested_total{outcome}` — counter, outcome `ingested` or `error`.

## Audit

Audit actions emitted on ingest + rescan:

- `vulnerability.report.ingested`
- `vulnerability.rescan.requested`
