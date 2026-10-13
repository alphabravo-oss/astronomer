# Catalog and application delivery

This runbook covers catalog synchronization, immutable source resolution,
application rollout, dependencies, drift, and system inventory. Installed
workloads continue running when the management plane or an upstream catalog is
unavailable because Flux reconciles the last accepted assignment locally.

## Entry points and support

**Platform Tools** provides guided cluster capabilities, presets and coordinated
chart installation. **Applications** provides general catalog installation and
the shared installed-release inventory. A Platform Tools release appears in
Applications with a link back to its owning management page. Baseline metrics
components remain separately managed by platform delivery.

Support is independent of the entry point. **Assured profile** identifies a
qualified exact chart, effective values, image digests, tested environments,
owner and support window. Edited values, other versions or an untested cluster
are outside that profile. **Community** means upstream application support terms
apply, without an Astronomer application assurance commitment. **Experimental**
means evaluation without a production assurance commitment. A catalog signature
or digest validates integrity; neither proves installation or functional tests.

Every installation page shows cluster identity, project context, namespaces,
release names, versions and management ownership. Namespace placement does not
limit cluster-wide permissions granted to operators, CRDs or cluster roles.
Project context is not a claim of namespace ownership; delivery ownership is
resolved as described below. Existing-release hints use the loaded inventory and
do not replace server authorization or namespace/release uniqueness checks.

Versions remain pinned until a reviewed replacement is qualified. Features,
security fixes, EOL or compatibility changes can trigger an update. The catalog
repository's `docs/ASSURANCE.md` defines evidence requirements and the daily
read-only upstream report; that report does not detect every CVE or EOL event.

## Catalog synchronization

Catalog installations in project-owned namespaces use that project's Delivery
identity. A cluster-authorized installation into an unowned namespace uses the
same cluster's system-managed `astronomer-system` project. This is delivery
ownership only: it neither assigns the namespace nor grants project-scoped
callers additional access. If that platform project is absent, assign a
non-reserved namespace to a project first. Requests queued before this ownership
contract are resolved against the same verified system project when retried.

Symptoms include `AstronomerCatalogSynchronizationFailed`, an unavailable
repository badge, or a catalog showing cached data.

1. Open **Applications → Repositories** and inspect the source URL, transport, last
   successful sync, immutable revision, index digest, and bounded error.
2. Confirm the source is HTTPS or digest-pinned OCI. A private mirror requires
   an expected SHA-256 document digest.
3. Verify the configured proxy and appended CA bundle without printing Secret
   values. Check `astronomer_catalog_sync_total` by `transport` and `result`.
4. Retry only after correcting reachability, authentication, trust, or digest
   configuration. Never delete last-known-good rows merely to clear the error.

## Stale last-known-good catalog

`AstronomerCatalogSynchronizationStale` means no successful refresh was
observed for 24 hours. Existing installations remain manageable. Confirm the
sync worker is scheduled, inspect worker/outbox health, then perform one manual
sync. In an air-gapped environment, import the verified catalog bundle and its
referenced chart/image artifacts into configured internal mirrors.

## Source resolution

Use the source detail and `astronomer_delivery_source_resolutions_total` to
separate fetch, authentication, immutable-reference, digest, and signature
failures. Rotate credentials through the source API; do not patch generated
Flux Secrets. A successful retry must produce an immutable commit, chart
archive digest, or OCI manifest digest before publication.

## Rollout or assignment failure

1. Open the application target, then its newest rollout.
2. Inspect the frozen plan digest, cohort gate, maintenance window, failure
   budget, and per-cluster state.
3. For pending assignments, check cluster connectivity, agent snapshot
   outcomes, and Flux readiness. For failed assignments, open normalized
   deployment conditions and event history.
4. Pause before changing shared policy. Retry only failed assignments when the
   immutable desired version is still correct; otherwise create a new rollout.
5. Roll back through the recorded previous immutable version. Never edit
   generated Flux objects directly.

## Dependency safety

Before removing a shared dependency, inspect its consumer references. Removal
must remain blocked while another application owns a live reference. Upgrade
dependencies before consumers when the catalog declares ordering or health
gates. Treat externally managed CRDs and operators as prerequisites, not as
resources owned by application uninstall.

## Drift

The deployment page compares desired/observed generation, revision, digest,
and Flux conditions. `detect` reports drift without correction; `repair`
allows Flux to restore declared state; `ignore` deliberately suppresses it.
If drift repeats, identify the competing field manager or controller before
retrying. Do not use force ownership to conceal a conflict.

## System inventory

Open **Delivery → System Components** for ownership, management method,
version, replicas, requests/limits, storage, compatibility, update evidence,
and observation age. Astronomer-owned components expose supported actions;
cluster and externally owned components are read-only. Stale evidence usually
indicates an agent permission or connectivity problem, not component removal.

## Support evidence

Collect the standard support bundle and include catalog/repository status,
rollout/event identifiers, normalized deployment conditions and inventory,
system-component inventory, and delivery/catalog metrics for the incident
window. Do not include credentials, Secret values, rendered Secret manifests,
or decrypted Helm values.

## Version-owned configuration

The catalog producer owns `configurations/<slug>/<chart-version>.json`. The
builder validates and embeds these records in `artifact.versions`; the application
API returns only metadata matching the repository, chart, exact version and
archive digest. Configuration files include reviewed JSON Schema, named presets,
namespace defaults, availability, recommendation, deprecation, revocation and
compatibility metadata. JSON remains the canonical contract.

The UI requests `view=curated`; filtering and exact pin resolution occur before
pagination. Cluster-scoped requests disable incompatible Kubernetes choices with
a reason. The API's omitted-view default remains `all` for existing clients.
Explicit version lookups use the exact database key, including retained pins
outside the upstream rolling window. `selected_version_id` retains an installed
version for an upgrade without making it recommended. An unavailable catalog
returns an error; it does not silently select a newer upstream release.

Both application entry points share one install editor. Common fields use the
selected version's schema. Presets show changed paths before application and
preserve other values. Changing versions preserves the entire override, including
unknown fields, arrays, Secret references and storage settings. Removed fields
remain visible in YAML; field errors must be corrected. Resetting an individual
resource reference to the chart default removes that override. Empty strings
retain their chart-specific meaning.

StorageClass, ingress-class and Secret selectors use authorized resource metadata
lists. Secret requests require the selected namespace and never request Secret
contents. Namespace suggestions are authorized and editable; the server checks
namespace ownership and mutation permission. Preview and install/upgrade enforce
catalog trust, revocation, immutable identity and both chart and catalog schemas.
Optional `upgradeFrom` constraints are enforced against the persisted source
version on mutation, and against `from_chart_version_id` during preview.

Platform Tools retain their release order, presets and ownership. Exact matching
chart versions reuse catalog constraints for every release; single-release tools
also expose the matching visual fields. Metadata from another chart version is
never reused. Other upstream versions remain an explicit advanced choice without
Astronomer configuration assurance.

### Coordinated publication

The deployment chart pins the generated canonical catalog at commit
`d02c4e145f82669284af2fe131c1ac2e24a8eb71`, with document digest
`sha256:56f31adcf7ade0d77bf91fd227cb3f52faf724180d6bd4f35e9e7e92e67cd8b4`.
This payload contains the exact-version visual configuration profiles. Future
updates must publish the reviewed generated payload or signed OCI artifact,
then update the source URL/digest and roll out the matching backend and UI.
Do not point production at a mutable branch or the unbuilt authoring file.
A verified catalog publication does not establish live workload assurance.

Longhorn 1.12.1's initial archive mismatch was reviewed against the upstream
index, GitHub asset digest and all 42 packaged files in the release source.
The corrected lock and review receipt are retained in the catalog repository's
`docs/evidence/035/`. It is available as Community-supported; no Assured claim
is made.


### Configuration pages

Application install and upgrade actions navigate to `/dashboard/catalog/configure`.
The URL carries only chart/release, project and cluster identifiers; the page
loads current authorized records rather than depending on the previous browse
page. Platform Tools use `/dashboard/clusters/$id/tools/$slug/configure`. Both
editors use normal page scrolling and a responsive action bar. Install/upgrade
opens a short confirmation dialog; cancellation leaves the configured values
intact. An accepted operation returns to its inventory with a trackable operation
ID. Unsaved values remain local to the page and are not stored in URLs.


Version profiles, including their configuration schemas and presets, are returned
on the exact `HelmChartVersion` records. Tool preview returns profiles separately
in `catalog_versions`, keyed by `namespace/release_name`. The existing chart-values
and tool chart-row shapes remain unchanged for released Go SDK clients. The UI
joins only the selected version/profile and the exact tool release key.
