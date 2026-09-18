# Astronomer Apps: Routed Catalog and Installation Product Design

- **Status:** Immediate product slice implemented and deployed (Helm revision 36)
- **Date:** 2026-08-26
- **Related implementation plan:** [Rancher-like Apps and Flux Delivery](2026-08-26-rancher-like-apps-implementation-plan.md)

## Outcome

Astronomer Apps presents one catalog data set through a cluster-scoped,
Rancher-like product experience without copying Rancher's proprietary chart
extensions or exposing Flux as the end-user model. Catalog discovery, install,
upgrade, rollback, uninstall, and progress remain backed by Astronomer's
namespace-aware RBAC, optional project policy, and Flux delivery engine.

## Navigation

The cluster sidebar is the only Apps navigation surface:

```text
Apps
├── Charts
├── Installed Apps
├── Operations
└── Repositories
```

Nested Installed/Browse/Recommended/Repositories tabs are removed. Recommended
applications are a featured shelf or filter on Charts, not a separate route.
Fleet-wide catalog administration can remain in Settings; cluster Repositories
shows the caller-visible source union for the selected cluster.

## Source presentation

One underlying chart collection is classified and filterable as:

1. **Astronomer First Party** — applications published and supported by
   Astronomer, beginning with Constellation.
2. **Astronomer Curated** — selected upstream applications with Astronomer
   metadata, compatibility guidance, and recommended configuration.
3. **Community** — other charts from enabled default repositories.
4. **Custom** — user-added repositories, with a stable distinct color and the
   repository name as the visible category.

This classification must not duplicate charts. Featured, category, repository,
and source-family filters are independent views over the same records.

## Chart detail route

Selecting a chart opens a full page at:

```text
/dashboard/clusters/:clusterId/apps/charts/:chartId
```

The page includes a prominent Install action, description and README, versions
and application versions, repository and support ownership, maintainers,
documentation and source links, compatibility, resource and storage estimates,
privileged/CRD/dependency guidance, lifecycle capabilities, and immutable
artifact/catalog verification information.

## Installation wizard

Install is a routed workflow at:

```text
/dashboard/clusters/:clusterId/apps/charts/:chartId/install
```

The workflow has four explicit stages:

1. **Target** — cluster, chart version, release name, and an RBAC-authorized
   namespace. Any project ownership is derived from that namespace.
2. **Configure** — robust common-value forms generated from standard
   `values.schema.json`, optionally refined by Astronomer catalog presentation
   metadata. Charts without a usable schema remain fully installable.
3. **Review values** — the complete effective values document that will be
   submitted, editable as raw YAML for settings not exposed by the form, with
   validation and a summary of target and immutable identities.
4. **Preflight and deploy** — server-owned compatibility, privilege, trust,
   resource, and storage checks followed by explicit confirmation.

Astronomer does not adopt Rancher's `questions.yml` as its product contract.
Standard Helm JSON Schema is portable; an optional Astronomer `uiSchema`
overlay can organize and annotate upstream values without forking their charts.

## Deployment progress

The accepted catalog operation remains visible after submission. The UI follows
its server-recorded events through request validation, Flux source/application
materialization, reconciliation, workload readiness, and completion. It shows
current state, timestamps, errors, retry when authorized, and a link to the
installed release. The progress view can continue polling in the background and
must not report success merely because the request was queued.

## Cluster, namespace, and project semantics

The Apps experience is cluster-scoped and never requires a project selector.
Projects remain an internal grouping and policy mechanism for namespaces; they
are not a prerequisite for browsing charts or a user-facing install target.

The server must enforce all of the following:

- the caller can browse the selected cluster's global sources and only those
  private sources granted through effective RBAC;
- the caller can install into the selected namespace;
- project identity is inferred from authoritative namespace membership when
  one exists, and an unassigned namespace remains valid for cluster-authorized
  callers; and
- the selected chart/version is visible, compatible, trusted, and not revoked.

The UI offers namespaces visible to the caller. It cannot replace server-side
authorization. Installation records retain inferred project identity when one
exists so history, quotas, policy, and ownership remain unambiguous without
forcing the user to choose it.

Every cluster receives system-managed **Default** and **System** projects. The
Default project initially owns the `default` namespace when it is unassigned;
the System project initially owns `kube-system`, `kube-public`, and
`kube-node-lease` when they are unassigned. Existing ownership is never stolen.
Operators can create additional projects and move namespaces through the normal
project lifecycle.

## First-party Constellation

Constellation is the first Astronomer First Party catalog application. Its
entry includes an immutable Helm artifact, logo, descriptions, support and
documentation links, version/release metadata, compatibility, resource and
storage guidance, lifecycle policy, and a meaningful `values.schema.json` form
covering ingress/TLS, sizing/HA, persistence, PostgreSQL/CNPG, Valkey, scanning,
and bootstrap administration while preserving expert YAML editing.

## Air-gap behavior

Catalog sync failures preserve the last-known-good verified catalog and never
prevent management of installed applications. A fresh environment without a
reachable catalog displays an actionable unavailable state rather than broken
cards or images. Custom internal HTTPS/OCI sources, mirrors, proxy settings,
and private CA trust remain supported.
