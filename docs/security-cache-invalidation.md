# Distributed security-cache invalidation

Astronomer coordinates the positive JWT-revocation and RBAC binding caches
across server replicas through the dedicated Redis channel
`astronomer:security-cache-invalidation:v1`. This channel is separate from
user-visible events. Its versioned messages contain only an invalidation kind,
a non-secret JTI or user identifier, a monotonic epoch, pod origin, and time.
JWTs, cookies, API tokens, role rules, passwords, and secret values are never
published or logged.

Each committed security reduction invalidates the local cache first, increments
the Redis epoch for the JWT or RBAC domain, then publishes the targeted message.
Every replica subscribes and also reconciles both epochs every 500 milliseconds.
An epoch gap or a missed Pub/Sub message causes conservative domain-wide local
invalidation.

If subscription or epoch reconciliation fails, the coordinator becomes
unhealthy. While unhealthy, JWT and RBAC positive cache hits are bypassed and
the authoritative database is queried. On reconnect, both local caches are
invalidated before positive cache hits are re-enabled. Multi-replica readiness
remains degraded until initial epoch state has been established. Explicit
single-replica development can use local-only caching and emits a startup
warning.

## Mutation inventory

| Mutation | Invalidation |
| --- | --- |
| User logout | JWT JTI and user cutoff |
| Admin force logout | JWT user |
| Password change, admin reset, or reset-token completion | JWT user |
| User deactivation/deletion, including SCIM | JWT user; deletion also RBAC user |
| Global, cluster, or project role create/update/delete | RBAC all |
| Global, cluster, or project binding create/delete | RBAC user |
| SSO/group resynchronization | RBAC user |
| Project namespace ownership change | RBAC all |

Publication failure never rolls back an already committed database mutation.
It marks the coordinator unhealthy, increments the failure metric, and forces
authoritative database reads until Redis reconciliation succeeds.
