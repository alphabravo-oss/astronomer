-- name: LockIdentityProvisioning :exec
-- All generations of an installation share one transaction-scoped lock.
-- Hash collisions only serialize unrelated work; they do not authorize it.
SELECT pg_advisory_xact_lock(hashtextextended('astronomer-identity-provisioning:' || sqlc.arg(scope)::text, 0));

-- name: PrepareIdentityProvisioning :one
INSERT INTO identity_provisioning_operations
(id, issuer, project_id, installation_id, credential_namespace, generation,
 desired_spec_digest, operation, actor_id, correlation_id)
SELECT sqlc.arg(id), sqlc.arg(issuer), t.project_id, i.id,
 sqlc.arg(credential_namespace), sqlc.arg(generation), sqlc.arg(desired_spec_digest),
 sqlc.arg(operation), sqlc.arg(actor_id), sqlc.arg(correlation_id)
FROM installed_charts i JOIN delivery_targets t ON t.id = i.request_id
JOIN projects p ON p.id = t.project_id AND p.cluster_id = i.cluster_id
JOIN users u ON u.id = sqlc.arg(actor_id) AND u.is_active
WHERE i.id = sqlc.arg(installation_id) AND t.project_id = sqlc.arg(project_id)
 AND t.deletion_state = 'active' AND i.status NOT IN ('uninstalling', 'uninstalled')
ON CONFLICT (issuer, project_id, installation_id, generation) DO NOTHING
RETURNING *;

-- name: GetIdentityProvisioningScope :one
SELECT * FROM identity_provisioning_operations
WHERE issuer=$1 AND project_id=$2 AND installation_id=$3 AND generation=$4;

-- name: GetIdentityProvisioningOperation :one
SELECT * FROM identity_provisioning_operations WHERE id=$1;

-- name: ClaimIdentityProvisioning :one
UPDATE identity_provisioning_operations
SET state=CASE WHEN operation='rotate' THEN 'rotating' ELSE 'provisioning' END,
 revision=revision+1, updated_at=now()
WHERE identity_provisioning_operations.id=$1 AND identity_provisioning_operations.revision=$2
 AND identity_provisioning_operations.state='pending' AND identity_provisioning_operations.operation IN ('register','rotate')
 AND EXISTS (
 SELECT 1 FROM installed_charts i JOIN delivery_targets t ON t.id=i.request_id
 JOIN projects p ON p.id=t.project_id AND p.cluster_id=i.cluster_id
 JOIN users u ON u.id=identity_provisioning_operations.actor_id AND u.is_active
 WHERE i.id=identity_provisioning_operations.installation_id
 AND t.project_id=identity_provisioning_operations.project_id
 AND t.deletion_state='active' AND i.status NOT IN ('uninstalling','uninstalled'))
RETURNING *;

-- name: RecoverIdentityProvisioning :one
-- There is deliberately no transition back to pending: an interrupted creation
-- might already have succeeded at the issuer. Retrying creation loses secrets.
UPDATE identity_provisioning_operations
SET state='recovery_required', revision=revision+1, updated_at=now()
WHERE id=$1 AND revision=$2 AND state IN ('provisioning','rotating','retiring')
RETURNING *;

-- name: CompleteIdentityProvisioning :one
UPDATE identity_provisioning_operations
SET state='ready', revision=revision+1, updated_at=now(),
 upstream_org_id=$3, upstream_project_id=$4, upstream_app_id=$5,
 upstream_client_id=$6, credential_secret_name=$7
WHERE id=$1 AND revision=$2 AND state IN ('provisioning','rotating','recovery_required')
 AND operation IN ('register','rotate')
RETURNING *;
