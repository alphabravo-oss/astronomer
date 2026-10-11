# Upgrading to Astronomer 2.0.0

Version 2.0.0 integrates the technology-guide implementation and compact CNPG
deployment changes. It is a major API and operational upgrade, not a claim that
every item in Plan 032 is complete.

## Before upgrading

Save the current Helm values and release manifest, take and verify a database
backup, and preserve the application's encryption and signing keys. Keep backup
files and rendered values private. The target application schema is 81; never
assume an older binary can use a database migrated by this release. Restore to a
separate database when rehearsing recovery.

The chart supports Kubernetes 1.33 through 1.35. Install the CloudNativePG
operator before selecting the CNPG profile. CNPG remains the recommended
database; Flux remains the delivery engine. Existing clusters are adopted,
not provisioned by Astronomer.

For a small installation, layer `deploy/chart/values-single-node.yaml` over
chart defaults and then apply the site's ingress, TLS, credentials and storage
settings. This profile uses one CNPG instance and development-mode application
settings. It is not HA. Production uses `values-production.yaml` with its
separate production security, replication and infrastructure requirements.

Changing `postgres.mode` does not copy an existing database. Follow
[the single-node CNPG migration runbook](single-node-cnpg.md): restore a separate
target, stop writers for the final copy, compare data, then switch the application.
After writes resume, the old database is stale; a Helm rollback alone cannot
recover the new writes. Configure off-host backups and recovery independently.

## Agent compatibility

The management plane accepts its matching 2.0 agent and retains the 1.x agent
application range for staged upgrades. Existing tunnel, heartbeat and required
capability checks still apply; an older agent that cannot satisfy those contracts
is rejected. Upgrade the management plane before deploying 2.0 agents, because
the 1.x management plane rejects them as a future major version.

## API clients

Regenerate clients from the shipped OpenAPI document and review the retained
[comparison against v1.2.1](../assurance/technology-guide/w06/v1.2.1-openapi-breaking.md).
In particular, alert inhibition rules require nonempty source and target matchers,
and rollout consumers must handle the documented state enum values. List and
catalog responses have explicit envelopes and pagination contracts; consumers
must follow continuations rather than assuming the first page is complete.

For upgrades originating before 1.2.1, browser login/refresh/TOTP flows use HttpOnly
cookies rather than JSON token fields. Keep the cookie jar and send the CSRF
header on unsafe session requests. Automation uses scoped API tokens. Use the
documented hyphenated kubeconfig endpoints and Flux delivery APIs; retired aliases
are not restored. The [API compatibility review](../api-breaking-change-review.md)
records the exact reviewed changes.

## Release evidence and limitations

The owner approved the same external certification and license deferrals for this
exact v2.0.0 tag on 2026-10-11. Raw findings, SBOMs, image signatures and automated
qualification remain required. Upstream vulnerability waivers are exact-image,
exact-finding and expiring; they acknowledge risk and do not assert remediation.
No production-scale, cloud, Rancher benchmark, human accessibility, clean-license
or full multi-host HA certification is claimed.

The local development deployment passed application smoke checks and a leadership
session-loss/replica-scaling check. After deploying release artifacts, check
readiness, login/logout, cluster inventory, delivery, metrics and project CRUD.
Check that CNPG is healthy and the running images match the signed release.

Development Promtail migration, six permissive API schemas, removal of the
selectable audit stub and repository consolidation remain tracked follow-ups.
Docker development continues to use Compose; Helm is the Kubernetes deployment.

## Browser tool origin migration

Configure [isolated tool DNS and TLS](tool-ui-isolation.md) before opening downstream UIs. Main-origin proxy URLs become launch pages; mutations use the isolated origin. HTTPS browser cookies now use host-bound names, so users must sign in again after upgrading. No CNPG data migration is required for this browser isolation change.
