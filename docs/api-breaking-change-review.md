# Reviewed OpenAPI contract corrections

This file is both the human review record and the exact `oasdiff` exception
input. Exceptions are narrow method/path/change triples; unrelated future
changes still fail. These entries correct previously incomplete OpenAPI
contracts to match validation that the server already enforced. They do not
authorize changing runtime behavior. The catalog sync entry records the
intentional move to durable asynchronous acceptance (`202`) so a request is
never reported complete before its committed worker intent runs.

Reviewed 2026-08-23 for the v1 contract hardening release:

- POST /api/v1/alerting/channels/ added required request body
- GET /api/v1/catalog/charts/{id}/versions/ the response's body `type` changed from `array<object>` to `object` for status `200`
- POST /api/v1/catalog/installed/ removed the optional property `installation` from the response with the `202` status
- POST /api/v1/catalog/installed/ removed the optional property `operation` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `attemptCount` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `completedAt` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `createdAt` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `errorMessage` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `id` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `operationType` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `startedAt` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `status` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `targetKey` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `targetType` from the response with the `202` status
- DELETE /api/v1/catalog/installed/{id}/ removed the optional property `updatedAt` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `attemptCount` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `completedAt` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `createdAt` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `errorMessage` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `id` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `operationType` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `startedAt` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `status` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `targetKey` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `targetType` from the response with the `202` status
- POST /api/v1/catalog/installed/{id}/rollback/ removed the optional property `updatedAt` from the response with the `202` status
- PUT /api/v1/catalog/installed/{id}/upgrade/ removed the optional property `installation` from the response with the `202` status
- PUT /api/v1/catalog/installed/{id}/upgrade/ removed the optional property `operation` from the response with the `202` status
- POST /api/v1/rbac/cluster-roles the request property `rules/items/verbs` became required
- PUT /api/v1/rbac/cluster-roles/{id} the request property `rules/items/verbs` became required
- POST /api/v1/rbac/global-roles the request property `rules/items/verbs` became required
- PUT /api/v1/rbac/global-roles/{id} the request property `rules/items/verbs` became required
- POST /api/v1/rbac/permission-preview the request property `rules/items/verbs` became required
- POST /api/v1/rbac/project-roles the request property `rules/items/verbs` became required
- PUT /api/v1/rbac/project-roles/{id} the request property `rules/items/verbs` became required
- POST /api/v1/admin/network-policy-templates added the new required request property `name`
- POST /api/v1/admin/network-policy-templates added the new required request property `spec_template`
- PUT /api/v1/admin/network-policy-templates/{id} added the new required request property `name`
- PUT /api/v1/admin/network-policy-templates/{id} added the new required request property `spec_template`
- POST /api/v1/catalog/repositories/{id}/sync/ removed the success response with the status `200`
- POST /api/v1/cluster-groups added the new required request property `name`
- PATCH /api/v1/cluster-groups/{id} added the new required request property `name`
- PUT /api/v1/cluster-groups/{id} added the new required request property `name`
- POST /api/v1/cluster-groups/{id}/move added the new required request property `cluster_ids`
- POST /api/v1/cluster-templates added the new required request property `name`
- PATCH /api/v1/cluster-templates/{id} added the new required request property `name`
- PUT /api/v1/cluster-templates/{id} added the new required request property `name`
- PUT /api/v1/clusters/{cluster_id}/apiserver-allowlist added the new required request property `cidrs`
- PUT /api/v1/clusters/{cluster_id}/apiserver-allowlist added the new required request property `mode`
- POST /api/v1/clusters/{cluster_id}/network-policies/applications added the new required request property `template_id`
- POST /api/v1/clusters/{cluster_id}/template added the new required request property `template_id`
- POST /api/v1/delivery/deployments/{id}/reconcile/ added the new path request parameter `id`
- POST /api/v1/delivery/deployments/{id}/reconcile/ added the new required `header` request parameter `if-match`
- POST /api/v1/delivery/deployments/{id}/resume/ added the new path request parameter `id`
- POST /api/v1/delivery/deployments/{id}/resume/ added the new required `header` request parameter `if-match`
- POST /api/v1/delivery/deployments/{id}/suspend/ added the new path request parameter `id`
- POST /api/v1/delivery/deployments/{id}/suspend/ added the new required `header` request parameter `if-match`
- POST /api/v1/delivery/rollouts/{id}/abort/ added the new path request parameter `id`
- POST /api/v1/delivery/rollouts/{id}/abort/ added the new required `header` request parameter `if-match`
- POST /api/v1/delivery/rollouts/{id}/pause/ added the new path request parameter `id`
- POST /api/v1/delivery/rollouts/{id}/pause/ added the new required `header` request parameter `if-match`
- POST /api/v1/delivery/rollouts/{id}/resume/ added the new path request parameter `id`
- POST /api/v1/delivery/rollouts/{id}/resume/ added the new required `header` request parameter `if-match`
- POST /api/v1/delivery/rollouts/{id}/retry/ added the new path request parameter `id`
- POST /api/v1/delivery/rollouts/{id}/retry/ added the new required `header` request parameter `if-match`
- POST /api/v1/delivery/targets/{id}/orphan/ added the new required `query` request parameter `project_id`

Reviewed 2026-08-24 as specification-only corrections to existing handler
responses; no runtime response field was removed:

- GET /api/v1/admin/scim-tokens/ removed the optional property `tokens` from the response with the `200` status
- POST /api/v1/admin/scim-tokens/ removed the optional property `created_at` from the response with the `201` status
- POST /api/v1/admin/scim-tokens/ removed the optional property `id` from the response with the `201` status
- POST /api/v1/admin/scim-tokens/ removed the optional property `last_used_at` from the response with the `201` status
- POST /api/v1/admin/scim-tokens/ removed the optional property `name` from the response with the `201` status
- POST /api/v1/admin/scim-tokens/ removed the optional property `prefix` from the response with the `201` status
- POST /api/v1/admin/scim-tokens/ removed the optional property `token` from the response with the `201` status
- GET /api/v1/auth/dex/connector-types/ removed the optional property `allOf[subschema #2]/data/items/display_name` from the response with the `200` status
- GET /api/v1/auth/dex/connector-types/ removed the optional property `allOf[subschema #2]/data/items/optional_fields` from the response with the `200` status
- GET /api/v1/auth/dex/connector-types/ removed the optional property `allOf[subschema #2]/data/items/required_fields` from the response with the `200` status
- GET /api/v1/auth/dex/connector-types/ removed the optional property `allOf[subschema #2]/data/items/secret_fields` from the response with the `200` status

Reviewed 2026-08-24 as intentional asynchronous-contract corrections. These
Gatekeeper mutations now commit desired state, a durable tunnel task, and a
mandatory audit intent before returning; `202` replaces the prior response
that incorrectly claimed the member-cluster effect had completed:

- POST /api/v1/clusters/{id}/gatekeeper/constraints/ removed the success response with the status `201`
- DELETE /api/v1/clusters/{id}/gatekeeper/constraints/{name}/ removed the success response with the status `204`

Reviewed 2026-08-24 as intentional runtime asynchronous-contract changes for
pre-GA/v1 hardening. The prior synchronous success responses falsely implied
that remote Kubernetes or backup effects had completed. Mutations now require
a stable `Idempotency-Key`, commit durable intent, and return a pollable `202`
operation receipt/status where applicable. Management backup operations remain
nonterminal until their Kubernetes Job outcome is observed. Drain's non-atomic
pod evidence moved from the old immediate response fields into the durable
`NodeOperation.progress` document so partial cordon, eviction, retry, and block
states remain truthful across worker retries and process restarts. These are
runtime behavior changes, not specification-only corrections:

- DELETE /api/v1/admin/management-backup/destinations/{id}/ removed the success response with the status `204`
- POST /api/v1/clusters/{cluster_id}/snapshots added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/clusters/{id}/gatekeeper/constraints/{name}/ removed the success response with the status `204`
- POST /api/v1/admin/management-backup/destinations/{id}/run/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/admin/management-backup/destinations/{id}/test/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/admin/management-backup/destinations/{id}/test/ removed the success response with the status `200`
- DELETE /api/v1/clusters/{cluster_id}/resources/persistentvolumes/{name} added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/clusters/{cluster_id}/resources/persistentvolumes/{name} removed the success response with the status `200`
- POST /api/v1/clusters/{cluster_id}/resources/{resource_type} added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{cluster_id}/resources/{resource_type} removed the success response with the status `200`
- DELETE /api/v1/clusters/{cluster_id}/resources/{resource_type}/{namespace}/{name} added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/clusters/{cluster_id}/resources/{resource_type}/{namespace}/{name} removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/annotations/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/annotations/ removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/annotations/remove/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/annotations/remove/ removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/cordon/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/cordon/ removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ removed the optional property `data/blockers` from the response with the `202` status
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ removed the optional property `data/evicted` from the response with the `202` status
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ removed the optional property `data/failed` from the response with the `202` status
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ removed the optional property `data/message` from the response with the `202` status
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ removed the optional property `data/node` from the response with the `202` status
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ removed the optional property `data/skipped` from the response with the `202` status
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ added the new `failed` enum value to the `data/status` response property for the response status `202`
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ added the new `pending` enum value to the `data/status` response property for the response status `202`
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ added the new `retrying` enum value to the `data/status` response property for the response status `202`
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ added the new `running` enum value to the `data/status` response property for the response status `202`
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ added the new `succeeded` enum value to the `data/status` response property for the response status `202`
- POST /api/v1/nodes/{cluster_id}/{node_name}/labels/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/labels/ removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/labels/remove/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/labels/remove/ removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/taints/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/taints/ removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/taints/remove/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/taints/remove/ removed the success response with the status `200`
- POST /api/v1/nodes/{cluster_id}/{node_name}/uncordon/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/nodes/{cluster_id}/{node_name}/uncordon/ removed the success response with the status `200`
- DELETE /api/v1/resources/{cluster_id}/{type}/{namespace}/{name} added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/resources/{cluster_id}/{type}/{namespace}/{name} removed the success response with the status `200`
- PUT /api/v1/resources/{cluster_id}/{type}/{namespace}/{name} added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/workloads/pods/{cluster_id}/{namespace}/{pod}/ removed the success response with the status `204`

Reviewed 2026-08-24 as intentional durable-operation contract hardening for
catalog and workload mutations. Callers must generate one stable UUID
`Idempotency-Key` per logical mutation and reuse that UUID for every transport
retry of the same mutation. Catalog-operation list consumers must read the
actual paginated response envelope (`data` plus `pagination`) instead of
deserializing the response body as a bare array:

- POST /api/v1/catalog/installed/ added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/catalog/installed/{id}/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/catalog/installed/{id}/rollback/ added the new required `header` request parameter `idempotency-key`
- PUT /api/v1/catalog/installed/{id}/upgrade/ added the new required `header` request parameter `idempotency-key`
- GET /api/v1/catalog/operations/ the response's body `type` changed from `array<object>` to `any` for status `200`
- POST /api/v1/catalog/operations/{id}/retry/ added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/restart/ added the new required `header` request parameter `idempotency-key`
- PATCH /api/v1/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/scale/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/workloads/operations/{id}/retry/ added the new required `header` request parameter `idempotency-key`

Reviewed 2026-08-24 as specification-only corrections made while replacing
the remaining management-backup and project UI compatibility transports with
generated operations. The destination handlers already require a JSON body;
the response envelopes and project fields below now describe the wire payloads
the existing handlers actually emit. No runtime response field was removed.
The `object` to `any` wording is oasdiff's representation of replacing an
unconstrained object with the repository's typed `allOf` envelope.

- GET /api/v1/admin/backup-drill/ the response's body `type` changed from `object` to `any` for status `200`
- GET /api/v1/admin/management-backup/ the response's body `type` changed from `object` to `any` for status `200`
- POST /api/v1/admin/management-backup/destinations/ added required request body
- POST /api/v1/admin/management-backup/destinations/ the response's body `type` changed from `object` to `any` for status `201`
- PUT /api/v1/admin/management-backup/destinations/{id}/ added required request body
- PUT /api/v1/admin/management-backup/destinations/{id}/ the response's body `type` changed from `object` to `any` for status `200`
- POST /api/v1/admin/management-backup/destinations/{id}/run/ the response's body `type` changed from `object` to `any` for status `202`
- GET /api/v1/clusters/{cluster_id}/projects/ removed the optional property `allOf[subschema #2]/data/items/resource_quota_cpu` from the response with the `200` status
- GET /api/v1/clusters/{cluster_id}/projects/ removed the optional property `allOf[subschema #2]/data/items/resource_quota_memory` from the response with the `200` status
- GET /api/v1/clusters/{cluster_id}/projects/ removed the optional property `allOf[subschema #2]/data/items/resource_quota_pods` from the response with the `200` status
- GET /api/v1/projects/ removed the optional property `allOf[subschema #2]/data/items/resource_quota_cpu` from the response with the `200` status
- GET /api/v1/projects/ removed the optional property `allOf[subschema #2]/data/items/resource_quota_memory` from the response with the `200` status
- GET /api/v1/projects/ removed the optional property `allOf[subschema #2]/data/items/resource_quota_pods` from the response with the `200` status
- POST /api/v1/projects/ removed the optional property `allOf[subschema #2]/data/resource_quota_cpu` from the response with the `201` status
- POST /api/v1/projects/ removed the optional property `allOf[subschema #2]/data/resource_quota_memory` from the response with the `201` status
- POST /api/v1/projects/ removed the optional property `allOf[subschema #2]/data/resource_quota_pods` from the response with the `201` status
- GET /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_cpu` from the response with the `200` status
- GET /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_memory` from the response with the `200` status
- GET /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_pods` from the response with the `200` status
- PATCH /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_cpu` from the response with the `200` status
- PATCH /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_memory` from the response with the `200` status
- PATCH /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_pods` from the response with the `200` status
- PUT /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_cpu` from the response with the `200` status
- PUT /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_memory` from the response with the `200` status
- PUT /api/v1/projects/{id}/ removed the optional property `allOf[subschema #2]/data/resource_quota_pods` from the response with the `200` status
- POST /api/v1/projects/{id}/add-namespace/ removed the optional property `allOf[subschema #2]/data/resource_quota_cpu` from the response with the `200` status
- POST /api/v1/projects/{id}/add-namespace/ removed the optional property `allOf[subschema #2]/data/resource_quota_memory` from the response with the `200` status
- POST /api/v1/projects/{id}/add-namespace/ removed the optional property `allOf[subschema #2]/data/resource_quota_pods` from the response with the `200` status
- PATCH /api/v1/projects/{id}/policy/ removed the optional property `allOf[subschema #2]/data/resource_quota_cpu` from the response with the `200` status
- PATCH /api/v1/projects/{id}/policy/ removed the optional property `allOf[subschema #2]/data/resource_quota_memory` from the response with the `200` status
- PATCH /api/v1/projects/{id}/policy/ removed the optional property `allOf[subschema #2]/data/resource_quota_pods` from the response with the `200` status
- POST /api/v1/projects/{id}/remove-namespace/ removed the optional property `allOf[subschema #2]/data/resource_quota_cpu` from the response with the `200` status
- POST /api/v1/projects/{id}/remove-namespace/ removed the optional property `allOf[subschema #2]/data/resource_quota_memory` from the response with the `200` status
- POST /api/v1/projects/{id}/remove-namespace/ removed the optional property `allOf[subschema #2]/data/resource_quota_pods` from the response with the `200` status

Reviewed 2026-08-24 as corrections to the kubectl-shell lifecycle contract.
The handler has always returned the created session as `201`, and close has
always returned a synchronous `200` receipt after cluster-side cleanup. The old
documented `200` open and empty `204` close responses were never emitted.

- POST /api/v1/clusters/{id}/shell/sessions/ removed the success response with the status `200`
- POST /api/v1/clusters/{id}/shell/sessions/{session_id}/close/ removed the success response with the status `204`

Reviewed 2026-08-25 as intentional enterprise asynchronous-contract
hardening. These operations now require caller-stable idempotency keys and
return durable, pollable receipts rather than claiming remote or queued effects
have completed. The response-schema entries record the corresponding typed
receipt corrections; no runtime field was silently removed.

- POST /api/v1/admin/charlie/trigger-events/{event_id}/retry/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/admin/charlie/trigger-events/{event_id}/retry/ the response's body `type` changed from `object` to `any` for status `202`
- POST /api/v1/admin/charlie/trigger-events/{event_id}/retry/ removed the required property `event` from the response with the `202` status
- DELETE /api/v1/admin/management-backup/destinations/{id}/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/admin/webhooks/{id}/deliveries/{delivery_id}/retry added the new required `header` request parameter `idempotency-key`
- POST /api/v1/admin/webhooks/{id}/deliveries/{delivery_id}/retry the response's body `type` changed from `object` to `any` for status `202`
- POST /api/v1/admin/webhooks/{id}/deliveries/{delivery_id}/retry removed the optional property `delivery_id` from the response with the `202` status
- POST /api/v1/admin/webhooks/{id}/deliveries/{delivery_id}/retry removed the optional property `message` from the response with the `202` status
- POST /api/v1/admin/webhooks/{id}/test added the new required `header` request parameter `idempotency-key`
- POST /api/v1/admin/webhooks/{id}/test the response's body `type` changed from `object` to `any` for status `202`
- POST /api/v1/admin/webhooks/{id}/test removed the optional property `delivery_id` from the response with the `202` status
- POST /api/v1/admin/webhooks/{id}/test removed the optional property `message` from the response with the `202` status
- POST /api/v1/admin/webhooks/{id}/test removed the optional property `queued_at` from the response with the `202` status
- POST /api/v1/admin/webhooks/{id}/test removed the optional property `subscription_id` from the response with the `202` status
- POST /api/v1/auth/dex/apply/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/auth/dex/apply/ removed the success response with the status `200`
- POST /api/v1/auth/dex/apply/ removed the optional property `allOf[subschema #2]/data/applied` from the response with the `202` status
- POST /api/v1/auth/dex/apply/ removed the optional property `allOf[subschema #2]/data/runtime_state` from the response with the `202` status
- POST /api/v1/auth/dex/apply/ removed the optional property `allOf[subschema #2]/data/staged` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/auth/dex/register-as-sso/ removed the success response with the status `200`
- POST /api/v1/auth/dex/register-as-sso/ removed the success response with the status `201`
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/applied` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/client_id` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/created` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/display_name` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/id` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/is_enabled` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/issuer_url` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/provider` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/runtime_changed` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/runtime_state` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/secret_resource_version` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/staged` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/updated` from the response with the `202` status
- POST /api/v1/auth/dex/register-as-sso/ removed the optional property `allOf[subschema #2]/data/verified` from the response with the `202` status
- POST /api/v1/catalog/repositories/{id}/sync/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/cluster-agents/{cluster_id}/upgrade/ added the new required `header` request parameter `idempotency-key` to all path's operations
- POST /api/v1/clusters/{cluster_id}/apiserver-allowlist/reconcile added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{cluster_id}/network-policies/applications added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{cluster_id}/network-policies/applications/{id}/reapply added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{cluster_id}/snapshots/{id}/restore added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{cluster_id}/template added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{cluster_id}/template/reapply added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{id}/agent-token/rotate/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/clusters/{id}/gatekeeper/constraints/ added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/clusters/{id}/gatekeeper/constraints/{name}/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/deployments/{id}/reconcile/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/deployments/{id}/resume/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/deployments/{id}/suspend/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/rollouts/{id}/abort/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/rollouts/{id}/approve/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/rollouts/{id}/approve/ removed the required property `data/event` from the response with the `202` status
- POST /api/v1/delivery/rollouts/{id}/approve/ removed the required property `data/rollout` from the response with the `202` status
- POST /api/v1/delivery/rollouts/{id}/approve/ removed the optional property `data/approval` from the response with the `202` status
- POST /api/v1/delivery/rollouts/{id}/pause/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/rollouts/{id}/resume/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/rollouts/{id}/retry/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/rollouts/{id}/rollback/ added the new required `header` request parameter `idempotency-key`
- POST /api/v1/delivery/rollouts/{id}/rollback/ removed the required property `data/event` from the response with the `202` status
- POST /api/v1/delivery/rollouts/{id}/rollback/ removed the required property `data/rollout` from the response with the `202` status
- POST /api/v1/delivery/rollouts/{id}/rollback/ removed the optional property `data/approval` from the response with the `202` status
- POST /api/v1/delivery/sources/{id}/verify/ the `header` request parameter `idempotency-key` became required
- POST /api/v1/delivery/sources/{id}/verify/ for the `header` request parameter `idempotency-key`, the minLength was increased from `0` to `1`
- DELETE /api/v1/delivery/targets/{id}/ added the new required `header` request parameter `idempotency-key`
- DELETE /api/v1/delivery/targets/{id}/ removed the required property `data/deletion_state` from the response with the `202` status
- DELETE /api/v1/delivery/targets/{id}/ removed the required property `data/id` from the response with the `202` status
- DELETE /api/v1/delivery/targets/{id}/ removed the required property `data/resource_version` from the response with the `202` status
- DELETE /api/v1/delivery/targets/{id}/ removed the optional property `data/deployment_count` from the response with the `202` status
- POST /api/v1/delivery/targets/{id}/rollouts/ for the `header` request parameter `idempotency-key`, the minLength was increased from `0` to `1`
- POST /api/v1/nodes/{cluster_id}/{node_name}/drain/ added the new required `header` request parameter `idempotency-key` to all path's operations
- DELETE /api/v1/workloads/pods/{cluster_id}/{namespace}/{pod}/ added the new required `header` request parameter `idempotency-key`
