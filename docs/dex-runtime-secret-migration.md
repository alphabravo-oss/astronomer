# Dex runtime Secret migration and recovery

DEX-01 separates ownership deliberately:

- Helm/Argo owns the stable Secret identity and retention metadata, but renders
  no `data` or `stringData`.
- `DexHandler` owns only `data.config.yaml`, using an exact-name merge patch.
- Dex mounts that Secret read-only. The compatibility ConfigMap is inert after
  cutover and must never receive `config.yaml` again.

The production preflight blocks a chart release while the retained Secret is
missing or has no `config.yaml`. This is intentional: switching the Deployment
volume before populating the Secret can take every Dex replica offline.

## Existing-install upgrade (required two-phase sequence)

1. Back up the management database and the platform Fernet key. They are the
   authoritative, restorable source for connector and static-client secrets.
2. Create the metadata-only Secret and exact-name Role/RoleBinding from the new
   chart in a preparatory Argo sync or reviewed manifest. Do not add `data` to
   Git or Helm values.
3. Populate it before chart cutover. Preferred: stage the new server image while
   retaining the old Dex Deployment volume and call
   `POST /api/v1/auth/dex/apply/`. For a one-time legacy bridge, copy the live
   ConfigMap through a mode-0600 local file; never print or commit its contents:

   ```sh
   umask 077
   tmp=$(mktemp)
   kubectl -n astronomer get configmap astronomer-dex-config \
     -o jsonpath='{.data.config\.yaml}' >"$tmp"
   kubectl -n astronomer create secret generic astronomer-dex-runtime \
     --from-file=config.yaml="$tmp" --dry-run=client -o yaml | kubectl apply -f -
   rm -f "$tmp"
   ```

   Ensure the Secret also carries `astronomer.io/runtime-writer=dex-handler`
   and `astronomer.io/secret-purpose=dex-runtime`; applying the metadata-only
   chart manifest supplies these without changing `data`.
4. Run the normal chart/Argo release. Preflight must pass before any volume
   switch. The Deployment uses `maxUnavailable: 0`, so old ConfigMap-backed
   replicas remain available until Secret-backed replicas become Ready.
5. Call `/apply` once more. Confirm the response reports `changed: false` on a
   repeat call, then verify the old ConfigMap has only `migration-notice` and no
   `config.yaml`.
6. Scan API responses, audit exports, support bundles, rendered manifests, and
   the database legacy `public_clients` column for the canary credentials used
   in the rehearsal. `public_clients` must be `[]`; only the Fernet envelope may
   be populated.

## Fresh install

There is no old Dex service to preserve. An operator may explicitly set
`dex.migration.requirePreparedConfig=false`, install the chart, configure
settings/connectors, and call `/apply`. Dex remains unready—not partially
configured—until the Secret has `config.yaml`. Re-enable the gate afterward.

## Rollback and recovery

Do not roll back to a chart version that templates Dex credentials into a
ConfigMap: Helm release history may replay the old plaintext values. For the
one-release compatibility window, roll back server/frontend/Dex image tags with
this chart while keeping the Secret-mounted Deployment and retained Secret.
This reverses code without recreating the plaintext path.

If the Secret is lost, restore the management DB and the same Fernet key, apply
the metadata-only Secret manifest, and call `/apply`. The Secret is a derived
runtime artifact; backup correctness is proven by reconstructing it from the
encrypted DB, not by exporting its plaintext data. Rotate credentials through
the settings/connector APIs and apply twice; the second apply must be a fixed
point. Kubernetes `resourceVersion`, never a content hash, is used for rollout.
