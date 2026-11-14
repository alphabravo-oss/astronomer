# Resume development on a new machine

The application and its current working plan are recoverable from GitHub.
Development does not depend on the old `/root` workspace, local stashes, a local
Identity checkout, or the old machine's Go/npm caches. Use
[CURRENT.md](../advisor-plans/CURRENT.md) for the next tasks; archived plans are
historical specifications, not additional execution ledgers.

## Repositories and source identities

| Source | Clone / reference | When needed |
| --- | --- | --- |
| Application, chart, migrations, generated contracts and plan | `https://github.com/alphabravo-oss/astronomer.git`, `main` | Always |
| Catalog source and locked Python tooling | `https://github.com/alphabravo-oss/astronomer-catalog.git`, `main` | Catalog changes and publishing |
| Shared Identity library | `github.com/alphabravo-oss/identity`, version in `go.mod` (currently `v0.1.0`) | Go downloads the published module automatically; clone separately only when changing the library |
| Examples | `https://github.com/alphabravo-oss/astronomer-examples.git`, `main` | Example/integration work |
| Qualification infrastructure | Private `https://github.com/alphabravo-oss/astronomer-testbed.git`, default branch `master`, directory `full-test-iac/` | AWS/Hetzner fixture work; requires repository access |
| Historical Charlie experiment | Private `https://github.com/AlphaBravoCompany/charlie.git`, branch `archive/local-wip-20261020`, commit `01f18a6a03fd20b840db067406a1d3c8c4c2460b` | Preservation only; this unreviewed experiment is not the current Charlie integration baseline |

The former standalone `astronomer-full-test-iac` source and its 15-commit history
are preserved in `astronomer-testbed/full-test-iac/`, including original tip
`046992c8e96d199e9a71375deed359611aebb50b`. The older testbed root harness remains
available for reconciliation; its Argo-era assumptions are not qualified for the
current Flux implementation. No infrastructure creation is required to build
the application or run its local development services.

The application has no local Go module replacements or symlinks pointing outside
its checkout. Generated SQL/OpenAPI clients and migration sources are committed.
Frontend packages come from `package-lock.json`; editor assets are regenerated
by the `predev`/`prebuild` scripts and do not need to be copied from another node.

## Start a new development environment

Use a Linux development host with Git, Bash, Make, curl, Python 3, a C compiler
(for race tests), and Docker Engine with the Compose plugin. Install Go from the
`go` line in `go.mod` and Node from `.node-version` before running the commands
below. The verified baseline is Go **1.27.4**, Node **24.22.0**, npm **11.19.1**;
the checked-in version sources take precedence when they change.

```bash
git clone https://github.com/alphabravo-oss/astronomer.git
cd astronomer
git status --short
git rev-parse HEAD
go version
node --version
npm --version
docker compose version

go mod download
npm --prefix frontend ci
make build
npm --prefix frontend run build

# Uses deterministic local builds; Air is optional when DEV_WATCH=0.
DEV_WATCH=0 scripts/dev.sh up
scripts/dev.sh status
```

`scripts/dev.sh` creates `.env.dev.local` from the tracked development defaults,
starts the Compose services, migrates a fresh database, and starts the API,
worker and web server. It now invokes `npm run dev`, including asset generation
on a checkout where `public/monaco/` does not yet exist. Install Air separately
if choosing the watch mode. Keep this development stack on a private network:
its example credentials are public, and several default listeners bind all
interfaces. Use the production installation runbook for a deployed service.

The dashboard defaults to port 3000; the API uses port 8000. Local administrator
credentials come from `ASTRONOMER_BOOTSTRAP_EMAIL` and
`ASTRONOMER_BOOTSTRAP_PASSWORD` in the development configuration. Keep local
overrides in the ignored `.env.dev.local` file. A fresh development database
starts independently of optional SSO and does not restore old users or clusters.

For the full source gates, install the tools pinned by the workflows: currently
golangci-lint **2.15.0**, Gitleaks **8.30.1** and Helm **4.3.1**. The Makefile pins
Go-run generators/scanners. Playwright live/browser tests need their browser
binaries and the declared backend fixtures. Then run the appropriate gates:

```bash
make check
make security
make helm-lint
npm --prefix frontend run type-check
npm --prefix frontend run lint
npm --prefix frontend test -- --maxWorkers=2
npm --prefix frontend run openapi:check
```

If `/tmp` is too small, set `TMPDIR` to a private directory on a sufficiently
large filesystem. Keep the upgrade/recovery free-space guards intact. Integration
tests need real PostgreSQL/Redis through Docker; the full integration target also
downloads the selected envtest binaries. Provisioning credential tests need an
explicit disposable Kubernetes target. Missing fixtures stay visibly skipped or
blocked and must be supplied for release qualification.

Clone optional repositories beside the application when needed. From
`astronomer-testbed/full-test-iac`, `ASTRONOMER_REPO=../../astronomer` selects that
layout; use an absolute override for another layout. Follow the catalog's own
locked Python/uv setup rather than copying a virtual environment.

## Continue the current work

Read `AGENTS.md`, `CLAUDE.md`, then `advisor-plans/CURRENT.md`. S0 records the
published consolidation and portability proof. S1 owns the next implementation:
review the current shared library lifecycle APIs, then finish worker/API/catalog
provisioning and its acceptance. Local login remains mandatory; SSO is optional;
Dex stays retired. S2–S7 retain deployment, independent-estate and release work.

The [2026-11-14 portability receipt](../advisor-plans/evidence/2026-11-14-portability/README.md)
records a separate GitHub clone, empty dependency/build caches, production Go and
frontend builds, and HTTP startup/login/asset checks against fresh disposable
PostgreSQL/Redis. This was a separate checkout on the existing Linux host, not a
new operating-system installation or a live Kubernetes release qualification.

## Restore an existing installation

Git preserves source and instructions. Existing live data, keys and machine
state require separate protected backups. Before retiring the old node, verify
that another machine can access and restore:

- The authenticated management backup, encrypted database dump, encrypted key
  bundle, wrapping/recovery key and manifest; include the required historical
  encryption keys so stored credentials can still decrypt.
- Deployment-specific signing/bootstrap/internal credentials, private CA trust,
  optional Identity/client and catalog trust configuration, and protected values.
- Exact release/chart/image/catalog identities and retained artifact access.
- Workload/PVC backups and their storage ownership, where those workloads are
  part of the move; a management database backup does not recreate their data.
- Terraform state, run identity, SSH keys and provider credentials for any
  existing test estate. Empty local state must not be used to take over or
  destroy old cloud resources.

Do not put these credentials, kubeconfigs, state files or raw database dumps in
Git. Store them in an off-node secret/backup system and prove a restore using
[management backup and restore](runbooks/management-backup-and-restore.md) and
[the DR runbook](management-plane-dr-runbook.md). Local stashes and the private
reconciliation snapshots are additional local preservation, not an off-node
backup of the live installation. Their survival is not required to rebuild the
published source.

The current source is ahead of the last published application release. A new
node must not combine its schema/chart with older server or worker images.
Create and qualify a coherent signed candidate under CURRENT S2; migration 87
retires broker state and schema 88 adds the provisioning journal. Follow the
[shared Identity cutover runbook](runbooks/shared-identity-cutover.md) for an
existing database. The portability check did not establish that the current
live installation has a complete off-node recovery set; that acceptance stays
open in the working plan.
