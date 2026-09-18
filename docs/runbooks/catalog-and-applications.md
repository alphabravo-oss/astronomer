# Catalog and application delivery

This runbook covers catalog synchronization, immutable source resolution,
application rollout, dependencies, drift, and system inventory. Installed
workloads continue running when the management plane or an upstream catalog is
unavailable because Flux reconciles the last accepted assignment locally.

## Catalog synchronization

Symptoms include `AstronomerCatalogSynchronizationFailed`, an unavailable
repository badge, or a catalog showing cached data.

1. Open **Apps → Repositories** and inspect the source URL, transport, last
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
