package charlie

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// adminVisibilityDatabase intentionally exposes only QueryRow. Every query is
// a fixed projection selected by product code below; callers cannot supply SQL,
// table names, predicates, URLs, or credentials.
type adminVisibilityDatabase interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type AdminVisibilityCapabilityAdapter struct {
	database adminVisibilityDatabase
}

func NewAdminVisibilityCapabilityAdapter(database adminVisibilityDatabase) (*AdminVisibilityCapabilityAdapter, error) {
	if database == nil {
		return nil, fmt.Errorf("Charlie admin-visibility database is unavailable")
	}
	return &AdminVisibilityCapabilityAdapter{database: database}, nil
}

func AdminVisibilityCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	registrations := map[string]CapabilityExecutor{}
	for _, name := range []string{
		"astronomer.delivery.summary", "astronomer.delivery.email_health", "astronomer.delivery.webhook_health", "astronomer.delivery.siem_health",
		"astronomer.logging.health", "astronomer.monitoring.health", "astronomer.identity.health", "astronomer.authentication.health", "astronomer.rbac.health",
		"astronomer.security.posture", "astronomer.external_integrations.health", "astronomer.governance.health", "astronomer.policy_engine.health", "astronomer.templates.health",
		"astronomer.configuration.overview", "astronomer.tenancy.summary", "astronomer.registration.health", "astronomer.fleet_operations.health", "astronomer.gitops.health",
		"astronomer.extensions.health", "astronomer.alerting.health", "astronomer.catalog.health", "astronomer.reconciliation.health", "astronomer.dashboard.health", "astronomer.platform.inventory",
		"astronomer.charlie.runtime_health",
	} {
		registrations[name] = adapter
	}
	return registrations
}

func (a *AdminVisibilityCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, _ map[string]json.RawMessage) (json.RawMessage, error) {
	var value any
	var err error
	switch capability.Name {
	case "astronomer.delivery.summary":
		value, err = a.deliverySummary(ctx)
	case "astronomer.delivery.email_health":
		value, err = a.databaseJSON(ctx, emailHealthQuery)
	case "astronomer.delivery.webhook_health":
		value, err = a.databaseJSON(ctx, webhookHealthQuery)
	case "astronomer.delivery.siem_health":
		value, err = a.databaseJSON(ctx, siemHealthQuery)
	case "astronomer.logging.health":
		value, err = a.databaseJSON(ctx, loggingHealthQuery)
	case "astronomer.monitoring.health":
		value, err = a.databaseJSON(ctx, monitoringHealthQuery)
	case "astronomer.identity.health":
		value, err = a.databaseJSON(ctx, identityHealthQuery)
	case "astronomer.authentication.health":
		value, err = a.databaseJSON(ctx, authenticationHealthQuery)
	case "astronomer.rbac.health":
		value, err = a.databaseJSON(ctx, rbacHealthQuery)
	case "astronomer.security.posture":
		value, err = a.databaseJSON(ctx, securityPostureQuery)
	case "astronomer.external_integrations.health":
		value, err = a.databaseJSON(ctx, credentialsHealthQuery)
	case "astronomer.governance.health":
		value, err = a.databaseJSON(ctx, governanceHealthQuery)
	case "astronomer.policy_engine.health":
		value, err = a.databaseJSON(ctx, policyEngineHealthQuery)
	case "astronomer.templates.health":
		value, err = a.databaseJSON(ctx, templatesHealthQuery)
	case "astronomer.configuration.overview":
		value, err = a.databaseJSON(ctx, configurationOverviewQuery)
	case "astronomer.tenancy.summary":
		value, err = a.databaseJSON(ctx, tenancySummaryQuery)
	case "astronomer.registration.health":
		value, err = a.databaseJSON(ctx, registrationHealthQuery)
	case "astronomer.fleet_operations.health":
		value, err = a.databaseJSON(ctx, fleetOperationsHealthQuery)
	case "astronomer.gitops.health":
		value, err = a.databaseJSON(ctx, gitOpsHealthQuery)
	case "astronomer.extensions.health":
		value, err = a.databaseJSON(ctx, extensionsHealthQuery)
	case "astronomer.alerting.health":
		value, err = a.databaseJSON(ctx, alertingHealthQuery)
	case "astronomer.catalog.health":
		value, err = a.databaseJSON(ctx, catalogHealthQuery)
	case "astronomer.reconciliation.health":
		value, err = a.databaseJSON(ctx, reconciliationHealthQuery)
	case "astronomer.dashboard.health":
		value, err = a.databaseJSON(ctx, dashboardHealthQuery)
	case "astronomer.platform.inventory":
		value, err = a.databaseJSON(ctx, platformInventoryQuery)
	case "astronomer.charlie.runtime_health":
		value, err = a.databaseJSON(ctx, charlieRuntimeHealthQuery)
	default:
		return nil, fmt.Errorf("unsupported admin-visibility capability")
	}
	if err != nil {
		return nil, err
	}
	return marshalBounded(value, capability.MaxResponseBytes)
}

func (a *AdminVisibilityCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}

func (a *AdminVisibilityCapabilityAdapter) databaseJSON(ctx context.Context, query string) (any, error) {
	var raw []byte
	if err := a.database.QueryRow(ctx, query).Scan(&raw); err != nil {
		return nil, err
	}
	var value any
	if !json.Valid(raw) || json.Unmarshal(raw, &value) != nil {
		return nil, fmt.Errorf("admin-visibility projection is invalid")
	}
	return value, nil
}

func (a *AdminVisibilityCapabilityAdapter) deliverySummary(ctx context.Context) (map[string]any, error) {
	result := map[string]any{}
	for name, query := range map[string]string{"email": emailHealthQuery, "webhooks": webhookHealthQuery, "siem": siemHealthQuery} {
		value, err := a.databaseJSON(ctx, query)
		if err != nil {
			return nil, err
		}
		result[name] = value
	}
	channels, err := a.databaseJSON(ctx, notificationChannelHealthQuery)
	if err != nil {
		return nil, err
	}
	result["notification_channels"] = channels
	return result, nil
}

const emailHealthQuery = `
SELECT jsonb_build_object(
  'configured', EXISTS(SELECT 1 FROM smtp_settings),
  'enabled', COALESCE((SELECT enabled FROM smtp_settings LIMIT 1), false),
  'host_configured', COALESCE((SELECT host <> '' FROM smtp_settings LIMIT 1), false),
  'credentials_configured', COALESCE((SELECT username <> '' OR password_encrypted <> '' FROM smtp_settings LIMIT 1), false),
  'require_tls', COALESCE((SELECT require_tls FROM smtp_settings LIMIT 1), true),
  'encryption', COALESCE((SELECT encryption FROM smtp_settings LIMIT 1), ''),
  'messages_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM email_messages GROUP BY status) grouped), '{}'::jsonb),
  'queued_or_failed', (SELECT count(*)::bigint FROM email_messages WHERE status IN ('queued','failed')),
  'oldest_queued_age_seconds', COALESCE((SELECT extract(epoch FROM now() - min(created_at))::bigint FROM email_messages WHERE status = 'queued'), 0),
  'maximum_attempts_observed', COALESCE((SELECT max(attempts)::bigint FROM email_messages), 0)
)`

const webhookHealthQuery = `
SELECT jsonb_build_object(
  'subscriptions', (SELECT count(*)::bigint FROM webhook_subscriptions),
  'enabled_subscriptions', (SELECT count(*)::bigint FROM webhook_subscriptions WHERE enabled),
  'subscriptions_without_encrypted_secret', (SELECT count(*)::bigint FROM webhook_subscriptions WHERE enabled AND secret_encrypted = ''),
  'enabled_non_https_subscriptions', (SELECT count(*)::bigint FROM webhook_subscriptions WHERE enabled AND url !~* '^https://'),
  'deliveries_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM webhook_deliveries GROUP BY status) grouped), '{}'::jsonb),
  'oldest_pending_age_seconds', COALESCE((SELECT extract(epoch FROM now() - min(created_at))::bigint FROM webhook_deliveries WHERE status IN ('queued','failed')), 0),
  'maximum_attempts_observed', COALESCE((SELECT max(attempts)::bigint FROM webhook_deliveries), 0),
  'recent_failures_24h', (SELECT count(*)::bigint FROM webhook_deliveries WHERE status IN ('failed','dropped') AND created_at >= now() - interval '24 hours')
)`

const siemHealthQuery = `
SELECT jsonb_build_object(
  'forwarders', (SELECT count(*)::bigint FROM siem_forwarders),
  'enabled_forwarders', (SELECT count(*)::bigint FROM siem_forwarders WHERE enabled),
  'enabled_without_auth', (SELECT count(*)::bigint FROM siem_forwarders WHERE enabled AND auth_encrypted = '' AND transport IN ('splunk_hec','ndjson_https')),
  'tls_verification_disabled', (SELECT count(*)::bigint FROM siem_forwarders WHERE enabled AND tls_skip_verify),
  'plaintext_transports', (SELECT count(*)::bigint FROM siem_forwarders WHERE enabled AND transport IN ('syslog_udp','syslog_tcp')),
  'queue_depth', (SELECT count(*)::bigint FROM siem_forward_queue),
  'oldest_queue_age_seconds', COALESCE((SELECT extract(epoch FROM now() - min(created_at))::bigint FROM siem_forward_queue), 0),
  'maximum_attempts_observed', COALESCE((SELECT max(attempts)::bigint FROM siem_forward_queue), 0),
  'forwarders_reporting_errors', (SELECT count(*)::bigint FROM siem_forwarder_status WHERE last_error <> ''),
  'dropped_total', COALESCE((SELECT sum(dropped_total)::bigint FROM siem_forwarder_status), 0),
  'dispatched_total', COALESCE((SELECT sum(dispatched_total)::bigint FROM siem_forwarder_status), 0)
)`

const notificationChannelHealthQuery = `
SELECT jsonb_build_object(
  'channels', (SELECT count(*)::bigint FROM notification_channels),
  'enabled_channels', (SELECT count(*)::bigint FROM notification_channels WHERE enabled),
  'by_type', COALESCE((SELECT jsonb_object_agg(channel_type, total) FROM (SELECT channel_type, count(*)::bigint total FROM notification_channels GROUP BY channel_type) grouped), '{}'::jsonb),
  'template_overrides', (SELECT count(*)::bigint FROM notification_templates),
  'enabled_template_overrides', (SELECT count(*)::bigint FROM notification_templates WHERE enabled),
  'template_overrides_by_channel', COALESCE((SELECT jsonb_object_agg(channel, total) FROM (SELECT channel, count(*)::bigint total FROM notification_templates GROUP BY channel) grouped), '{}'::jsonb)
)`

const loggingHealthQuery = `
SELECT jsonb_build_object(
  'outputs', (SELECT count(*)::bigint FROM logging_outputs),
  'enabled_outputs', (SELECT count(*)::bigint FROM logging_outputs WHERE enabled),
  'outputs_by_type', COALESCE((SELECT jsonb_object_agg(output_type, total) FROM (SELECT output_type, count(*)::bigint total FROM logging_outputs GROUP BY output_type) grouped), '{}'::jsonb),
  'pipelines', (SELECT count(*)::bigint FROM logging_pipelines),
  'enabled_pipelines', (SELECT count(*)::bigint FROM logging_pipelines WHERE enabled),
  'operations_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM logging_operations GROUP BY status) grouped), '{}'::jsonb)
)`

const monitoringHealthQuery = `
SELECT jsonb_build_object(
  'backends', (SELECT count(*)::bigint FROM monitoring_backends),
  'default_backends', (SELECT count(*)::bigint FROM monitoring_backends WHERE is_default),
  'backends_by_type', COALESCE((SELECT jsonb_object_agg(backend_type, total) FROM (SELECT backend_type, count(*)::bigint total FROM monitoring_backends GROUP BY backend_type) grouped), '{}'::jsonb),
  'missing_query_endpoint', (SELECT count(*)::bigint FROM monitoring_backends WHERE query_url = ''),
  'credentialed_backends', (SELECT count(*)::bigint FROM monitoring_backends WHERE auth_type <> 'none'),
  'legacy_unsealed_credentials', (SELECT count(*)::bigint FROM monitoring_backends WHERE auth_config_encrypted = '' AND auth_config <> '{}'::jsonb),
  'alertmanager_configured', (SELECT count(*)::bigint FROM monitoring_backends WHERE alertmanager_url <> ''),
  'cluster_config_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM cluster_monitoring_configs GROUP BY status) grouped), '{}'::jsonb),
  'operations_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM monitoring_operations GROUP BY status) grouped), '{}'::jsonb)
)`

const identityHealthQuery = `
SELECT jsonb_build_object(
  'users', (SELECT count(*)::bigint FROM users),
  'active_users', (SELECT count(*)::bigint FROM users WHERE is_active),
  'locked_users', (SELECT count(*)::bigint FROM users WHERE locked_until > now()),
  'users_requiring_password_change', (SELECT count(*)::bigint FROM users WHERE must_change_password),
  'sso_providers', (SELECT count(*)::bigint FROM sso_configurations),
  'enabled_sso_providers', (SELECT count(*)::bigint FROM sso_configurations WHERE is_enabled),
  'dex_connectors', (SELECT count(*)::bigint FROM dex_connectors),
  'enabled_dex_connectors', (SELECT count(*)::bigint FROM dex_connectors WHERE enabled),
  'dex_runtime_configured', EXISTS(SELECT 1 FROM dex_settings),
  'dex_runtime_generation_pending', COALESCE((SELECT runtime_generation > runtime_applied_generation FROM dex_settings LIMIT 1), false),
  'identity_group_mappings', (SELECT count(*)::bigint FROM identity_group_mappings),
  'cached_idp_group_memberships', (SELECT count(*)::bigint FROM user_idp_groups),
  'scim_tokens', (SELECT count(*)::bigint FROM scim_tokens),
  'unused_scim_tokens', (SELECT count(*)::bigint FROM scim_tokens WHERE last_used_at IS NULL)
)`

const authenticationHealthQuery = `
SELECT jsonb_build_object(
  'active_local_accounts', (SELECT count(*)::bigint FROM users WHERE is_active AND password <> '' AND NOT is_service),
  'active_service_accounts', (SELECT count(*)::bigint FROM users WHERE is_active AND is_service),
  'totp_enrolled_accounts', (SELECT count(*)::bigint FROM user_totp_enrollments enrollment JOIN users account ON account.id = enrollment.user_id WHERE account.is_active),
  'active_local_accounts_without_totp', (SELECT count(*)::bigint FROM users account WHERE account.is_active AND account.password <> '' AND NOT account.is_service AND NOT EXISTS (SELECT 1 FROM user_totp_enrollments enrollment WHERE enrollment.user_id = account.id)),
  'totp_accounts_without_recovery_codes', (SELECT count(*)::bigint FROM user_totp_enrollments enrollment WHERE NOT EXISTS (SELECT 1 FROM user_totp_recovery_codes code WHERE code.user_id = enrollment.user_id AND code.used_at IS NULL)),
  'unused_totp_recovery_codes', (SELECT count(*)::bigint FROM user_totp_recovery_codes WHERE used_at IS NULL),
  'active_sso_sessions', (SELECT count(*)::bigint FROM sso_sessions WHERE expires_at > now()),
  'expired_sso_sessions_pending_cleanup', (SELECT count(*)::bigint FROM sso_sessions WHERE expires_at <= now()),
  'active_sso_sessions_without_encrypted_upstream_token', (SELECT count(*)::bigint FROM sso_sessions WHERE expires_at > now() AND upstream_id_token_encrypted = ''),
  'active_sso_sessions_with_upstream_logout', (SELECT count(*)::bigint FROM sso_sessions WHERE expires_at > now() AND end_session_endpoint <> ''),
  'active_password_reset_tokens', (SELECT count(*)::bigint FROM password_reset_tokens WHERE used_at IS NULL AND expires_at > now()),
  'expired_password_reset_tokens_pending_cleanup', (SELECT count(*)::bigint FROM password_reset_tokens WHERE used_at IS NULL AND expires_at <= now())
)`

const rbacHealthQuery = `
SELECT jsonb_build_object(
  'global_roles', (SELECT count(*)::bigint FROM global_roles),
  'global_bindings', (SELECT count(*)::bigint FROM global_role_bindings),
  'cluster_roles', (SELECT count(*)::bigint FROM cluster_roles),
  'cluster_bindings', (SELECT count(*)::bigint FROM cluster_role_bindings),
  'project_roles', (SELECT count(*)::bigint FROM project_roles),
  'project_bindings', (SELECT count(*)::bigint FROM project_role_bindings),
  'user_bindings', (SELECT count(*)::bigint FROM global_role_bindings WHERE user_id IS NOT NULL) + (SELECT count(*)::bigint FROM cluster_role_bindings WHERE user_id IS NOT NULL) + (SELECT count(*)::bigint FROM project_role_bindings WHERE user_id IS NOT NULL),
  'group_bindings', (SELECT count(*)::bigint FROM global_role_bindings WHERE "group" <> '') + (SELECT count(*)::bigint FROM cluster_role_bindings WHERE "group" <> '') + (SELECT count(*)::bigint FROM project_role_bindings WHERE "group" <> ''),
  'invalid_subject_bindings', (SELECT count(*)::bigint FROM global_role_bindings WHERE user_id IS NULL AND "group" = '') + (SELECT count(*)::bigint FROM cluster_role_bindings WHERE user_id IS NULL AND "group" = '') + (SELECT count(*)::bigint FROM project_role_bindings WHERE user_id IS NULL AND "group" = ''),
  'identity_group_mappings', (SELECT count(*)::bigint FROM identity_group_mappings),
  'native_rules', (SELECT count(*)::bigint FROM native_rbac_rules),
  'global_scope_native_rules', (SELECT count(*)::bigint FROM native_rbac_rules WHERE cluster_id IS NULL),
  'wildcard_resource_native_rules', (SELECT count(*)::bigint FROM native_rbac_rules WHERE resource = '*'),
  'wildcard_verb_native_rules', (SELECT count(*)::bigint FROM native_rbac_rules WHERE '*' = ANY(verbs))
)`

const securityPostureQuery = `
SELECT jsonb_build_object(
  'active_api_tokens', (SELECT count(*)::bigint FROM api_tokens WHERE NOT is_revoked AND (expires_at IS NULL OR expires_at > now())),
  'expired_unrevoked_api_tokens', (SELECT count(*)::bigint FROM api_tokens WHERE NOT is_revoked AND expires_at <= now()),
  'api_tokens_without_expiry', (SELECT count(*)::bigint FROM api_tokens WHERE NOT is_revoked AND expires_at IS NULL),
  'expired_jwt_revocations_pending_cleanup', (SELECT count(*)::bigint FROM jwt_revocations WHERE expires_at <= now()),
  'vault_connections', (SELECT count(*)::bigint FROM vault_connections),
  'enabled_vault_connections', (SELECT count(*)::bigint FROM vault_connections WHERE enabled),
  'unhealthy_vault_connections', (SELECT count(*)::bigint FROM vault_connections WHERE enabled AND NOT last_health_ok),
  'vault_tls_verification_disabled', (SELECT count(*)::bigint FROM vault_connections WHERE enabled AND tls_skip_verify),
  'monitoring_legacy_unsealed_credentials', (SELECT count(*)::bigint FROM monitoring_backends WHERE auth_config_encrypted = '' AND auth_config <> '{}'::jsonb),
  'siem_tls_verification_disabled', (SELECT count(*)::bigint FROM siem_forwarders WHERE enabled AND tls_skip_verify),
  'prometheus_tls_verification_disabled', (SELECT count(*)::bigint FROM prometheus_datasources WHERE enabled AND tls_skip_verify),
  'active_compliance_baselines', (SELECT count(*)::bigint FROM compliance_baseline_applications WHERE status = 'applied'),
  'users_requiring_password_change', (SELECT count(*)::bigint FROM users WHERE must_change_password),
  'currently_locked_users', (SELECT count(*)::bigint FROM users WHERE locked_until > now())
)`

const credentialsHealthQuery = `
SELECT jsonb_build_object(
  'project_cloud_credentials', (SELECT count(*)::bigint FROM cloud_credentials),
  'cloud_credentials_by_provider', COALESCE((SELECT jsonb_object_agg(provider, total) FROM (SELECT provider, count(*)::bigint total FROM cloud_credentials GROUP BY provider) grouped), '{}'::jsonb),
  'cloud_credentials_without_encrypted_material', (SELECT count(*)::bigint FROM cloud_credentials WHERE data_encrypted = ''),
  'cloud_credential_materializations_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM cloud_credential_materializations GROUP BY status) grouped), '{}'::jsonb),
  'stale_pending_materializations', (SELECT count(*)::bigint FROM cloud_credential_materializations WHERE status = 'pending' AND updated_at < now() - interval '15 minutes'),
  'failed_materializations', (SELECT count(*)::bigint FROM cloud_credential_materializations WHERE status = 'failed'),
  'vault_connections', (SELECT count(*)::bigint FROM vault_connections),
  'enabled_vault_connections', (SELECT count(*)::bigint FROM vault_connections WHERE enabled),
  'vault_connections_by_auth_method', COALESCE((SELECT jsonb_object_agg(auth_method, total) FROM (SELECT auth_method, count(*)::bigint total FROM vault_connections GROUP BY auth_method) grouped), '{}'::jsonb),
  'enabled_vault_connections_without_encrypted_auth', (SELECT count(*)::bigint FROM vault_connections WHERE enabled AND auth_encrypted = ''),
  'unhealthy_vault_connections', (SELECT count(*)::bigint FROM vault_connections WHERE enabled AND NOT last_health_ok),
  'stale_vault_health_checks', (SELECT count(*)::bigint FROM vault_connections WHERE enabled AND (last_health_at IS NULL OR last_health_at < now() - interval '1 hour')),
  'vault_tls_verification_disabled', (SELECT count(*)::bigint FROM vault_connections WHERE enabled AND tls_skip_verify)
)`

const governanceHealthQuery = `
SELECT jsonb_build_object(
  'maintenance_windows', (SELECT count(*)::bigint FROM maintenance_windows),
  'enabled_maintenance_windows', (SELECT count(*)::bigint FROM maintenance_windows WHERE enabled),
  'maintenance_modes', COALESCE((SELECT jsonb_object_agg(mode, total) FROM (SELECT mode, count(*)::bigint total FROM maintenance_windows WHERE enabled GROUP BY mode) grouped), '{}'::jsonb),
  'deferred_operations_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM deferred_operations GROUP BY status) grouped), '{}'::jsonb),
  'expired_pending_deferred_operations', (SELECT count(*)::bigint FROM deferred_operations WHERE status = 'pending' AND expires_at <= now()),
  'quota_plans', (SELECT count(*)::bigint FROM quota_plans),
  'hard_quota_plans', (SELECT count(*)::bigint FROM quota_plans WHERE enforcement = 'hard'),
  'active_compliance_baselines', (SELECT count(*)::bigint FROM compliance_baseline_applications WHERE status = 'applied'),
  'enabled_read_audit_policies', (SELECT count(*)::bigint FROM read_audit_policies WHERE enabled),
  'enabled_alert_inhibitions', (SELECT count(*)::bigint FROM alert_inhibitions WHERE enabled),
  'network_policy_templates', (SELECT count(*)::bigint FROM network_policy_templates)
)`

const policyEngineHealthQuery = `
SELECT jsonb_build_object(
  'controller_policies', (SELECT count(*)::bigint FROM control_plane_policies),
  'maximum_recent_failure_window_minutes', COALESCE((SELECT max(recent_failure_window_minutes)::bigint FROM control_plane_policies), 0),
  'active_controller_alerts', (SELECT count(*)::bigint FROM control_plane_alerts WHERE status = 'active'),
  'acknowledged_active_controller_alerts', (SELECT count(*)::bigint FROM control_plane_alerts WHERE status = 'active' AND acknowledged_at IS NOT NULL),
  'controller_alerts_by_condition', COALESCE((SELECT jsonb_object_agg(condition_type, total) FROM (SELECT condition_type, count(*)::bigint total FROM control_plane_alerts WHERE status = 'active' GROUP BY condition_type) grouped), '{}'::jsonb),
  'active_controller_silences', (SELECT count(*)::bigint FROM control_plane_silences WHERE starts_at <= now() AND ends_at > now()),
  'expired_controller_silences_pending_cleanup', (SELECT count(*)::bigint FROM control_plane_silences WHERE ends_at <= now()),
  'future_controller_silences', (SELECT count(*)::bigint FROM control_plane_silences WHERE starts_at > now()),
  'enabled_product_alert_rules', (SELECT count(*)::bigint FROM alert_rules WHERE enabled),
  'active_product_alert_silences', (SELECT count(*)::bigint FROM alert_silences WHERE starts_at <= now() AND ends_at > now())
)`

const templatesHealthQuery = `
SELECT jsonb_build_object(
  'cluster_templates', (SELECT count(*)::bigint FROM cluster_templates),
  'cluster_template_applications_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM cluster_template_applications GROUP BY status) grouped), '{}'::jsonb),
  'failed_cluster_template_applications', (SELECT count(*)::bigint FROM cluster_template_applications WHERE status = 'failed'),
  'pod_security_templates', (SELECT count(*)::bigint FROM pod_security_templates),
  'default_pod_security_templates', (SELECT count(*)::bigint FROM pod_security_templates WHERE is_default),
  'cluster_security_policies_by_status', COALESCE((SELECT jsonb_object_agg(sync_status, total) FROM (SELECT sync_status, count(*)::bigint total FROM cluster_security_policies GROUP BY sync_status) grouped), '{}'::jsonb),
  'network_policy_templates', (SELECT count(*)::bigint FROM network_policy_templates),
  'enabled_network_policy_templates', (SELECT count(*)::bigint FROM network_policy_templates WHERE enabled),
  'network_policy_applications_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM network_policy_applications GROUP BY status) grouped), '{}'::jsonb),
  'compliance_baselines', (SELECT count(*)::bigint FROM compliance_baselines WHERE enabled),
  'active_compliance_baseline_applications', (SELECT count(*)::bigint FROM compliance_baseline_applications WHERE status = 'applied')
)`

const configurationOverviewQuery = `
WITH overrides AS (
  SELECT count(*)::bigint AS total,
    COALESCE(jsonb_agg(jsonb_build_object(
      'key', key,
      'value_type', COALESCE(jsonb_typeof(value), 'null'),
      'value', CASE
        WHEN key ~* '(secret|password|token|credential|private|certificate|bundle|ca_)' THEN jsonb_build_object('configured', value IS NOT NULL AND value <> 'null'::jsonb, 'redacted', true)
        WHEN jsonb_typeof(value) IN ('boolean','number') THEN value
        ELSE jsonb_build_object('configured', value IS NOT NULL AND value <> 'null'::jsonb, 'bytes', octet_length(value::text))
      END,
      'updated_at', updated_at
    ) ORDER BY key), '[]'::jsonb) AS settings
  FROM platform_settings
)
SELECT jsonb_build_object(
  'platform', jsonb_build_object(
    'configured', EXISTS(SELECT 1 FROM platform_configuration),
    'server_url_configured', COALESCE((SELECT server_url <> '' FROM platform_configuration LIMIT 1), false),
    'telemetry_enabled', COALESCE((SELECT telemetry_enabled FROM platform_configuration LIMIT 1), false),
    'bootstrapped', COALESCE((SELECT bootstrapped_at IS NOT NULL FROM platform_configuration LIMIT 1), false)
  ),
  'stored_override_count', overrides.total,
  'settings', overrides.settings
) FROM overrides`

const tenancySummaryQuery = `
SELECT jsonb_build_object(
  'users', (SELECT count(*)::bigint FROM users),
  'projects', (SELECT count(*)::bigint FROM projects),
  'cluster_registrations', (SELECT count(*)::bigint FROM clusters),
  'cluster_registrations_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM clusters GROUP BY status) grouped), '{}'::jsonb),
  'cluster_registrations_by_environment', COALESCE((SELECT jsonb_object_agg(environment, total) FROM (SELECT environment, count(*)::bigint total FROM clusters GROUP BY environment) grouped), '{}'::jsonb),
  'quota_plans', (SELECT count(*)::bigint FROM quota_plans),
  'projects_by_quota_plan', COALESCE((SELECT jsonb_object_agg(quota_plan, total) FROM (SELECT quota_plan, count(*)::bigint total FROM projects GROUP BY quota_plan) grouped), '{}'::jsonb),
  'users_by_quota_plan', COALESCE((SELECT jsonb_object_agg(quota_plan, total) FROM (SELECT quota_plan, count(*)::bigint total FROM users GROUP BY quota_plan) grouped), '{}'::jsonb)
)`

const registrationHealthQuery = `
SELECT jsonb_build_object(
  'registrations_by_phase', COALESCE((SELECT jsonb_object_agg(registration_phase, total) FROM (SELECT registration_phase, count(*)::bigint total FROM clusters WHERE decommissioned_at IS NULL GROUP BY registration_phase) grouped), '{}'::jsonb),
  'registrations_stalled_15m', (SELECT count(*)::bigint FROM clusters WHERE decommissioned_at IS NULL AND registration_phase NOT IN ('ready','failed') AND updated_at < now() - interval '15 minutes'),
  'registration_steps_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM cluster_registration_steps GROUP BY status) grouped), '{}'::jsonb),
  'registration_steps_stalled_15m', (SELECT count(*)::bigint FROM cluster_registration_steps WHERE status IN ('pending','running') AND created_at < now() - interval '15 minutes'),
  'agent_lifecycle_operations_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM agent_lifecycle_operations GROUP BY status) grouped), '{}'::jsonb),
  'agent_lifecycle_operations_stalled_30m', (SELECT count(*)::bigint FROM agent_lifecycle_operations WHERE status IN ('pending','running') AND updated_at < now() - interval '30 minutes'),
  'decommissions_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM cluster_decommissions GROUP BY status) grouped), '{}'::jsonb),
  'decommissions_stalled_30m', (SELECT count(*)::bigint FROM cluster_decommissions WHERE status IN ('pending','running') AND updated_at < now() - interval '30 minutes'),
  'decommission_attempts_total', COALESCE((SELECT sum(attempts)::bigint FROM cluster_decommissions), 0)
)`

const fleetOperationsHealthQuery = `
SELECT jsonb_build_object(
  'operations_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM fleet_operations GROUP BY status) grouped), '{}'::jsonb),
  'operations_by_type', COALESCE((SELECT jsonb_object_agg(operation_type, total) FROM (SELECT operation_type, count(*)::bigint total FROM fleet_operations GROUP BY operation_type) grouped), '{}'::jsonb),
  'running_operations', (SELECT count(*)::bigint FROM fleet_operations WHERE status = 'running'),
  'stalled_running_operations', (SELECT count(*)::bigint FROM fleet_operations WHERE status = 'running' AND updated_at < now() - interval '30 minutes'),
  'maximum_configured_concurrency', COALESCE((SELECT max(max_concurrent)::bigint FROM fleet_operations), 0),
  'failed_cluster_targets_total', COALESCE((SELECT sum(failed_clusters)::bigint FROM fleet_operations), 0),
  'targets_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM fleet_operation_targets GROUP BY status) grouped), '{}'::jsonb),
  'stalled_running_targets', (SELECT count(*)::bigint FROM fleet_operation_targets WHERE status = 'running' AND updated_at < now() - interval '30 minutes')
)`

const gitOpsHealthQuery = `
SELECT jsonb_build_object(
  'sources', (SELECT count(*)::bigint FROM gitops_registration_sources),
  'enabled_sources', (SELECT count(*)::bigint FROM gitops_registration_sources WHERE enabled),
  'sources_with_errors', (SELECT count(*)::bigint FROM gitops_registration_sources WHERE enabled AND last_error <> ''),
  'never_synchronized_sources', (SELECT count(*)::bigint FROM gitops_registration_sources WHERE enabled AND last_synced_at IS NULL),
  'stale_interval_sources', (SELECT count(*)::bigint FROM gitops_registration_sources WHERE enabled AND sync_mode = 'interval' AND (last_synced_at IS NULL OR last_synced_at < now() - make_interval(secs => greatest(sync_interval_seconds * 3, 300)))),
  'credentialed_sources', (SELECT count(*)::bigint FROM gitops_registration_sources WHERE auth_mode <> 'none'),
  'credentialed_sources_without_envelope', (SELECT count(*)::bigint FROM gitops_registration_sources WHERE auth_mode <> 'none' AND auth_encrypted = ''),
  'dangerous_delete_policy_sources', (SELECT count(*)::bigint FROM gitops_registration_sources WHERE enabled AND on_delete = 'decommission'),
  'registered_clusters', (SELECT count(*)::bigint FROM gitops_registered_clusters)
)`

const extensionsHealthQuery = `
SELECT jsonb_build_object(
  'extensions', count(*)::bigint,
  'enabled_extensions', count(*) FILTER (WHERE enabled)::bigint,
  'incompatible_extensions', count(*) FILTER (WHERE compatibility_status = 'incompatible')::bigint,
  'unknown_compatibility', count(*) FILTER (WHERE compatibility_status = 'unknown')::bigint,
  'enabled_unverified_bundles', count(*) FILTER (WHERE enabled AND NOT bundle_verified)::bigint,
  'by_source', COALESCE((SELECT jsonb_object_agg(source, total) FROM (SELECT source, count(*)::bigint total FROM ui_extensions GROUP BY source) grouped), '{}'::jsonb)
) FROM ui_extensions`

const alertingHealthQuery = `
SELECT jsonb_build_object(
  'rules', (SELECT count(*)::bigint FROM alert_rules),
  'enabled_rules', (SELECT count(*)::bigint FROM alert_rules WHERE enabled),
  'rules_without_channels', (SELECT count(*)::bigint FROM alert_rules rule WHERE rule.enabled AND NOT EXISTS (SELECT 1 FROM alert_rule_channels link WHERE link.alert_rule_id = rule.id)),
  'notification_channels', (SELECT count(*)::bigint FROM notification_channels),
  'enabled_notification_channels', (SELECT count(*)::bigint FROM notification_channels WHERE enabled),
  'events_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM alert_events GROUP BY status) grouped), '{}'::jsonb),
  'charlie_alert_deliveries_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM charlie_alert_deliveries GROUP BY status) grouped), '{}'::jsonb)
)`

const catalogHealthQuery = `
SELECT jsonb_build_object(
  'repositories', (SELECT count(*)::bigint FROM helm_repositories),
  'enabled_repositories', (SELECT count(*)::bigint FROM helm_repositories WHERE enabled),
  'repositories_with_sync_errors', (SELECT count(*)::bigint FROM helm_repositories WHERE enabled AND last_sync_error <> ''),
  'repositories_never_synchronized', (SELECT count(*)::bigint FROM helm_repositories WHERE enabled AND last_synced_at IS NULL),
  'charts', (SELECT count(*)::bigint FROM helm_charts),
  'deprecated_charts', (SELECT count(*)::bigint FROM helm_charts WHERE deprecated),
  'chart_versions', (SELECT count(*)::bigint FROM helm_chart_versions),
  'unhydrated_chart_versions', (SELECT count(*)::bigint FROM helm_chart_versions WHERE content_hydrated_at IS NULL),
  'blessed_charts', (SELECT count(*)::bigint FROM catalog_blessed_charts),
  'management_unsafe_blessed_charts', (SELECT count(*)::bigint FROM catalog_blessed_charts WHERE NOT mgmt_safe),
  'installed_charts_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM installed_charts GROUP BY status) grouped), '{}'::jsonb),
  'charts_with_ratings', (SELECT count(*)::bigint FROM chart_rating_aggregates),
  'ratings', (SELECT count(*)::bigint FROM chart_ratings)
)`

const reconciliationHealthQuery = `
SELECT jsonb_build_object(
  'repair_jobs_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM repair_job_states GROUP BY status) grouped), '{}'::jsonb),
  'repair_jobs_with_failures', (SELECT count(*)::bigint FROM repair_job_states WHERE status = 'failed' OR error_count > 0),
  'repair_job_errors_total', COALESCE((SELECT sum(error_count)::bigint FROM repair_job_states), 0),
  'repair_jobs_never_successful', (SELECT count(*)::bigint FROM repair_job_states WHERE last_successful_reconcile_at IS NULL),
  'stale_repair_jobs_1h', (SELECT count(*)::bigint FROM repair_job_states WHERE updated_at < now() - interval '1 hour'),
  'idempotency_records', (SELECT count(*)::bigint FROM operation_idempotency_keys),
  'idempotency_records_without_operation', (SELECT count(*)::bigint FROM operation_idempotency_keys WHERE operation_id IS NULL AND created_at < now() - interval '10 minutes'),
  'idempotency_records_without_operation_table', (SELECT count(*)::bigint FROM operation_idempotency_keys WHERE operation_table = '')
)`

const dashboardHealthQuery = `
SELECT jsonb_build_object(
  'widgets', (SELECT count(*)::bigint FROM dashboard_widgets),
  'enabled_widgets', (SELECT count(*)::bigint FROM dashboard_widgets WHERE enabled),
  'widgets_by_type', COALESCE((SELECT jsonb_object_agg(widget_type, total) FROM (SELECT widget_type, count(*)::bigint total FROM dashboard_widgets GROUP BY widget_type) grouped), '{}'::jsonb),
  'widgets_by_scope', COALESCE((SELECT jsonb_object_agg(scope, total) FROM (SELECT scope, count(*)::bigint total FROM dashboard_widgets GROUP BY scope) grouped), '{}'::jsonb),
  'prometheus_datasources', (SELECT count(*)::bigint FROM prometheus_datasources),
  'enabled_prometheus_datasources', (SELECT count(*)::bigint FROM prometheus_datasources WHERE enabled),
  'credentialed_prometheus_datasources', (SELECT count(*)::bigint FROM prometheus_datasources WHERE auth_encrypted <> ''),
  'prometheus_tls_verification_disabled', (SELECT count(*)::bigint FROM prometheus_datasources WHERE enabled AND tls_skip_verify)
)`

const platformInventoryQuery = `
SELECT jsonb_build_object(
  'identity', jsonb_build_object('users', (SELECT count(*)::bigint FROM users), 'sso_providers', (SELECT count(*)::bigint FROM sso_configurations), 'dex_connectors', (SELECT count(*)::bigint FROM dex_connectors), 'active_sso_sessions', (SELECT count(*)::bigint FROM sso_sessions WHERE expires_at > now()), 'totp_enrollments', (SELECT count(*)::bigint FROM user_totp_enrollments)),
  'tenancy', jsonb_build_object('cluster_registrations', (SELECT count(*)::bigint FROM clusters), 'projects', (SELECT count(*)::bigint FROM projects), 'quota_plans', (SELECT count(*)::bigint FROM quota_plans)),
  'catalog', jsonb_build_object('repositories', (SELECT count(*)::bigint FROM helm_repositories), 'charts', (SELECT count(*)::bigint FROM helm_charts), 'chart_versions', (SELECT count(*)::bigint FROM helm_chart_versions), 'tools', (SELECT count(*)::bigint FROM cluster_tools)),
  'operations', jsonb_build_object('catalog', (SELECT count(*)::bigint FROM catalog_operations), 'argocd', (SELECT count(*)::bigint FROM argocd_operations), 'tools', (SELECT count(*)::bigint FROM tool_operations), 'monitoring', (SELECT count(*)::bigint FROM monitoring_operations), 'logging', (SELECT count(*)::bigint FROM logging_operations), 'workloads', (SELECT count(*)::bigint FROM workload_operations)),
  'observability', jsonb_build_object('monitoring_backends', (SELECT count(*)::bigint FROM monitoring_backends), 'logging_outputs', (SELECT count(*)::bigint FROM logging_outputs), 'logging_pipelines', (SELECT count(*)::bigint FROM logging_pipelines), 'dashboard_widgets', (SELECT count(*)::bigint FROM dashboard_widgets)),
  'delivery', jsonb_build_object('notification_channels', (SELECT count(*)::bigint FROM notification_channels), 'webhook_subscriptions', (SELECT count(*)::bigint FROM webhook_subscriptions), 'siem_forwarders', (SELECT count(*)::bigint FROM siem_forwarders), 'email_messages', (SELECT count(*)::bigint FROM email_messages)),
  'governance', jsonb_build_object('alert_rules', (SELECT count(*)::bigint FROM alert_rules), 'maintenance_windows', (SELECT count(*)::bigint FROM maintenance_windows), 'read_audit_policies', (SELECT count(*)::bigint FROM read_audit_policies), 'compliance_baselines', (SELECT count(*)::bigint FROM compliance_baselines), 'cluster_templates', (SELECT count(*)::bigint FROM cluster_templates), 'network_policy_templates', (SELECT count(*)::bigint FROM network_policy_templates)),
  'integrations', jsonb_build_object('cloud_credentials', (SELECT count(*)::bigint FROM cloud_credentials), 'vault_connections', (SELECT count(*)::bigint FROM vault_connections), 'gitops_sources', (SELECT count(*)::bigint FROM gitops_registration_sources), 'ui_extensions', (SELECT count(*)::bigint FROM ui_extensions)),
  'reliability', jsonb_build_object('backups', (SELECT count(*)::bigint FROM backups), 'task_outbox', (SELECT count(*)::bigint FROM task_outbox), 'queue_alerts', (SELECT count(*)::bigint FROM control_plane_alerts), 'repair_jobs', (SELECT count(*)::bigint FROM repair_job_states), 'fleet_operations', (SELECT count(*)::bigint FROM fleet_operations)),
  'charlie', jsonb_build_object('connections', (SELECT count(*)::bigint FROM charlie_connections), 'sessions', (SELECT count(*)::bigint FROM charlie_sessions), 'findings', (SELECT count(*)::bigint FROM charlie_findings))
)`

const charlieRuntimeHealthQuery = `
SELECT jsonb_build_object(
  'connections', (SELECT count(*)::bigint FROM charlie_connections),
  'active_connections', (SELECT count(*)::bigint FROM charlie_connections WHERE active),
  'ready_connections', (SELECT count(*)::bigint FROM charlie_connections WHERE active AND health_state = 'ready'),
  'emergency_disabled_connections', (SELECT count(*)::bigint FROM charlie_connections WHERE emergency_disabled),
  'mode_mismatch_connections', (SELECT count(*)::bigint FROM charlie_connections WHERE requested_mode <> verified_mode),
  'disclosure_mismatch_connections', (SELECT count(*)::bigint FROM charlie_connections WHERE disclosure_digest = '' OR disclosure_digest <> acknowledged_disclosure_digest),
  'expired_artifact_credentials', (SELECT count(*)::bigint FROM charlie_connections WHERE active AND artifact_credential_expires_at <= now()),
  'certificates_expiring_7d', (SELECT count(*)::bigint FROM charlie_connections WHERE active AND certificate_expires_at <= now() + interval '7 days'),
  'sessions_by_state', COALESCE((SELECT jsonb_object_agg(state, total) FROM (SELECT state, count(*)::bigint total FROM charlie_sessions GROUP BY state) grouped), '{}'::jsonb),
  'approvals_by_state', COALESCE((SELECT jsonb_object_agg(state, total) FROM (SELECT state, count(*)::bigint total FROM charlie_action_approvals GROUP BY state) grouped), '{}'::jsonb),
  'receipts_by_state', COALESCE((SELECT jsonb_object_agg(state, total) FROM (SELECT state, count(*)::bigint total FROM charlie_action_receipts GROUP BY state) grouped), '{}'::jsonb),
  'ambiguous_receipts', (SELECT count(*)::bigint FROM charlie_action_receipts WHERE state = 'ambiguous'),
  'trigger_events_by_state', COALESCE((SELECT jsonb_object_agg(state, total) FROM (SELECT state, count(*)::bigint total FROM charlie_trigger_events GROUP BY state) grouped), '{}'::jsonb),
  'findings_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM charlie_findings GROUP BY status) grouped), '{}'::jsonb),
  'alert_deliveries_by_status', COALESCE((SELECT jsonb_object_agg(status, total) FROM (SELECT status, count(*)::bigint total FROM charlie_alert_deliveries GROUP BY status) grouped), '{}'::jsonb),
  'enabled_automation_policies', (SELECT count(*)::bigint FROM charlie_automation_policies WHERE enabled),
  'enabled_trigger_rules', (SELECT count(*)::bigint FROM charlie_trigger_rules WHERE enabled)
)`
