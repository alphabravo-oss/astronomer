-- Charlie integration metadata only. Content, raw evidence, model output, tool
-- arguments/results, and reusable central credentials do not belong here.

-- name: GetCharlieAutomationRole :one
SELECT * FROM global_roles WHERE name = 'Charlie Automation' AND is_builtin = true;

-- name: EnsureCharlieAutomationBinding :one
INSERT INTO global_role_bindings (user_id, "group", role_id, source)
VALUES (sqlc.arg(user_id), '', sqlc.arg(role_id), 'manual')
ON CONFLICT (user_id, role_id) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: UpsertAgentOperationalStatus :one
INSERT INTO agent_operational_statuses (
    cluster_id, agent_id, installed_agent_version, desired_agent_version,
    protocol_version, protocol_compatible, authentication_state,
    registration_state, credential_state, credential_expires_at, upgrade_state,
    audit_ingestion_state, metrics_ingestion_state, state_ingestion_state,
    pending_command_count, failed_command_count, expired_command_count,
    downstream_api_reachable, downstream_api_reported_at, owning_server_replica,
    last_successful_connection_at, last_status_at
) VALUES (
    sqlc.arg(cluster_id), sqlc.arg(agent_id), sqlc.arg(installed_agent_version),
    sqlc.arg(desired_agent_version), sqlc.arg(protocol_version),
    sqlc.narg(protocol_compatible), sqlc.arg(authentication_state),
    sqlc.arg(registration_state), sqlc.arg(credential_state),
    sqlc.narg(credential_expires_at), sqlc.arg(upgrade_state),
    sqlc.arg(audit_ingestion_state), sqlc.arg(metrics_ingestion_state),
    sqlc.arg(state_ingestion_state), sqlc.arg(pending_command_count),
    sqlc.arg(failed_command_count), sqlc.arg(expired_command_count),
    sqlc.narg(downstream_api_reachable), sqlc.narg(downstream_api_reported_at),
    sqlc.arg(owning_server_replica), sqlc.narg(last_successful_connection_at),
    sqlc.arg(last_status_at)
)
ON CONFLICT (cluster_id) DO UPDATE SET
    agent_id = EXCLUDED.agent_id,
    installed_agent_version = EXCLUDED.installed_agent_version,
    desired_agent_version = EXCLUDED.desired_agent_version,
    protocol_version = EXCLUDED.protocol_version,
    protocol_compatible = EXCLUDED.protocol_compatible,
    authentication_state = EXCLUDED.authentication_state,
    registration_state = EXCLUDED.registration_state,
    credential_state = EXCLUDED.credential_state,
    credential_expires_at = EXCLUDED.credential_expires_at,
    upgrade_state = EXCLUDED.upgrade_state,
    audit_ingestion_state = EXCLUDED.audit_ingestion_state,
    metrics_ingestion_state = EXCLUDED.metrics_ingestion_state,
    state_ingestion_state = EXCLUDED.state_ingestion_state,
    pending_command_count = EXCLUDED.pending_command_count,
    failed_command_count = EXCLUDED.failed_command_count,
    expired_command_count = EXCLUDED.expired_command_count,
    downstream_api_reachable = EXCLUDED.downstream_api_reachable,
    downstream_api_reported_at = EXCLUDED.downstream_api_reported_at,
    owning_server_replica = EXCLUDED.owning_server_replica,
    last_successful_connection_at = EXCLUDED.last_successful_connection_at,
    last_status_at = EXCLUDED.last_status_at,
    updated_at = now()
RETURNING *;

-- name: RecordAgentConnectionEvent :one
INSERT INTO agent_connection_events (
    cluster_id, connection_id, event_type, reason_code, agent_id,
    agent_version, protocol_version, server_replica, metadata, occurred_at
) VALUES (
    sqlc.arg(cluster_id), sqlc.narg(connection_id), sqlc.arg(event_type),
    sqlc.arg(reason_code), sqlc.arg(agent_id), sqlc.arg(agent_version),
    sqlc.arg(protocol_version), sqlc.arg(server_replica), sqlc.arg(metadata),
    sqlc.arg(occurred_at)
) RETURNING *;

-- name: RecordTunnelLocatorEvent :one
INSERT INTO tunnel_locator_events (
    connection_id, cluster_id, event_type, reason_code, server_replica, occurred_at
) VALUES (
    sqlc.arg(connection_id), sqlc.narg(cluster_id), sqlc.arg(event_type),
    sqlc.arg(reason_code), sqlc.arg(server_replica), sqlc.arg(occurred_at)
) RETURNING *;

-- name: CharlieAgentFleetSummary :one
WITH latest AS (
    SELECT DISTINCT ON (cluster_id) cluster_id, status, last_ping, connected_at, disconnected_at
    FROM agent_connections
    ORDER BY cluster_id, connected_at DESC
)
SELECT
    count(*)::bigint AS total_clusters,
    count(*) FILTER (WHERE latest.status = 'connected')::bigint AS connected_clusters,
    count(*) FILTER (WHERE latest.status IS NULL OR latest.status <> 'connected')::bigint AS disconnected_clusters,
    count(*) FILTER (WHERE COALESCE(latest.last_ping, latest.connected_at, c.last_heartbeat, c.created_at) < now() - make_interval(secs => sqlc.arg(stale_seconds)::int))::bigint AS stale_heartbeats,
    count(*) FILTER (WHERE ops.authentication_state IN ('failed', 'expired', 'revoked') OR ops.registration_state IN ('failed', 'rejected'))::bigint AS authentication_or_registration_failures,
    count(*) FILTER (WHERE ops.audit_ingestion_state IN ('degraded', 'failed') OR ops.metrics_ingestion_state IN ('degraded', 'failed') OR ops.state_ingestion_state IN ('degraded', 'failed'))::bigint AS ingestion_degraded,
    count(*) FILTER (WHERE ops.downstream_api_reachable = false)::bigint AS reported_api_unreachable
FROM clusters c
LEFT JOIN latest ON latest.cluster_id = c.id
LEFT JOIN agent_operational_statuses ops ON ops.cluster_id = c.id
WHERE c.decommissioned_at IS NULL;

-- name: CharlieAgentFleetList :many
SELECT
    c.id AS cluster_id, c.name AS cluster_name, c.display_name, c.environment,
    c.region, c.labels, c.agent_version AS cluster_agent_version,
    COALESCE(latest.agent_id, ops.agent_id, '') AS agent_id,
    COALESCE(latest.agent_version, ops.installed_agent_version, c.agent_version, '') AS installed_agent_version,
    COALESCE(latest.status, 'never') AS connection_state,
    COALESCE(latest.last_ping, c.last_heartbeat) AS last_heartbeat,
    ops.last_successful_connection_at,
    COALESCE(ops.authentication_state, 'unknown') AS authentication_state,
    COALESCE(ops.registration_state, 'unknown') AS registration_state,
	COALESCE(ops.credential_state, 'unknown') AS credential_state,
	ops.credential_expires_at,
    COALESCE(ops.protocol_version, '') AS protocol_version,
    ops.protocol_compatible,
	COALESCE(ops.desired_agent_version, '') AS desired_agent_version,
	COALESCE(ops.upgrade_state, 'unknown') AS upgrade_state,
    COALESCE(ops.owning_server_replica, '') AS owning_server_replica,
    COALESCE(ops.audit_ingestion_state, 'unknown') AS audit_ingestion_state,
    COALESCE(ops.metrics_ingestion_state, 'unknown') AS metrics_ingestion_state,
    COALESCE(ops.state_ingestion_state, 'unknown') AS state_ingestion_state,
	COALESCE(ops.pending_command_count, 0)::int AS pending_command_count,
	COALESCE(ops.failed_command_count, 0)::int AS failed_command_count,
	COALESCE(ops.expired_command_count, 0)::int AS expired_command_count,
	ops.downstream_api_reachable,
	ops.downstream_api_reported_at,
	ops.last_status_at
FROM clusters c
LEFT JOIN LATERAL (
    SELECT ac.agent_id, ac.agent_version, ac.status, ac.last_ping
    FROM agent_connections ac WHERE ac.cluster_id = c.id
    ORDER BY ac.connected_at DESC LIMIT 1
) latest ON true
LEFT JOIN agent_operational_statuses ops ON ops.cluster_id = c.id
WHERE c.decommissioned_at IS NULL
  AND (sqlc.narg(environment)::text IS NULL OR c.environment = sqlc.narg(environment))
  AND (sqlc.narg(region)::text IS NULL OR c.region = sqlc.narg(region))
  AND (sqlc.narg(connection_state)::text IS NULL OR COALESCE(latest.status, 'never') = sqlc.narg(connection_state))
ORDER BY c.display_name, c.id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CharlieAgentFleetGet :one
SELECT
    c.id AS cluster_id, c.name AS cluster_name, c.display_name, c.environment,
    c.region, c.labels, c.agent_version AS cluster_agent_version,
    COALESCE(latest.agent_id, ops.agent_id, '') AS agent_id,
    COALESCE(latest.agent_version, ops.installed_agent_version, c.agent_version, '') AS installed_agent_version,
    COALESCE(latest.status, 'never') AS connection_state,
    COALESCE(latest.last_ping, c.last_heartbeat) AS last_heartbeat,
    ops.*
FROM clusters c
LEFT JOIN LATERAL (
    SELECT ac.agent_id, ac.agent_version, ac.status, ac.last_ping
    FROM agent_connections ac WHERE ac.cluster_id = c.id
    ORDER BY ac.connected_at DESC LIMIT 1
) latest ON true
LEFT JOIN agent_operational_statuses ops ON ops.cluster_id = c.id
WHERE c.id = sqlc.arg(cluster_id) AND c.decommissioned_at IS NULL;

-- name: CharlieAgentConnectionHistory :many
SELECT * FROM agent_connection_events
WHERE cluster_id = sqlc.arg(cluster_id)
  AND occurred_at >= sqlc.arg(since)
ORDER BY occurred_at DESC, id
LIMIT sqlc.arg(row_limit);

-- name: CharlieAgentReconnectStats :one
SELECT
    count(*) FILTER (WHERE event_type = 'connected')::bigint AS reconnect_count,
    count(*) FILTER (WHERE event_type = 'disconnected')::bigint AS disconnect_count,
    count(*) FILTER (WHERE event_type IN ('connected', 'disconnected', 'reconnecting'))::bigint AS flap_event_count
FROM agent_connection_events
WHERE cluster_id = sqlc.arg(cluster_id) AND occurred_at >= sqlc.arg(since);

-- name: CharlieTunnelReplicaDistribution :many
SELECT owning_server_replica AS server_replica, count(*)::bigint AS connection_count
FROM agent_operational_statuses
WHERE owning_server_replica <> ''
GROUP BY owning_server_replica
ORDER BY connection_count DESC, server_replica;

-- name: CharlieTunnelRecentErrors :many
SELECT * FROM tunnel_locator_events
WHERE event_type <> 'recovered'
  AND occurred_at >= sqlc.arg(since)
  AND (sqlc.narg(connection_id)::text IS NULL OR connection_id = sqlc.narg(connection_id))
ORDER BY occurred_at DESC, id
LIMIT sqlc.arg(row_limit);

-- name: CharlieTunnelHealth :one
SELECT
    count(*) FILTER (WHERE event_type <> 'recovered')::bigint AS recent_errors,
    count(*) FILTER (WHERE event_type = 'lookup_failed')::bigint AS lookup_failures,
    count(*) FILTER (WHERE event_type = 'owner_unreachable')::bigint AS owner_unreachable,
    max(occurred_at) AS last_event_at
FROM tunnel_locator_events
WHERE occurred_at >= sqlc.arg(since);

-- name: GetActiveCharlieConnection :one
SELECT * FROM charlie_connections WHERE active = true LIMIT 1;

-- name: GetLatestCharlieConnection :one
SELECT * FROM charlie_connections ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: GetCharlieConnection :one
SELECT * FROM charlie_connections WHERE id = $1;

-- name: GetCharlieConnectionByDeploymentID :one
SELECT * FROM charlie_connections WHERE deployment_id = $1 AND active = true;

-- name: CreateCharlieConnection :one
INSERT INTO charlie_connections (
    installation_id, product_id, product_slug, deployment_id, route_id, central_url,
    central_ca_fingerprint, signing_key_id, signing_key_fingerprint,
    onboarding_schema_version, central_api_version, agent_protocol_version,
    chart_reference, chart_version, chart_digest, image_reference, image_digest, logical_agent_id, replica_count,
    bridge_service_name, mcp_service_name, local_trust_material_encrypted,
    agent_secret_name, onboarding_package_id, onboarding_package_digest,
    onboarding_package_expires_at, enrollment_credentials_expires_at,
    artifact_credential_expires_at, certificate_expires_at,
    onboarding_state, agent_secret_hmac, requested_mode, verified_mode,
    disclosure_digest, health_state, active, created_by_id, reconciliation_due_at
) VALUES (
    sqlc.arg(installation_id), sqlc.arg(product_id), sqlc.arg(product_slug), sqlc.arg(deployment_id),
    sqlc.arg(route_id), sqlc.arg(central_url), sqlc.arg(central_ca_fingerprint),
    sqlc.arg(signing_key_id), sqlc.arg(signing_key_fingerprint),
    sqlc.arg(onboarding_schema_version), sqlc.arg(central_api_version),
    sqlc.arg(agent_protocol_version), sqlc.arg(chart_reference), sqlc.arg(chart_version),
    sqlc.arg(chart_digest), sqlc.arg(image_reference), sqlc.arg(image_digest), sqlc.arg(logical_agent_id),
    sqlc.arg(replica_count),
    sqlc.arg(bridge_service_name), sqlc.arg(mcp_service_name),
    sqlc.arg(local_trust_material_encrypted), sqlc.arg(agent_secret_name),
    sqlc.arg(onboarding_package_id), sqlc.arg(onboarding_package_digest),
    sqlc.arg(onboarding_package_expires_at), sqlc.arg(enrollment_credentials_expires_at),
    sqlc.arg(artifact_credential_expires_at), sqlc.arg(certificate_expires_at),
    sqlc.arg(onboarding_state), sqlc.arg(agent_secret_hmac),
    'disabled', 'disabled', sqlc.arg(disclosure_digest), sqlc.arg(health_state), false,
    sqlc.narg(created_by_id), sqlc.narg(reconciliation_due_at)
) RETURNING *;

-- name: GetCharlieConnectionByPackageID :one
SELECT * FROM charlie_connections WHERE onboarding_package_id = $1;

-- name: AdvanceCharlieOnboardingState :one
UPDATE charlie_connections
SET onboarding_state = sqlc.arg(next_state),
    agent_secret_hmac = COALESCE(NULLIF(sqlc.arg(agent_secret_hmac)::text, ''), agent_secret_hmac),
    last_error_code = sqlc.arg(last_error_code),
    reconciliation_due_at = sqlc.narg(reconciliation_due_at),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND onboarding_state = sqlc.arg(expected_state)
RETURNING *;

-- name: ActivateCharlieConnection :one
UPDATE charlie_connections
SET active = true,
    onboarding_state = 'active',
    health_state = sqlc.arg(health_state),
    last_verified_at = now(),
    updated_at = now()
WHERE charlie_connections.id = sqlc.arg(id)
  AND charlie_connections.onboarding_state = 'consumed'
  AND charlie_connections.active = false
RETURNING *;

-- name: LockCharlieConnectionActivation :many
SELECT id
FROM charlie_connections
WHERE active = true OR id = sqlc.arg(id)
ORDER BY id
FOR UPDATE;

-- name: DeactivateCharlieConnectionsForReplacement :exec
UPDATE charlie_connections
SET active = false,
    requested_mode = 'disabled',
    verified_mode = 'disabled',
    health_state = 'inactive',
    leader_instance_id = '',
    updated_at = now()
WHERE active = true
  AND id <> sqlc.arg(id);

-- name: CompareAndSetCharlieMode :one
UPDATE charlie_connections
SET requested_mode = sqlc.arg(requested_mode),
    verified_mode = sqlc.arg(verified_mode),
    verified_mode_revision = sqlc.arg(next_revision),
    disclosure_digest = sqlc.arg(disclosure_digest),
    last_verified_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND active = true
  AND verified_mode_revision = sqlc.arg(expected_revision)
  AND emergency_disabled = false
RETURNING *;

-- name: SetCharlieEmergencyDisabled :one
UPDATE charlie_connections
SET emergency_disabled = true,
    emergency_disabled_by_id = sqlc.arg(actor_id),
    emergency_disabled_at = now(),
    requested_mode = 'disabled',
    updated_at = now()
WHERE id = sqlc.arg(id) AND active = true
RETURNING *;

-- name: ClearCharlieEmergencyDisabled :one
UPDATE charlie_connections
SET emergency_disabled = false,
    emergency_disabled_by_id = NULL,
    emergency_disabled_at = NULL,
    requested_mode = 'disabled',
    verified_mode = 'disabled',
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND active = true
  AND emergency_disabled = true
  AND verified_mode = 'disabled'
RETURNING *;

-- name: UpdateCharlieAgentStatus :one
UPDATE charlie_connections
SET leader_instance_id = sqlc.arg(leader_instance_id),
    fencing_epoch = sqlc.arg(fencing_epoch),
    health_state = sqlc.arg(health_state),
    last_connected_at = sqlc.narg(last_connected_at),
    last_error_code = sqlc.arg(last_error_code),
    updated_at = now()
WHERE id = sqlc.arg(id) AND active = true
RETURNING *;

-- name: DisconnectCharlieConnection :one
UPDATE charlie_connections
SET active = false,
    requested_mode = 'disabled',
    health_state = 'disconnected',
    updated_at = now()
WHERE id = $1 AND active = true
RETURNING *;

-- name: CreateCharlieSession :one
INSERT INTO charlie_sessions (
    connection_id, charlie_session_id, client_session_id, owner_user_id,
    source, visibility, intent, resource_scope_summary, state
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(charlie_session_id),
    sqlc.arg(client_session_id), sqlc.narg(owner_user_id), sqlc.arg(source),
    sqlc.arg(visibility), sqlc.arg(intent), sqlc.arg(resource_scope_summary),
    sqlc.arg(state)
) RETURNING *;

-- name: GetCharlieSession :one
SELECT * FROM charlie_sessions WHERE id = $1;

-- name: GetCharlieSessionByClientID :one
SELECT * FROM charlie_sessions WHERE connection_id = $1 AND client_session_id = $2;

-- name: BindCharlieSessionCentralID :one
UPDATE charlie_sessions
SET charlie_session_id = sqlc.arg(charlie_session_id),
    central_revision = sqlc.arg(central_revision),
    state = 'active',
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = 'creating'
  AND charlie_session_id = ''
  AND central_revision = 0
RETURNING *;

-- name: FailCreatingCharlieSession :one
UPDATE charlie_sessions
SET state = 'failed', updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = 'creating'
  AND charlie_session_id = ''
RETURNING *;

-- name: ListCharlieSessionsForOwner :many
SELECT * FROM charlie_sessions
WHERE owner_user_id = sqlc.arg(owner_user_id)
ORDER BY updated_at DESC, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListCharlieAccessibleSessionCandidates :many
SELECT * FROM charlie_sessions
WHERE connection_id = sqlc.arg(connection_id)
  AND (
    (visibility = 'private' AND source = 'user' AND owner_user_id = sqlc.arg(owner_user_id))
    OR visibility = 'incident'
  )
ORDER BY updated_at DESC, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateCharlieSessionCursor :one
UPDATE charlie_sessions
SET state = sqlc.arg(state),
    last_event_id = sqlc.arg(last_event_id),
    central_revision = sqlc.arg(central_revision),
    completed_at = sqlc.narg(completed_at),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND central_revision <= sqlc.arg(central_revision)
RETURNING *;

-- name: AbortCharlieSession :one
UPDATE charlie_sessions
SET state = 'aborted', completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND state IN ('creating', 'active', 'waiting_approval')
RETURNING *;

-- name: GetCharlieSessionByCentralID :one
SELECT * FROM charlie_sessions WHERE charlie_session_id = $1;

-- name: ListCharlieFindingSyncCandidateSessions :many
SELECT * FROM charlie_sessions
WHERE connection_id = sqlc.arg(connection_id)
  AND state IN ('active', 'waiting_approval', 'completed')
  AND charlie_session_id <> ''
ORDER BY updated_at DESC, id
LIMIT 100;

-- name: ListCharlieApprovalCandidateSessions :many
SELECT * FROM charlie_sessions
WHERE state IN ('active', 'waiting_approval')
  AND charlie_session_id <> ''
ORDER BY updated_at DESC, id
LIMIT 200;

-- name: AddCharlieSessionResource :exec
INSERT INTO charlie_session_resources (session_id, resource_type, resource_id, required_verb)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: ListCharlieSessionResources :many
SELECT * FROM charlie_session_resources WHERE session_id = $1 ORDER BY resource_type, resource_id, required_verb;

-- name: ListCharlieSessionResourcesBatch :many
SELECT * FROM charlie_session_resources
WHERE session_id = ANY(sqlc.arg(session_ids)::uuid[])
ORDER BY session_id, resource_type, resource_id, required_verb;

-- name: CreateCharlieDelegation :one
INSERT INTO charlie_delegations (
    session_id, authorization_hash, authorization_prefix, principal_type,
    principal_id, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetActiveCharlieDelegationByHash :one
SELECT * FROM charlie_delegations
WHERE authorization_hash = $1
  AND revoked_at IS NULL
  AND expires_at > now();

-- name: RevokeCharlieDelegation :execrows
UPDATE charlie_delegations SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeCharlieDelegationsForSession :execrows
UPDATE charlie_delegations SET revoked_at = now()
WHERE session_id = $1 AND revoked_at IS NULL;

-- name: RevokeCharlieDelegationsForPrincipal :execrows
UPDATE charlie_delegations SET revoked_at = now()
WHERE principal_id = $1 AND revoked_at IS NULL;

-- name: RevokeExpiredCharlieDelegations :execrows
UPDATE charlie_delegations SET revoked_at = now()
WHERE expires_at <= now() AND revoked_at IS NULL;

-- name: CreateCharlieActionApproval :one
INSERT INTO charlie_action_approvals (
    connection_id, session_id, approval_id, charlie_action_id, turn_id,
    capability, argument_digest, disclosure_digest, mode_revision,
    policy_revision, fencing_epoch, manifest_digest, resource_type, resource_id,
    approver_id, rationale_digest, expires_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(session_id), sqlc.arg(approval_id),
    sqlc.arg(charlie_action_id), sqlc.arg(turn_id), sqlc.arg(capability),
    sqlc.arg(argument_digest), sqlc.arg(disclosure_digest),
    sqlc.arg(mode_revision), sqlc.arg(policy_revision), sqlc.arg(fencing_epoch),
    sqlc.arg(manifest_digest), sqlc.arg(resource_type), sqlc.arg(resource_id), sqlc.arg(approver_id),
    sqlc.arg(rationale_digest), sqlc.arg(expires_at)
) RETURNING *;

-- name: ApproveCharlieActionApproval :one
UPDATE charlie_action_approvals
SET state = 'approved', updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: GetActiveCharlieActionApproval :one
SELECT * FROM charlie_action_approvals
WHERE charlie_action_id = sqlc.arg(charlie_action_id)
  AND approval_id = sqlc.arg(approval_id)
  AND state = 'approved'
  AND expires_at > now()
	AND EXISTS (
		SELECT 1 FROM charlie_session_resources resource
		WHERE resource.session_id = charlie_action_approvals.session_id
		  AND resource.resource_type = charlie_action_approvals.resource_type
		  AND resource.resource_id = charlie_action_approvals.resource_id
		  AND resource.required_verb = 'read'
	);

-- name: GetCharlieActionApprovalByApprovalID :one
SELECT * FROM charlie_action_approvals
WHERE approval_id = sqlc.arg(approval_id);

-- name: ConsumeCharlieActionApproval :one
UPDATE charlie_action_approvals
SET state = 'dispatched', dispatched_at = now(), updated_at = now()
WHERE charlie_action_id = sqlc.arg(charlie_action_id)
  AND approval_id = sqlc.arg(approval_id)
  AND argument_digest = sqlc.arg(argument_digest)
  AND disclosure_digest = sqlc.arg(disclosure_digest)
  AND mode_revision = sqlc.arg(mode_revision)
  AND policy_revision = sqlc.arg(policy_revision)
  AND fencing_epoch = sqlc.arg(fencing_epoch)
	AND charlie_action_approvals.resource_id = sqlc.arg(resource_id)
  AND state = 'approved'
  AND expires_at > now()
	AND EXISTS (
		SELECT 1 FROM charlie_session_resources resource
		WHERE resource.session_id = charlie_action_approvals.session_id
		  AND resource.resource_type = charlie_action_approvals.resource_type
		  AND resource.resource_id = charlie_action_approvals.resource_id
		  AND resource.required_verb = 'read'
	)
RETURNING *;

-- name: TransitionCharlieActionApproval :one
UPDATE charlie_action_approvals
SET state = sqlc.arg(next_state), updated_at = now()
WHERE id = sqlc.arg(id)
  AND state IN ('pending', 'approved')
  AND sqlc.arg(next_state)::text IN ('rejected', 'expired')
RETURNING *;

-- name: ClaimCharlieActionReceipt :one
INSERT INTO charlie_action_receipts (
    connection_id, session_id, charlie_action_id, turn_id, capability, effect,
    argument_digest, arguments_encrypted, authorization_hash, fencing_epoch, product_idempotency_key,
    state, lease_owner, lease_expires_at, audit_correlation_id, resource_digest
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(session_id), sqlc.arg(charlie_action_id),
    sqlc.arg(turn_id), sqlc.arg(capability), sqlc.arg(effect),
    sqlc.arg(argument_digest), sqlc.arg(arguments_encrypted), sqlc.arg(authorization_hash),
    sqlc.arg(fencing_epoch), sqlc.arg(product_idempotency_key), 'claimed',
    sqlc.arg(lease_owner), sqlc.arg(lease_expires_at), sqlc.arg(audit_correlation_id),
    sqlc.arg(resource_digest)
)
ON CONFLICT (charlie_action_id) DO UPDATE
SET state = 'claimed',
    lease_owner = EXCLUDED.lease_owner,
    lease_expires_at = EXCLUDED.lease_expires_at,
    attempt = charlie_action_receipts.attempt + 1,
    updated_at = now()
WHERE ((charlie_action_receipts.state = 'claimed' AND charlie_action_receipts.lease_expires_at < now())
    OR (charlie_action_receipts.state = 'deferred' AND EXISTS (
        SELECT 1 FROM charlie_action_deferrals d
        WHERE d.charlie_action_id = charlie_action_receipts.charlie_action_id
          AND d.deferred_until <= now() AND d.expires_at > now()
    )))
  AND charlie_action_receipts.argument_digest = EXCLUDED.argument_digest
  AND charlie_action_receipts.authorization_hash = EXCLUDED.authorization_hash
  AND charlie_action_receipts.fencing_epoch = EXCLUDED.fencing_epoch
  AND charlie_action_receipts.resource_digest = EXCLUDED.resource_digest
RETURNING charlie_action_receipts.*;

-- name: GetCharlieActionReceipt :one
SELECT * FROM charlie_action_receipts WHERE charlie_action_id = $1;

-- name: TransitionCharlieActionReceipt :one
UPDATE charlie_action_receipts
SET state = sqlc.arg(next_state),
    result_digest = sqlc.arg(result_digest),
    result_status = sqlc.arg(result_status),
    result_encrypted = sqlc.arg(result_encrypted),
    dispatched_at = CASE WHEN sqlc.arg(next_state)::text = 'dispatched' THEN now() ELSE dispatched_at END,
    verified_at = CASE WHEN sqlc.arg(next_state)::text IN ('succeeded', 'failed') THEN now() ELSE verified_at END,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = sqlc.arg(expected_state)
  AND lease_owner = sqlc.arg(lease_owner)
  AND fencing_epoch = sqlc.arg(fencing_epoch)
RETURNING *;

-- name: ListCharlieAmbiguousReceipts :many
SELECT * FROM charlie_action_receipts
WHERE state IN ('dispatched', 'ambiguous', 'verifying')
ORDER BY updated_at, id
LIMIT $1;

-- name: ClaimCharlieAmbiguousReceipt :one
WITH candidate AS (
    SELECT id FROM charlie_action_receipts
    WHERE state IN ('dispatched', 'ambiguous', 'verifying')
      AND lease_expires_at < now()
    ORDER BY updated_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE charlie_action_receipts receipt
SET state = 'verifying',
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = sqlc.arg(lease_expires_at),
    attempt = attempt + 1,
    updated_at = now()
FROM candidate
WHERE receipt.id = candidate.id
RETURNING receipt.*;

-- name: GetCharlieAutomationPolicy :one
SELECT * FROM charlie_automation_policies
WHERE connection_id = sqlc.arg(connection_id)
  AND capability = sqlc.arg(capability);

-- name: ListCharlieAutomationPolicies :many
SELECT * FROM charlie_automation_policies
WHERE connection_id = sqlc.arg(connection_id)
ORDER BY capability;

-- name: UpsertCharlieAutomationPolicy :one
INSERT INTO charlie_automation_policies (
    connection_id, capability, enabled, max_actions_per_incident,
    max_actions_per_window, budget_window_seconds, cooldown_seconds
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(capability), sqlc.arg(enabled),
    sqlc.arg(max_actions_per_incident), sqlc.arg(max_actions_per_window),
    sqlc.arg(budget_window_seconds), sqlc.arg(cooldown_seconds)
)
ON CONFLICT (connection_id, capability) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    max_actions_per_incident = EXCLUDED.max_actions_per_incident,
    max_actions_per_window = EXCLUDED.max_actions_per_window,
    budget_window_seconds = EXCLUDED.budget_window_seconds,
    cooldown_seconds = EXCLUDED.cooldown_seconds,
    revision = charlie_automation_policies.revision + 1,
    updated_at = now()
RETURNING *;

-- name: GetCharlieActionSafetySnapshot :one
WITH current_receipt AS (
    SELECT current.id, current.created_at FROM charlie_action_receipts current
    WHERE current.charlie_action_id = sqlc.arg(action_id_arg)
), prior_receipts AS (
    SELECT r.* FROM charlie_action_receipts r
    WHERE r.session_id = sqlc.arg(session_id_arg)
      AND r.charlie_action_id <> sqlc.arg(action_id_arg)
)
SELECT
    NOT EXISTS (
        SELECT 1 FROM prior_receipts r
        LEFT JOIN current_receipt c ON true
        WHERE r.state IN ('claimed', 'dispatched', 'verifying', 'ambiguous')
          AND (c.id IS NULL OR r.created_at < c.created_at OR (r.created_at = c.created_at AND r.id::text < c.id::text))
    ) AS incident_clear,
    NOT EXISTS (
        SELECT 1 FROM prior_receipts r
        WHERE r.capability = sqlc.arg(capability_arg)
          AND r.resource_digest = sqlc.arg(resource_digest_arg)
          AND r.state IN ('dispatched', 'verifying', 'ambiguous', 'succeeded', 'failed')
          AND r.updated_at > now() - make_interval(secs => sqlc.arg(cooldown_seconds)::integer)
    ) AS cooldown_clear,
    NOT EXISTS (
        SELECT 1 FROM prior_receipts r WHERE r.state IN ('failed', 'ambiguous')
    ) AS circuit_closed,
    (SELECT count(*) FROM prior_receipts r WHERE r.auto_budget_reserved) < sqlc.arg(max_actions_per_incident)::integer AS incident_budget_available,
    (SELECT count(*) FROM prior_receipts r
       WHERE r.auto_budget_reserved
         AND r.updated_at > now() - make_interval(secs => sqlc.arg(budget_window_seconds)::integer)
    ) < sqlc.arg(max_actions_per_window)::integer AS window_budget_available;

-- name: ReserveCharlieAutoBudget :one
WITH locked_session AS MATERIALIZED (
    SELECT s.id FROM charlie_sessions s WHERE s.id = sqlc.arg(session_id_arg) FOR UPDATE
), policy AS (
    SELECT p.* FROM charlie_automation_policies p, locked_session
    WHERE p.connection_id = sqlc.arg(connection_id_arg)
      AND p.capability = sqlc.arg(capability_arg)
      AND p.enabled = true
), current_receipt AS (
    SELECT r.* FROM charlie_action_receipts r, locked_session
    WHERE r.charlie_action_id = sqlc.arg(action_id_arg)
      AND r.session_id = locked_session.id
      AND r.connection_id = sqlc.arg(connection_id_arg)
      AND r.capability = sqlc.arg(capability_arg)
      AND r.resource_digest = sqlc.arg(resource_digest_arg)
      AND r.state = 'claimed'
), eligible AS (
    SELECT r.id, p.revision
    FROM current_receipt r, policy p
    WHERE NOT EXISTS (
        SELECT 1 FROM charlie_action_receipts other
        WHERE other.session_id = r.session_id AND other.id <> r.id
          AND other.state IN ('claimed', 'dispatched', 'verifying', 'ambiguous')
          AND (other.created_at < r.created_at OR (other.created_at = r.created_at AND other.id::text < r.id::text))
    )
      AND NOT EXISTS (
        SELECT 1 FROM charlie_action_receipts other
        WHERE other.session_id = r.session_id AND other.id <> r.id
          AND other.capability = r.capability AND other.resource_digest = r.resource_digest
          AND other.state IN ('dispatched', 'verifying', 'ambiguous', 'succeeded', 'failed')
          AND other.updated_at > now() - make_interval(secs => p.cooldown_seconds)
    )
      AND NOT EXISTS (
        SELECT 1 FROM charlie_action_receipts other
        WHERE other.session_id = r.session_id AND other.id <> r.id
          AND other.state IN ('failed', 'ambiguous')
    )
      AND (SELECT count(*) FROM charlie_action_receipts other
           WHERE other.session_id = r.session_id AND other.auto_budget_reserved) < p.max_actions_per_incident
      AND (SELECT count(*) FROM charlie_action_receipts other
           WHERE other.session_id = r.session_id AND other.auto_budget_reserved
             AND other.updated_at > now() - make_interval(secs => p.budget_window_seconds)) < p.max_actions_per_window
)
UPDATE charlie_action_receipts r
SET auto_budget_reserved = true,
    safety_policy_revision = eligible.revision,
    updated_at = now()
FROM eligible
WHERE r.id = eligible.id AND r.auto_budget_reserved = false
RETURNING r.*;

-- name: CreateCharlieActionDeferral :one
INSERT INTO charlie_action_deferrals (
    charlie_action_id, window_id, deferred_until, expires_at
) VALUES (
    sqlc.arg(charlie_action_id), sqlc.arg(window_id),
    sqlc.arg(deferred_until), sqlc.arg(expires_at)
)
ON CONFLICT (charlie_action_id) DO UPDATE
SET updated_at = now()
WHERE charlie_action_deferrals.window_id = EXCLUDED.window_id
  AND charlie_action_deferrals.deferred_until = EXCLUDED.deferred_until
  AND charlie_action_deferrals.expires_at = EXCLUDED.expires_at
RETURNING charlie_action_deferrals.*;

-- name: CreateCharlieTriggerRule :one
INSERT INTO charlie_trigger_rules (
    connection_id, name, rule_type, category, enabled, minimum_severity,
    selectors, thresholds, window_seconds, cooldown_seconds,
    service_identity_id, mode_ceiling, created_by_id
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(name), sqlc.arg(rule_type),
    sqlc.arg(category), sqlc.arg(enabled), sqlc.arg(minimum_severity),
    sqlc.arg(selectors), sqlc.arg(thresholds), sqlc.arg(window_seconds),
    sqlc.arg(cooldown_seconds), sqlc.arg(service_identity_id),
    sqlc.arg(mode_ceiling), sqlc.narg(created_by_id)
) RETURNING *;

-- name: ListEnabledCharlieTriggerRules :many
SELECT * FROM charlie_trigger_rules WHERE connection_id = $1 AND enabled = true ORDER BY name;

-- name: GetCharlieTriggerRule :one
SELECT * FROM charlie_trigger_rules WHERE id = $1;

-- name: SetCharlieTriggerRuleEnabled :one
UPDATE charlie_trigger_rules SET enabled = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListCharlieTriggerRules :many
SELECT * FROM charlie_trigger_rules WHERE connection_id = $1 ORDER BY name, id;

-- name: UpdateCharlieTriggerRule :one
UPDATE charlie_trigger_rules
SET name = sqlc.arg(name),
    rule_type = sqlc.arg(rule_type),
    category = sqlc.arg(category),
    enabled = sqlc.arg(enabled),
    minimum_severity = sqlc.arg(minimum_severity),
    selectors = sqlc.arg(selectors),
    thresholds = sqlc.arg(thresholds),
    window_seconds = sqlc.arg(window_seconds),
    cooldown_seconds = sqlc.arg(cooldown_seconds),
    service_identity_id = sqlc.arg(service_identity_id),
    mode_ceiling = sqlc.arg(mode_ceiling),
    updated_at = now()
WHERE id = sqlc.arg(id) AND connection_id = sqlc.arg(connection_id)
RETURNING *;

-- name: CreateCharlieTriggerEvent :one
INSERT INTO charlie_trigger_events (
    rule_id, source, event_type, resource_type, resource_id, fingerprint,
    summary_metadata, state, next_attempt_at, first_occurred_at,
    last_occurred_at, origin_resource_ref, origin_event_ref
) VALUES (
    sqlc.arg(rule_id), sqlc.arg(source), sqlc.arg(event_type),
    sqlc.arg(resource_type), sqlc.arg(resource_id), sqlc.arg(fingerprint),
    sqlc.arg(summary_metadata), 'pending', sqlc.arg(next_attempt_at),
    sqlc.arg(occurred_at), sqlc.arg(occurred_at), sqlc.arg(origin_resource_ref),
    sqlc.arg(origin_event_ref)
)
ON CONFLICT (rule_id, fingerprint) WHERE state IN ('pending', 'dispatching', 'dispatched', 'retry')
DO UPDATE SET
    repeat_count = charlie_trigger_events.repeat_count + 1,
    last_occurred_at = GREATEST(charlie_trigger_events.last_occurred_at, EXCLUDED.last_occurred_at),
    summary_metadata = EXCLUDED.summary_metadata,
    origin_resource_ref = EXCLUDED.origin_resource_ref,
    origin_event_ref = EXCLUDED.origin_event_ref,
    updated_at = now()
RETURNING *;

-- name: CreateCharlieTriggerEventWithOutbox :one
WITH event AS (
    INSERT INTO charlie_trigger_events (
        rule_id, source, event_type, resource_type, resource_id, fingerprint,
        summary_metadata, state, next_attempt_at, first_occurred_at,
        last_occurred_at, origin_resource_ref, origin_event_ref
    ) VALUES (
        sqlc.arg(rule_id), sqlc.arg(source), sqlc.arg(event_type),
        sqlc.arg(resource_type), sqlc.arg(resource_id), sqlc.arg(fingerprint),
        sqlc.arg(summary_metadata), 'pending', sqlc.arg(next_attempt_at),
        sqlc.arg(occurred_at), sqlc.arg(occurred_at), sqlc.arg(origin_resource_ref),
        sqlc.arg(origin_event_ref)
    )
    ON CONFLICT (rule_id, fingerprint) WHERE state IN ('pending', 'dispatching', 'dispatched', 'retry')
    DO UPDATE SET
        repeat_count = charlie_trigger_events.repeat_count + 1,
        last_occurred_at = GREATEST(charlie_trigger_events.last_occurred_at, EXCLUDED.last_occurred_at),
        summary_metadata = EXCLUDED.summary_metadata,
        origin_resource_ref = EXCLUDED.origin_resource_ref,
        origin_event_ref = EXCLUDED.origin_event_ref,
        updated_at = now()
    RETURNING *
), outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    )
    SELECT
        'charlie-trigger:' || id::text,
        'charlie:trigger_dispatch',
        convert_to(jsonb_build_object('event_id', id::text)::text, 'UTF8'),
        'tunnel', 8, 60, 1800, 20, sqlc.arg(next_attempt_at)
    FROM event
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO UPDATE SET
        payload = EXCLUDED.payload,
        status = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.status ELSE 'pending' END,
        attempt_count = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.attempt_count ELSE 0 END,
        next_attempt_at = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.next_attempt_at ELSE EXCLUDED.next_attempt_at END,
        locked_until = NULL,
        last_error = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.last_error ELSE '' END,
        updated_at = now()
    RETURNING id
)
SELECT event.* FROM event CROSS JOIN outbox;

-- name: ClaimDueCharlieTriggerEvents :many
WITH due AS (
    SELECT id FROM charlie_trigger_events
    WHERE state IN ('pending', 'retry') AND next_attempt_at <= now()
    ORDER BY next_attempt_at, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)
)
UPDATE charlie_trigger_events e
SET state = 'dispatching', attempt_count = attempt_count + 1, updated_at = now()
FROM due WHERE e.id = due.id
RETURNING e.*;

-- name: GetCharlieTriggerEvent :one
SELECT * FROM charlie_trigger_events WHERE id = $1;

-- name: ListCharlieTriggerEventsForAdmin :many
SELECT e.*
FROM charlie_trigger_events e
JOIN charlie_trigger_rules r ON r.id = e.rule_id
WHERE r.connection_id = sqlc.arg(connection_id)
  AND (sqlc.narg(event_state)::text IS NULL OR e.state = sqlc.narg(event_state))
ORDER BY e.updated_at DESC, e.id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: RetryDeadCharlieTriggerEventWithOutbox :one
WITH source AS (
    SELECT e.*
    FROM charlie_trigger_events e
    JOIN charlie_trigger_rules r ON r.id = e.rule_id
    WHERE e.id = sqlc.arg(retry_of_event_id)
      AND e.state = 'dead'
      AND r.connection_id = sqlc.arg(connection_id)
), event AS (
    INSERT INTO charlie_trigger_events (
        id, rule_id, retry_of_event_id, source, event_type, resource_type,
        resource_id, fingerprint, summary_metadata, state, session_id,
        repeat_count, first_occurred_at, last_occurred_at,
        origin_resource_ref, origin_event_ref, attempt_count, next_attempt_at,
        last_error_code, dead_lettered_at
    )
    SELECT
        sqlc.arg(request_id), rule_id, id, source, event_type, resource_type,
        resource_id, fingerprint, summary_metadata, 'pending', NULL,
        repeat_count, first_occurred_at, last_occurred_at,
        origin_resource_ref, origin_event_ref, 0, now(), '', NULL
    FROM source
    ON CONFLICT (id) DO UPDATE
    SET updated_at = charlie_trigger_events.updated_at
    WHERE charlie_trigger_events.retry_of_event_id = EXCLUDED.retry_of_event_id
      AND charlie_trigger_events.rule_id = EXCLUDED.rule_id
      AND charlie_trigger_events.fingerprint = EXCLUDED.fingerprint
    RETURNING *
), outbox AS (
    INSERT INTO task_outbox (
        dedupe_key, task_type, payload, queue_name, max_retry, timeout_seconds,
        unique_seconds, max_delivery_attempts, next_attempt_at
    )
    SELECT
        'charlie-trigger:' || id::text,
        'charlie:trigger_dispatch',
        convert_to(jsonb_build_object('event_id', id::text)::text, 'UTF8'),
        'tunnel', 8, 60, 1800, 20, next_attempt_at
    FROM event
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO UPDATE SET
        payload = EXCLUDED.payload,
        status = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.status ELSE 'pending' END,
        attempt_count = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.attempt_count ELSE 0 END,
        next_attempt_at = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.next_attempt_at ELSE EXCLUDED.next_attempt_at END,
        locked_until = NULL,
        last_error = CASE WHEN task_outbox.status = 'delivered' THEN task_outbox.last_error ELSE '' END,
        updated_at = now()
    RETURNING id
)
SELECT event.* FROM event CROSS JOIN outbox;

-- name: ClaimCharlieTriggerEvent :one
UPDATE charlie_trigger_events
SET state = 'dispatching', attempt_count = attempt_count + 1, updated_at = now()
WHERE id = $1
  AND state IN ('pending', 'retry')
RETURNING *;

-- name: TransitionCharlieTriggerEvent :one
UPDATE charlie_trigger_events
SET state = sqlc.arg(next_state),
    session_id = COALESCE(sqlc.narg(session_id), session_id),
    next_attempt_at = sqlc.arg(next_attempt_at),
    last_error_code = sqlc.arg(last_error_code),
    dead_lettered_at = CASE WHEN sqlc.arg(next_state)::text = 'dead' THEN now() ELSE dead_lettered_at END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND state = sqlc.arg(expected_state)
RETURNING *;

-- name: SuppressActiveCharlieTriggerEvent :one
UPDATE charlie_trigger_events
SET state = 'suppressed', last_error_code = sqlc.arg(reason_code), updated_at = now()
WHERE rule_id = sqlc.arg(rule_id)
  AND fingerprint = sqlc.arg(fingerprint)
  AND state IN ('pending', 'retry')
RETURNING *;

-- name: UpsertCharlieFinding :one
INSERT INTO charlie_findings (
    connection_id, charlie_finding_id, session_id, source, severity, status,
    effective_mode, execution_block_code, dedupe_fingerprint, title, summary,
    recommended_action_label, risk_impact, verification_summary, expires_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(charlie_finding_id), sqlc.narg(session_id),
    sqlc.arg(source), sqlc.arg(severity), 'open', sqlc.arg(effective_mode),
    sqlc.arg(execution_block_code), sqlc.arg(dedupe_fingerprint), sqlc.arg(title),
    sqlc.arg(summary), sqlc.arg(recommended_action_label), sqlc.arg(risk_impact),
    sqlc.arg(verification_summary), sqlc.narg(expires_at)
)
ON CONFLICT (connection_id, dedupe_fingerprint) WHERE status IN ('open', 'acknowledged')
DO UPDATE SET
    severity = EXCLUDED.severity,
    execution_block_code = EXCLUDED.execution_block_code,
    title = EXCLUDED.title,
    summary = EXCLUDED.summary,
    recommended_action_label = EXCLUDED.recommended_action_label,
    risk_impact = EXCLUDED.risk_impact,
    verification_summary = EXCLUDED.verification_summary,
    expires_at = EXCLUDED.expires_at,
    repeat_count = charlie_findings.repeat_count + 1,
    updated_at = now()
RETURNING *;

-- name: UpsertCharlieApprovalFinding :one
INSERT INTO charlie_findings (
    connection_id, charlie_finding_id, approval_id, session_id, source,
    severity, status, effective_mode, execution_block_code,
    dedupe_fingerprint, title, summary, recommended_action_label,
    risk_impact, verification_summary, expires_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(charlie_finding_id), sqlc.arg(approval_id),
    sqlc.arg(session_id), 'user', 'warning', 'open', 'approval',
    'approval_required', sqlc.arg(dedupe_fingerprint),
    sqlc.arg(title), sqlc.arg(summary), sqlc.arg(recommended_action_label),
    sqlc.arg(risk_impact), sqlc.arg(verification_summary), sqlc.arg(expires_at)
)
ON CONFLICT (approval_id) DO UPDATE
SET updated_at = charlie_findings.updated_at
WHERE charlie_findings.connection_id = EXCLUDED.connection_id
  AND charlie_findings.session_id = EXCLUDED.session_id
  AND charlie_findings.dedupe_fingerprint = EXCLUDED.dedupe_fingerprint
  AND charlie_findings.expires_at = EXCLUDED.expires_at
RETURNING *;

-- name: TransitionCharlieFindingForApproval :one
UPDATE charlie_findings
SET status = CASE
        WHEN sqlc.arg(approval_state)::text = 'rejected' THEN 'resolved'
        WHEN sqlc.arg(approval_state)::text = 'expired' THEN 'expired'
        ELSE status
    END,
    execution_block_code = CASE
        WHEN sqlc.arg(approval_state)::text = 'rejected' THEN 'approval_rejected'
        WHEN sqlc.arg(approval_state)::text = 'expired' THEN 'approval_expired'
        ELSE execution_block_code
    END,
    summary = CASE
        WHEN sqlc.arg(approval_state)::text = 'rejected' THEN 'The exact approval was rejected. No action was authorized.'
        WHEN sqlc.arg(approval_state)::text = 'expired' THEN 'The exact approval expired. No action was authorized.'
        ELSE summary
    END,
    updated_at = now()
WHERE approval_id = sqlc.arg(approval_id)
  AND status IN ('open', 'acknowledged')
  AND sqlc.arg(approval_state)::text IN ('rejected', 'expired')
RETURNING *;

-- name: GetCharlieFindingByApprovalID :one
SELECT * FROM charlie_findings WHERE approval_id = $1;

-- name: GetCharlieFinding :one
SELECT * FROM charlie_findings WHERE id = $1;

-- name: GetCharlieFindingByCentralID :one
SELECT * FROM charlie_findings
WHERE connection_id = sqlc.arg(connection_id)
  AND charlie_finding_id = sqlc.arg(charlie_finding_id);

-- name: UpsertSyncedCharlieFinding :one
INSERT INTO charlie_findings (
    connection_id, charlie_finding_id, session_id, source, severity, status,
    effective_mode, execution_block_code, dedupe_fingerprint, title, summary,
    recommended_action_label, risk_impact, verification_summary, repeat_count, updated_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(charlie_finding_id), sqlc.arg(session_id),
    sqlc.arg(source), sqlc.arg(severity), sqlc.arg(status), sqlc.arg(effective_mode),
    sqlc.arg(execution_block_code), sqlc.arg(dedupe_fingerprint), sqlc.arg(title),
    sqlc.arg(summary), sqlc.arg(recommended_action_label), '',
    sqlc.arg(verification_summary), sqlc.arg(central_repeat_count), sqlc.arg(central_updated_at)
)
ON CONFLICT (connection_id, charlie_finding_id)
DO UPDATE SET
    severity = EXCLUDED.severity,
    status = EXCLUDED.status,
    effective_mode = EXCLUDED.effective_mode,
    execution_block_code = EXCLUDED.execution_block_code,
    title = EXCLUDED.title,
    summary = EXCLUDED.summary,
    recommended_action_label = EXCLUDED.recommended_action_label,
    verification_summary = EXCLUDED.verification_summary,
    repeat_count = GREATEST(charlie_findings.repeat_count, EXCLUDED.repeat_count),
    updated_at = EXCLUDED.updated_at
WHERE charlie_findings.session_id = EXCLUDED.session_id
  AND charlie_findings.updated_at < EXCLUDED.updated_at
RETURNING *;

-- name: GetActiveCharlieFindingByFingerprint :one
SELECT * FROM charlie_findings
WHERE connection_id = sqlc.arg(connection_id)
  AND dedupe_fingerprint = sqlc.arg(dedupe_fingerprint)
  AND status IN ('open', 'acknowledged');

-- name: ListCharlieFindings :many
SELECT * FROM charlie_findings
WHERE connection_id = sqlc.arg(connection_id)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
ORDER BY updated_at DESC, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ExpireCharlieFindings :execrows
UPDATE charlie_findings
SET status = 'expired',
    execution_block_code = CASE WHEN approval_id IS NOT NULL THEN 'approval_expired' ELSE execution_block_code END,
    summary = CASE WHEN approval_id IS NOT NULL THEN 'The exact approval expired. No action was authorized.' ELSE summary END,
    updated_at = now()
WHERE status IN ('open', 'acknowledged')
  AND expires_at IS NOT NULL
  AND expires_at <= sqlc.arg(as_of);

-- name: DeleteCharlieFindingMetadataBefore :execrows
DELETE FROM charlie_findings
WHERE status IN ('dismissed', 'resolved', 'expired')
  AND updated_at < sqlc.arg(before);

-- name: DeleteCharlieSessionMetadataBefore :execrows
DELETE FROM charlie_sessions
WHERE state IN ('completed', 'aborted', 'failed')
  AND updated_at < sqlc.arg(before);

-- name: TransitionCharlieFinding :one
UPDATE charlie_findings
SET status = sqlc.arg(next_status),
    acknowledged_by_id = CASE WHEN sqlc.arg(next_status)::text = 'acknowledged' THEN sqlc.narg(actor_id) ELSE acknowledged_by_id END,
    acknowledged_at = CASE WHEN sqlc.arg(next_status)::text = 'acknowledged' THEN now() ELSE acknowledged_at END,
    dismissed_by_id = CASE WHEN sqlc.arg(next_status)::text = 'dismissed' THEN sqlc.narg(actor_id) ELSE dismissed_by_id END,
    dismissed_at = CASE WHEN sqlc.arg(next_status)::text = 'dismissed' THEN now() ELSE dismissed_at END,
    resolved_by_id = CASE WHEN sqlc.arg(next_status)::text = 'resolved' THEN sqlc.narg(actor_id) ELSE resolved_by_id END,
    resolved_at = CASE WHEN sqlc.arg(next_status)::text = 'resolved' THEN now() ELSE resolved_at END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND status = sqlc.arg(expected_status)
RETURNING *;

-- name: AddCharlieFindingResource :exec
INSERT INTO charlie_finding_resources (finding_id, resource_type, resource_id, required_verb)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: ListCharlieFindingResources :many
SELECT * FROM charlie_finding_resources WHERE finding_id = $1 ORDER BY resource_type, resource_id, required_verb;
