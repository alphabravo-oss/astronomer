-- Notification Channels

-- name: GetNotificationChannelByID :one
SELECT * FROM notification_channels WHERE id = $1;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListEnabledNotificationChannels :many
SELECT * FROM notification_channels WHERE enabled = true ORDER BY created_at DESC;

-- name: CreateNotificationChannel :one
INSERT INTO notification_channels (name, channel_type, configuration, enabled, created_by_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateNotificationChannel :one
UPDATE notification_channels SET
    name = $2,
    channel_type = $3,
    configuration = $4,
    enabled = $5
WHERE id = $1
RETURNING *;

-- name: DeleteNotificationChannel :exec
DELETE FROM notification_channels WHERE id = $1;

-- name: CountNotificationChannels :one
SELECT count(*) FROM notification_channels;

-- Alert Rules

-- name: GetAlertRuleByID :one
SELECT * FROM alert_rules WHERE id = $1;

-- name: ListAlertRules :many
SELECT * FROM alert_rules ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListAlertRulesByCluster :many
SELECT * FROM alert_rules WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAlertRulesByIDs :many
-- Batch-load rules for a page of alert events so the event-list response can
-- resolve rule name/severity without a per-row GetAlertRuleByID (the N+1 the
-- event-list path previously ran).
SELECT * FROM alert_rules WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: CreateAlertRule :one
INSERT INTO alert_rules (name, cluster_id, rule_type, configuration, severity, enabled, cooldown_minutes, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateAlertRule :one
UPDATE alert_rules SET
    name = $2,
    rule_type = $3,
    configuration = $4,
    severity = $5,
    enabled = $6,
    cooldown_minutes = $7
WHERE id = $1
RETURNING *;

-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = $1;

-- name: CountAlertRules :one
SELECT count(*) FROM alert_rules;

-- Alert Rule Channels (M2M)

-- name: AddAlertRuleChannel :exec
INSERT INTO alert_rule_channels (alert_rule_id, notification_channel_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveAlertRuleChannel :exec
DELETE FROM alert_rule_channels WHERE alert_rule_id = $1 AND notification_channel_id = $2;

-- name: ListChannelsForAlertRule :many
SELECT nc.* FROM notification_channels nc
INNER JOIN alert_rule_channels arc ON nc.id = arc.notification_channel_id
WHERE arc.alert_rule_id = $1;

-- name: ListAlertRuleChannelsByRules :many
-- Bulk fetch of rule<->channel links for a set of rules, so the
-- alertmanager renderer and rule list can build a rule_id->channel map
-- in Go instead of issuing one ListChannelsForAlertRule per rule (N+1).
SELECT * FROM alert_rule_channels
WHERE alert_rule_id = ANY(sqlc.arg(rule_ids)::uuid[]);

-- Alert Events

-- name: GetAlertEventByID :one
SELECT * FROM alert_events WHERE id = $1;

-- name: ListAlertEvents :many
SELECT * FROM alert_events ORDER BY fired_at DESC LIMIT $1 OFFSET $2;

-- name: ListAlertEventsByRule :many
SELECT * FROM alert_events WHERE rule_id = $1 ORDER BY fired_at DESC LIMIT $2 OFFSET $3;

-- name: CreateAlertEvent :one
INSERT INTO alert_events (rule_id, cluster_id, status, message, details)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateAlertEventStatus :exec
UPDATE alert_events SET status = $2, resolved_at = $3 WHERE id = $1;

-- name: AcknowledgeAlertEvent :exec
UPDATE alert_events SET acknowledged_by_id = $2, acknowledged_at = now() WHERE id = $1;

-- name: CountAlertEvents :one
SELECT count(*) FROM alert_events;

-- name: CountActiveAlertsByRules :many
-- Aggregates the active (firing/acknowledged) alert count per rule for a
-- set of rules in a single query, replacing the per-rule 200-event fetch
-- the rule list used to COUNT active alerts in Go.
SELECT rule_id, count(*) AS active_count
FROM alert_events
WHERE status IN ('firing', 'acknowledged')
  AND rule_id = ANY(sqlc.arg(rule_ids)::uuid[])
GROUP BY rule_id;

-- name: ListAlertEventsFiltered :many
-- Page of alert events with the status/severity/cluster filters pushed
-- into SQL (severity lives on the rule). Any filter is optional; a NULL
-- narg disables that predicate.
SELECT * FROM alert_events
WHERE (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
) AND (
    sqlc.narg(cluster_id)::uuid IS NULL OR cluster_id = sqlc.narg(cluster_id)::uuid
) AND (
    sqlc.narg(severity)::text IS NULL
    OR rule_id IN (SELECT id FROM alert_rules WHERE severity = sqlc.narg(severity)::text)
)
ORDER BY fired_at DESC
LIMIT $1 OFFSET $2;

-- name: CountAlertEventsFiltered :one
-- Total matching the same status/severity/cluster filters as
-- ListAlertEventsFiltered, so pagination reports a correct total.
SELECT count(*) FROM alert_events
WHERE (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
) AND (
    sqlc.narg(cluster_id)::uuid IS NULL OR cluster_id = sqlc.narg(cluster_id)::uuid
) AND (
    sqlc.narg(severity)::text IS NULL
    OR rule_id IN (SELECT id FROM alert_rules WHERE severity = sqlc.narg(severity)::text)
);

-- name: DeleteAlertEventsOlderThan :execrows
-- Deletes alert events older than the supplied cutoff. Used by the scheduled
-- cleanup_old_alert_events worker.
DELETE FROM alert_events WHERE fired_at < $1;

-- Alert Silences

-- name: ListAlertSilences :many
SELECT * FROM alert_silences ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: GetAlertSilenceByID :one
SELECT * FROM alert_silences WHERE id = $1;

-- name: CreateAlertSilence :one
INSERT INTO alert_silences (rule_id, cluster_id, reason, starts_at, ends_at, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteAlertSilence :exec
DELETE FROM alert_silences WHERE id = $1;

-- name: CountAlertSilences :one
SELECT count(*) FROM alert_silences;

-- Cluster lookups (alerting)

-- name: ListClustersByIDs :many
-- Bulk cluster lookup so the rule list can resolve every cluster name for
-- a page in one query instead of one GetClusterByID per rule.
SELECT * FROM clusters WHERE id = ANY(sqlc.arg(ids)::uuid[]);
