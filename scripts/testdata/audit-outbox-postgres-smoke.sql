\set ON_ERROR_STOP on

-- A transaction rollback must remove the mutation's audit intent with it.
BEGIN;
INSERT INTO audit_outbox (
  id, dedupe_key, event_created_at, action, resource_type, detail
) VALUES (
  '00000000-0000-4000-8000-000000000015',
  'postgres-smoke:rollback', clock_timestamp(),
  'smoke.rollback', 'smoke_resource', '{"phase":"rollback"}'::jsonb
);
ROLLBACK;

DO $audit_smoke$
BEGIN
  IF EXISTS (
    SELECT 1 FROM audit_outbox
    WHERE id = '00000000-0000-4000-8000-000000000015'
  ) THEN
    RAISE EXCEPTION 'rolled-back audit intent survived';
  END IF;
END
$audit_smoke$;

-- Mirror DeliverAuditOutbox: the canonical insert and receipt transition are
-- one statement. Re-running it after a simulated post-insert lease replay must
-- preserve exactly one audit event.
INSERT INTO audit_outbox (
  id, dedupe_key, event_created_at, action, resource_type, detail, status,
  attempt_count, locked_until
) VALUES (
  '00000000-0000-4000-8000-000000000016',
  'postgres-smoke:delivery', clock_timestamp(),
  'smoke.delivery', 'smoke_resource', '{"phase":"committed"}'::jsonb,
  'delivering', 1, clock_timestamp() + interval '2 minutes'
);

WITH source AS (
  SELECT ao.* FROM audit_outbox AS ao
  WHERE ao.id = '00000000-0000-4000-8000-000000000016'
    AND ao.status = 'delivering'
), persisted AS (
  INSERT INTO audit_log (
    id, created_at, schema_version, user_id, actor_auth_method, action,
    resource_type, resource_id, resource_name, http_method, path,
    status_code, duration_ms, request_id, ip_address, user_agent, detail,
    source, correlation_id, action_class
  )
  SELECT
    id, event_created_at, schema_version, user_id, actor_auth_method, action,
    resource_type, resource_id, resource_name, http_method, path,
    status_code, duration_ms, request_id, ip_address, user_agent, detail,
    source, correlation_id, action_class
  FROM source
  ON CONFLICT (id, created_at) DO NOTHING
), marked AS (
  UPDATE audit_outbox AS o
  SET status = 'delivered', delivered_at = clock_timestamp(),
      locked_until = NULL, last_error = '', updated_at = clock_timestamp()
  WHERE o.id = '00000000-0000-4000-8000-000000000016'
    AND EXISTS (SELECT 1 FROM source)
  RETURNING o.id
)
SELECT count(*) FROM marked;

UPDATE audit_outbox
SET status = 'delivering', locked_until = clock_timestamp() - interval '1 second'
WHERE id = '00000000-0000-4000-8000-000000000016';

WITH source AS (
  SELECT ao.* FROM audit_outbox AS ao
  WHERE ao.id = '00000000-0000-4000-8000-000000000016'
    AND ao.status = 'delivering'
), persisted AS (
  INSERT INTO audit_log (
    id, created_at, schema_version, user_id, actor_auth_method, action,
    resource_type, resource_id, resource_name, http_method, path,
    status_code, duration_ms, request_id, ip_address, user_agent, detail,
    source, correlation_id, action_class
  )
  SELECT
    id, event_created_at, schema_version, user_id, actor_auth_method, action,
    resource_type, resource_id, resource_name, http_method, path,
    status_code, duration_ms, request_id, ip_address, user_agent, detail,
    source, correlation_id, action_class
  FROM source
  ON CONFLICT (id, created_at) DO NOTHING
), marked AS (
  UPDATE audit_outbox AS o
  SET status = 'delivered', delivered_at = clock_timestamp(),
      locked_until = NULL, last_error = '', updated_at = clock_timestamp()
  WHERE o.id = '00000000-0000-4000-8000-000000000016'
    AND EXISTS (SELECT 1 FROM source)
  RETURNING o.id
)
SELECT count(*) FROM marked;

DO $audit_smoke$
DECLARE
  event_count integer;
  receipt_status text;
BEGIN
  SELECT count(*) INTO event_count FROM audit_log
  WHERE id = '00000000-0000-4000-8000-000000000016';
  SELECT status INTO receipt_status FROM audit_outbox
  WHERE id = '00000000-0000-4000-8000-000000000016';
  IF event_count <> 1 OR receipt_status <> 'delivered' THEN
    RAISE EXCEPTION 'audit delivery is not idempotent: count=%, status=%',
      event_count, receipt_status;
  END IF;
END
$audit_smoke$;

-- The shared durable-operation idempotency claim must create an operation on
-- the first call and identify (without reinserting) that operation on replay.
-- This executes the same CTE shape used by the generated Go extension and the
-- required second-statement response attachment (sibling data-modifying CTEs
-- cannot observe one another's newly inserted rows through the shared snapshot).
CREATE TEMP TABLE operation_disposition_smoke (inserted boolean NOT NULL);

WITH claimed AS (
  INSERT INTO operation_idempotency_keys (
    scope, idempotency_key, operation_table, operation_id
  ) VALUES (
    'postgres-smoke', 'logging-disposition', 'logging_operations', gen_random_uuid()
  )
  ON CONFLICT (scope, idempotency_key) DO UPDATE
  SET operation_table = CASE WHEN operation_idempotency_keys.operation_table = '' THEN 'logging_operations' ELSE operation_idempotency_keys.operation_table END,
      operation_id = COALESCE(operation_idempotency_keys.operation_id, gen_random_uuid()),
      updated_at = now()
  RETURNING operation_table, operation_id
), inserted AS (
  INSERT INTO logging_operations (
    id, target_type, target_key, operation_type, payload, status
  )
  SELECT operation_id, 'output', 'postgres-smoke', 'apply', '{}'::jsonb, 'pending'
  FROM claimed
  WHERE operation_table = 'logging_operations'
  ON CONFLICT (id) DO NOTHING
  RETURNING *
)
INSERT INTO operation_disposition_smoke (inserted)
SELECT true FROM inserted
UNION ALL
SELECT false FROM logging_operations
JOIN claimed ON logging_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'logging_operations'
LIMIT 1;

UPDATE operation_idempotency_keys AS key
SET response = to_jsonb(op), updated_at = now()
FROM logging_operations AS op
WHERE key.scope = 'postgres-smoke'
  AND key.idempotency_key = 'logging-disposition'
  AND key.operation_id = op.id;

WITH claimed AS (
  INSERT INTO operation_idempotency_keys (
    scope, idempotency_key, operation_table, operation_id
  ) VALUES (
    'postgres-smoke', 'logging-disposition', 'logging_operations', gen_random_uuid()
  )
  ON CONFLICT (scope, idempotency_key) DO UPDATE
  SET operation_table = CASE WHEN operation_idempotency_keys.operation_table = '' THEN 'logging_operations' ELSE operation_idempotency_keys.operation_table END,
      operation_id = COALESCE(operation_idempotency_keys.operation_id, gen_random_uuid()),
      updated_at = now()
  RETURNING operation_table, operation_id
), inserted AS (
  INSERT INTO logging_operations (
    id, target_type, target_key, operation_type, payload, status
  )
  SELECT operation_id, 'output', 'postgres-smoke', 'apply', '{}'::jsonb, 'pending'
  FROM claimed
  WHERE operation_table = 'logging_operations'
  ON CONFLICT (id) DO NOTHING
  RETURNING *
)
INSERT INTO operation_disposition_smoke (inserted)
SELECT true FROM inserted
UNION ALL
SELECT false FROM logging_operations
JOIN claimed ON logging_operations.id = claimed.operation_id
WHERE claimed.operation_table = 'logging_operations'
LIMIT 1;

UPDATE operation_idempotency_keys AS key
SET response = to_jsonb(op), updated_at = now()
FROM logging_operations AS op
WHERE key.scope = 'postgres-smoke'
  AND key.idempotency_key = 'logging-disposition'
  AND key.operation_id = op.id;

DO $operation_idempotency_smoke$
DECLARE
  first_count integer;
  replay_count integer;
  operation_count integer;
  stored_response jsonb;
BEGIN
  SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted)
  INTO first_count, replay_count FROM operation_disposition_smoke;
  SELECT count(*) INTO operation_count FROM logging_operations
  WHERE target_key = 'postgres-smoke';
  SELECT response INTO stored_response FROM operation_idempotency_keys
  WHERE scope = 'postgres-smoke' AND idempotency_key = 'logging-disposition';
  IF first_count <> 1 OR replay_count <> 1 OR operation_count <> 1 OR stored_response = '{}'::jsonb THEN
    RAISE EXCEPTION 'durable operation disposition failed: first=%, replay=%, operations=%, response=%',
      first_count, replay_count, operation_count, stored_response;
  END IF;
END
$operation_idempotency_smoke$;

DELETE FROM logging_operations WHERE target_key = 'postgres-smoke';
DELETE FROM operation_idempotency_keys
WHERE scope = 'postgres-smoke' AND idempotency_key = 'logging-disposition';

DELETE FROM audit_log
WHERE id = '00000000-0000-4000-8000-000000000016';
DELETE FROM audit_outbox
WHERE id IN (
  '00000000-0000-4000-8000-000000000015',
  '00000000-0000-4000-8000-000000000016'
);
