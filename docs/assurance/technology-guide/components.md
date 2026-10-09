# Active components and ownership

[components.json](components.json) contains source inputs and graphs; [baseline.json](baseline.json) separates installed identities from defaults. Inventory is not recursive offline qualification. W01 owns stable discovery; W03 owns rendered asset closure.

| Component class | Authority / consumer | Owner / preserved responsibility | Next work and limitations |
| --- | --- | --- | --- |
| First-party binaries | `cmd/server`, `worker`, `agent`, `migrator`, `astro`, `keyrotate`, `dexconfigcheck`, Charlie qualification/deploy hooks | Backend/CLI/platform; typed construction | W01 builds every entrypoint, not only server |
| Release images | Seven roles in `internal/releasecontract/manifest.go`; Dockerfiles/Makefile | Release/platform; exact identity, SBOM, scan, signature | W01/W17; keyrotate shares migrate image; DR/shell Python/OpenSSL are additional graphs |
| Chart assets | `deploy/chart/images.txt`, values, schema and templates | Platform; PostgreSQL/CNPG, Valkey, Dex, nginx, busybox, preflight, backup/drill | No private values inspected. W03/W11 inventory operator overrides and optional profiles |
| Flux | Compatibility contract, `deploy/flux`, release distribution, agent/delivery | Delivery/platform; local convergence, three controllers | 2.9.6 distribution and pinned controller digests in JSON; readiness is not outage proof |
| Built-ins | `deploy/bundles/catalog.json` consumed by builtin provisioner | Delivery; Trivy/scanner, kube-state-metrics and node exporter | Source/version/digest/images/requirements in JSON. Public source/value wiring remains W03/W04 |
| Application catalog | Catalog producer, `internal/catalog`, typed source/trust config, airgap script | Catalog/delivery/security; signed producer/consumer identity | Two direct Python pins, no complete transitive lock. Local runtime/distribution identity W03/W15 |
| Charlie | `internal/charlie/contract/pin.json`, pinned OpenAPI/schema/authority/conformance, release artifact | Integration/security; bridge negotiation, authorization, durable actions/redaction | Live agent digest recorded separately; no artifact downloaded or verified anew |
| Database | CNPG production values/template; external PostgreSQL; development standalone | Data/platform/operator; CR/PVC/DSN/key/backup preservation | Source PostgreSQL 17.11; support 16/17. Live standalone cannot qualify CNPG. Operators/plugins are external prerequisites |
| Cache/queue | Valkey protocol, Asynq, Redis client, PostgreSQL outboxes; standalone/Sentinel/external modes | Worker/platform; queue ownership and outage recovery | Live standalone digest recorded; W12 real Sentinel/outage/reconnect tests |
| Object storage | S3 adapters/endpoint/CA overrides, backup, external Barman ObjectStore | Platform/data/operator; RustFS-compatible or qualified external S3 | No RustFS workload identified; external endpoint/version unknown without configuration. W03/W12 qualify actual supplied backend |
| Management logging | `deploy/chart/templates/management-logging*`, Fluent Bit image and typed logging config | Platform/logging; operator-owned logging settings and collector | Separate from embedded built-in catalog; functional shipper/redaction evidence W10 |
| Development telemetry | `deploy/docker-compose*.yml` and collector configs | Backend/frontend/platform | Grafana 11.5.2, Tempo 2.7.2, Loki/Promtail 3.4.2, Prometheus 3.2.1; W10 renews collectors, reuses Plan 030 |
| Installed monitoring | Helm stack 61.3.2; Grafana 11.1.0, Prometheus 2.53.1 and sidecars/exporters | Operator/platform; distinct deployment | Images in baseline; no new monitoring qualification |
| Go graph | `go.mod`1.26.6 and `go.sum`;243 declared requirements | Backend/tooling; pgx/sqlc, Helm 3 SDK, Kubernetes/OTel/auth | Declared graph is not fresh update/vulnerability output; W01 resolves latest |
| npm graph | Manifest/lock;777 package locations | Frontend/tooling; React/Vite/TanStack/static nginx | Source Node 24.21.0/npm 11.19.0. W01 updates toolchain/lock/ADR together |
| Python graph | Catalog requirements/workflow, DR/release/test helpers | Catalog/release/platform | Catalog 3.12 workflow; direct jsonschema/PyYAML pins. W15 adds full lock and equivalent gates |
| Tooling | Makefile/workflows/local-CI lock/Dockerfile/generators/scans | Platform/security/release | sqlc 1.31.1, golangci-lint 2.12.2, govulncheck 1.8.0, oapi-codegen 2.5.0, gosec 2.22.10, Gitleaks 8.27.2; W01 also inventories other invocations/actions/fixtures |
| Supporting repos | Catalog/examples/testbed/full-test-IaC SHAs and dirty paths | Catalog/QA/platform | Examples are fixtures, not shipped runtime. Preserve testbed changes; no Terraform state/private `.env` read |

Versions need separate namespaces: Trivy chart 0.37.0/operator 0.35.0; Helm CLI/embedded SDK/Flux-owned graph; CNPG operator/PostgreSQL/Barman plugin/extensions. Unknown external storage/operator/plugin identities remain unknown until an attributable safe inventory exists. Each class above has its regression story in [invariants.md](invariants.md).
