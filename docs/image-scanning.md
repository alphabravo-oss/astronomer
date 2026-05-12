# Image Vulnerability Scanning

Sprint 062 wires astronomer-go to ingest VulnerabilityReport CRDs from
[Trivy-operator](https://aquasecurity.github.io/trivy-operator/) running
in each managed cluster, surface them as a per-cluster "Image scans"
tab, and roll them up across the fleet at `/dashboard/security/`.

We **do not** run scans from the management plane. The operator scans
workloads inside the cluster and writes its results as
`aquasecurity.github.io/v1alpha1/VulnerabilityReport` CRs. The CRD
mirror in our agent ships those CRs to the management plane, where
`internal/scanner.Ingester` persists them.

## Install

```
Catalog → Aqua Security → trivy-operator → Install in security namespace
```

The seed in migration `062_image_vulnerabilities.up.sql` adds the Aqua
Security helm repo + a curated `trivy-operator` chart entry. The seed is
idempotent — re-running the migration leaves operator-edited rows
intact.

Once installed, the operator starts emitting `VulnerabilityReport` CRs
on its own scan cadence (default: every 6h). The CRD mirror streams
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

See migration `062_image_vulnerabilities.up.sql`:

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
