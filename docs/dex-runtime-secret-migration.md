# Dex runtime Secret migration and recovery

DEX-01 separates ownership deliberately:

- Helm/Argo owns the stable Secret identity and retention metadata, but renders
  no `data` or `stringData`.
- `DexHandler` owns only `data.config.yaml`, using an exact-name merge patch.
- Dex mounts that Secret read-only after an explicit prepare/cutover sequence.

The production preflight blocks a chart release while the retained Secret is
missing or has no `config.yaml`. This is intentional: switching the Deployment
volume before populating the Secret can take every Dex replica offline.

## Existing-install upgrade (required two-release sequence)

1. Back up the management database and the platform Fernet key. They are the
   authoritative, restorable source for connector and static-client secrets.
2. Upgrade with `dex.migration.phase=prepare`. A credential-free pre-upgrade
   / Argo `PreSync` hook first metadata-patches the original exact-name
   ConfigMap with Helm `keep` and Argo `Prune=false,Delete=false`, creates a
   metadata-only retained staging ConfigMap, then copies live
   legacy data Kubernetes-to-Kubernetes through a mode-0600 temporary patch
   file. The original object remains a restart/rollback source throughout the
   prepare phase. No credential bytes enter Helm values, rendered manifests, logs, or
   release history. The prepare Deployment stays ConfigMap-mounted.
3. Populate the metadata-only runtime Secret by calling
   `POST /api/v1/auth/dex/apply/`. Confirm its exact ownership labels and a
   semantically valid `config.yaml`; preflight uses the bounded first-party
   `dexconfigcheck` parser and never prints content. In `prepare`, `/apply`
   reports `runtime_state=staged`, advances `runtime_staged_generation`, and
   deliberately does not inspect, restart, or health-check the still
   ConfigMap-mounted Deployment. A retry after a crash reuses that same
   generation and converges on the existing Secret resourceVersion.
4. Upgrade again with `dex.migration.phase=cutover`. Preflight requires the
   durable prepare marker and valid runtime Secret. The Deployment uses
   `maxUnavailable: 0` and switches to the Secret. The bundled bootstrap
   atomically stages the cutover phase/generation and disables Dex SSO; a
   subsequent `/apply` performs rollout and health verification, reports
   `runtime_state=applied`, and conditionally advances
   `runtime_applied_generation` before SSO may be enabled.
5. The post-upgrade / Argo `PostSync` cleanup hook independently waits for the observed
   Secret-mounted Deployment to be fully ready/available, rechecks Secret
   ownership, and only then deletes both legacy ConfigMap names. A failed check
   leaves them retained for recovery and fails the release.
6. Scan API responses, audit exports, support bundles, rendered manifests, and
   the database legacy `public_clients` column for the canary credentials used
   in the rehearsal. `public_clients` must be `[]`; only the Fernet envelope may
   be populated.

### Argo CD execution

Treat `prepare` and `cutover` as two separately reviewed Git revisions and two
successful sync operations. The chart uses Helm hook annotations that Argo CD
maps to `PreSync`/`PostSync`; it intentionally does not depend on Helm's
imperative `.Release.IsUpgrade` flag. Do not use `SkipHooks`, selective sync, or
manual pruning for these revisions:

1. Commit `dex.migration.phase=prepare`, sync the whole Application, and wait
   for the prepare hooks and ConfigMap-mounted Dex Deployment to become healthy.
2. Call `/api/v1/auth/dex/apply/` through the authenticated management API and
   verify the retained runtime Secret exists. Runtime credential data remains
   outside Git and Argo's desired-state/revision history.
3. Commit `dex.migration.phase=cutover`, sync the whole Application, and wait
   for both the Secret-mounted Deployment and the `PostSync` cleanup hook.
4. If `PreSync` or `PostSync` fails, stop. Keep the failed revision and its hook
   diagnostics available; do not force a later wave or delete retained objects
   to make the Application appear healthy.

The phase value is therefore part of the deployment state machine, not a
long-lived environment preference. New installations stay on `fresh`; legacy
installations pass through `prepare` exactly long enough to establish the live
staging object, then remain on `cutover` after the verified transition.

Every API/runtime mutation also carries an opaque monotonic database
`runtime_generation`. The same decimal generation is written to the retained
Secret and Deployment pod-template annotations. Secret verification first
advances `runtime_staged_generation` with a generation-CAS update. Only after
the exact generation is observed ready and healthy is
`runtime_applied_generation` advanced with another conditional update; SSO
enablement checks the current and applied values in the same SQL statement.
A settings or connector mutation captures the prior Dex SSO state, advances the
generation, and disables Dex SSO atomically. Restoration is also generation-CAS
guarded, so a stale request can never re-enable SSO after a newer stage begins.
Every stage, logical connector mutation, activation, and restoration uses the
same PostgreSQL transaction-scoped advisory lock. Activation rechecks the
locked settings row after waiting, requires the current generation to be fully
applied outside `prepare`, and clears restoration provenance on success. A
later operator-initiated manual disable therefore remains disabled.
A stale or crashed request can therefore be retried, but it cannot overwrite a
newer Secret generation or enable an older client credential pair. Generations
are counters only and are never hashes of credential-bearing content.

## Database compatibility cutover

Migration 134 is deliberately expand-only. During the supported mixed-version
window, new code encrypts an envelope but continues to dual-write
`public_clients`; previous replicas therefore remain correct. Do not declare the
database credential migration complete merely because the envelope exists.

After old replicas are quiesced, run `keyrotate --dry-run
--dex-public-clients-cutover-confirmed`, review only counts, then repeat without
`--dry-run`. The cutover is CAS-safe, resumable, and observable through
`public_clients_cutover_at`; it never prints client JSON. Its forward-fix down
migration retains the envelope because dropping it after scrub would destroy
credentials. A later contract migration may remove the legacy column only after
the rollback window closes.

Legacy Helm values such as `dex.clientSecret` and `dex.clientSecretRef` are
rejected. Before upgrade, rotate any credential that appeared in Helm values and
remove credential-bearing historical release Secrets according to the platform
retention policy; rendering it unused does not erase Helm's stored values.

`keyrotate` is deliberately not a logical connector mutation. Migration 137
retains the compatibility trigger for direct connector CRUD from pre-137
replicas; that legacy path takes the shared advisory lock, advances
`runtime_generation`, preserves restoration provenance, and disables Dex SSO.
New staged connector CRUD and ciphertext-only `keyrotate` CAS rewrites instead
set a transaction-local, one-shot bypass before their connector DML. The
trigger consumes and clears the bypass when a row changes. Staged no-row paths
never arm it, failed statements roll it back, and a no-row `keyrotate` CAS ends
its statement transaction, so the bypass cannot leak into a pooled connection.
New logical CRUD therefore stages exactly once, while `keyrotate` changes
neither the generation nor a healthy SSO provider's enabled state.

## Fresh install

There is no old Dex service to preserve. Keep `dex.migration.phase=fresh`,
install the chart, configure settings/connectors, and call `/apply`. Fresh
preflight fails if a legacy ConfigMap exists, preventing the default from being
used as a direct-upgrade shortcut. Dex remains unready—not partially configured—
until the Secret has `config.yaml`.

## Rollback and recovery

Do not roll back to a chart version that templates Dex credentials into a
ConfigMap: Helm release history may replay the old plaintext values. During
prepare, roll back only to the prepare release so the retained staging ConfigMap
remains available. After cutover, keep the Secret-mounted Deployment and
retained Secret.
This reverses code without recreating the plaintext path.

If the Secret is lost, restore the management DB and the same Fernet key, apply
the metadata-only Secret manifest, and call `/apply`. The Secret is a derived
runtime artifact; backup correctness is proven by reconstructing it from the
encrypted DB, not by exporting its plaintext data. Rotate credentials through
the settings/connector APIs and apply twice; the second apply must be a fixed
point. Kubernetes `resourceVersion`, never a content hash, is used for rollout.
