import type {
  CharlieAccessView,
  CharlieActionPolicy,
  CharlieAdminStatusView,
  CharlieAgentView,
  CharlieAlertPolicy,
  CharlieAutomationView,
  CharlieConnectionView,
  CharlieDiagnosticsView,
  CharlieKubernetesVisibilityView,
  CharlieModeView,
  CharlieOnboardingView,
  CharlieTriggerEvent,
  CharlieTriggerRule,
} from "@/lib/api/charlie-admin";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Schemas = OpenAPIComponents["schemas"];

export function mapCharlieOnboarding(
  wire: Schemas["CharlieOnboardingStatus"],
): CharlieOnboardingView {
  return {
    packageId: wire.package_id,
    productId: wire.product_id,
    productSlug: wire.product_slug,
    deploymentId: wire.deployment_id,
    logicalAgentId: wire.logical_agent_id,
    routeId: wire.route_id,
    allowedRouteIds: wire.allowed_route_ids,
    schema: wire.schema,
    centralApiVersion: wire.central_api_version,
    centralTrustFingerprint: wire.central_trust_fingerprint,
    signingKeyId: wire.signing_key_id,
    signingFingerprint: wire.signing_fingerprint,
    packageDigest: wire.package_digest,
    artifact: {
      image: wire.artifact.image,
      manifestDigest: wire.artifact.manifest_digest,
      chart: wire.artifact.chart,
      chartDigest: wire.artifact.chart_digest,
    },
    replicaCount: wire.replica_count,
    issuedAt: wire.issued_at,
    expiresAt: wire.expires_at,
    state: wire.state,
    idempotent: wire.idempotent,
  };
}

export function mapCharlieConnection(
  wire: Schemas["CharlieAdminConnection"],
): CharlieConnectionView {
  return {
    connected: wire.connected,
    endpoint: wire.endpoint,
    productId: wire.product_id,
    productSlug: wire.product_slug,
    deploymentId: wire.deployment_id,
    routeId: wire.route_id,
    centralVersion: wire.central_version,
    signingKeyId: wire.signing_key_id,
    signingFingerprint: wire.signing_fingerprint,
    packageDigest: wire.package_digest,
    disclosureDigest: wire.disclosure_digest,
    disclosureAcknowledged: wire.disclosure_acknowledged,
    updatedAt: wire.updated_at,
  };
}

export function mapCharlieAgent(
  wire: Schemas["CharlieAdminAgent"],
): CharlieAgentView {
  return {
    applicationState: wire.application_state,
    desiredReplicas: wire.desired_replicas,
    readyReplicas: wire.ready_replicas,
    leaderReplica: wire.leader_replica,
    standbyReplicas: wire.standby_replicas,
    replicas: wire.replicas.map((replica) => ({
      ordinal: replica.ordinal,
      instanceId: replica.instance_id,
      role: replica.role,
      state: replica.state,
      lastHeartbeatAt: replica.last_heartbeat_at,
      version: replica.version,
    })),
    fencingEpoch: wire.fencing_epoch,
    lastHeartbeatAt: wire.last_heartbeat_at,
    agentVersion: wire.agent_version,
    chartVersion: wire.chart_version,
    chartDigest: wire.chart_digest,
    imageDigest: wire.image_digest,
  };
}

export function mapCharlieMode(
  wire: Schemas["CharlieAdminMode"],
): CharlieModeView {
  return {
    requested: wire.requested,
    authoritative: wire.authoritative,
    revision: wire.revision,
    emergencyDisabled: wire.emergency_disabled,
    disablePending: wire.disable_pending,
    disclosureDigest: wire.disclosure_digest,
    acknowledgedDisclosureDigest: wire.acknowledged_disclosure_digest,
    effects: wire.effects,
    workloadCeiling: wire.workload_ceiling,
    workloadCeilingReady: wire.workload_ceiling_ready,
    autoReadiness: {
      ready: wire.auto_readiness.ready,
      blockers: wire.auto_readiness.blockers.map((blocker) => ({
        code: blocker.code,
        message: blocker.message,
        nextAction: blocker.next_action,
      })),
    },
  };
}

export function mapCharlieAdminStatus(
  wire: Schemas["CharlieAdminStatus"],
): CharlieAdminStatusView {
  return {
    connection: mapCharlieConnection(wire.connection),
    agent: mapCharlieAgent(wire.agent),
    mode: mapCharlieMode(wire.mode),
  };
}

export function mapCharlieKubernetesVisibility(
  wire: Schemas["CharlieKubernetesVisibility"],
): CharlieKubernetesVisibilityView {
  return {
    schema: wire.schema,
    profile: wire.profile,
    revision: wire.revision,
    state: wire.state,
    instanceId: wire.instance_id,
    namespaces: wire.namespaces,
    productOwnedOnly: wire.product_owned_only,
    clusterScoped: wire.cluster_scoped,
    podLogs: wire.pod_logs,
    downstreamTargets: wire.downstream_targets,
    secretValues: wire.secret_values,
    exec: wire.exec,
    attach: wire.attach,
    portForward: wire.port_forward,
    apiProxy: wire.api_proxy,
    requiresRediscovery: wire.requires_rediscovery,
    requiresCentralReview: wire.requires_central_review,
    requiresProductAcknowledgement: wire.requires_product_acknowledgement,
    candidateDisclosureDigest: wire.candidate_disclosure_digest,
    availableProfiles: wire.available_profiles,
    scopeSummary: wire.scope_summary,
  };
}

export function mapCharlieTriggerRule(
  wire: Schemas["CharlieAdminTriggerRule"],
): CharlieTriggerRule {
  return {
    id: wire.id ?? "",
    name: wire.name,
    enabled: wire.enabled,
    sourceType: wire.source_type,
    severities: wire.severities ?? [],
    scopes: wire.scopes ?? [],
    cooldownSeconds: wire.cooldown_seconds,
    gracePeriodSeconds: wire.grace_period_seconds,
    flapWindowSeconds: wire.flap_window_seconds,
    flapCount: wire.flap_count,
    estateThresholdPercent:
      wire.estate_threshold_percent ?? wire.fleet_threshold_percent,
    minimumAgentVersion: wire.minimum_agent_version,
    suppressed: wire.suppressed,
    maximumAttempts: wire.maximum_attempts,
    deadLetterEnabled: wire.dead_letter_enabled,
    serviceIdentity: wire.service_identity ?? "",
    modeCeiling: wire.mode_ceiling,
  };
}

export function mapCharlieActionPolicy(
  wire: Schemas["CharlieAdminActionPolicy"],
): CharlieActionPolicy {
  return {
    capability: wire.capability,
    effect: wire.effect,
    risk: wire.risk,
    autoEligible: wire.auto_eligible,
    centralAllowlisted: wire.central_allowlisted,
    centralState: wire.central_state,
    enabled: wire.enabled,
    revision: wire.revision,
    maxActionsPerIncident: wire.max_actions_per_incident,
    maxActionsPerWindow: wire.max_actions_per_window,
    budgetWindowSeconds: wire.budget_window_seconds,
    cooldownSeconds: wire.cooldown_seconds,
    scopeSummary: wire.scope_summary,
    preconditions: wire.preconditions,
    verification: wire.verification,
    circuitState: wire.circuit_state,
  };
}

export function mapCharlieAutomation(
  wire: Schemas["CharlieAdminAutomation"],
): CharlieAutomationView {
  return {
    rules: wire.rules.map(mapCharlieTriggerRule),
    actionPolicies: wire.action_policies.map(mapCharlieActionPolicy),
    defaultsRevision: wire.defaults_revision,
    serviceIdentityEnabled: wire.service_identity_enabled,
  };
}

function mapCharlieAlertChannel(
  wire: Schemas["CharlieAdminAlertChannel"],
) {
  return {
    id: wire.id,
    name: wire.name,
    type: wire.type,
    enabled: wire.enabled,
    destinationConfigured: wire.destination_configured,
  };
}

export function mapCharlieAlertPolicy(
  wire: Schemas["CharlieAdminAlertPolicy"],
): CharlieAlertPolicy {
  return {
    enabled: wire.enabled,
    minimumSeverity: wire.minimum_severity,
    dedupeWindowSeconds: wire.dedupe_window_seconds,
    escalationAfterSeconds: wire.escalation_after_seconds,
    quietHoursEnabled: wire.quiet_hours_enabled,
    quietHoursStart: wire.quiet_hours_start,
    quietHoursEnd: wire.quiet_hours_end,
    quietHoursTimezone: wire.quiet_hours_timezone,
    revision: wire.revision,
    channelIds: wire.channel_ids,
    channels: wire.channels.map(mapCharlieAlertChannel),
    availableChannels: wire.available_channels.map(mapCharlieAlertChannel),
    inAppEnabled: true,
  };
}

export function mapCharlieTriggerEvent(
  wire: Schemas["CharlieAdminTriggerEvent"],
): CharlieTriggerEvent {
  return {
    id: wire.id,
    retryOfEventId: wire.retry_of_event_id,
    ruleId: wire.rule_id,
    eventType: wire.event_type,
    resourceType: wire.resource_type,
    resourceId: wire.resource_id,
    state: wire.state,
    repeatCount: wire.repeat_count,
    attemptCount: wire.attempt_count,
    lastErrorCode: wire.last_error_code,
    firstOccurredAt: wire.first_occurred_at,
    lastOccurredAt: wire.last_occurred_at,
    deadLetteredAt: wire.dead_lettered_at,
    updatedAt: wire.updated_at,
  };
}

export function mapCharlieAccess(
  wire: Schemas["CharlieAdminAccess"],
): CharlieAccessView {
  const mapPermission = (permission: Schemas["CharlieAdminPermission"]) => ({
    permission: permission.permission,
    scope: permission.scope,
    source: permission.source,
  });
  return {
    effectivePermissions: wire.effective_permissions.map(mapPermission),
    automationGrants: wire.automation_grants.map(mapPermission),
  };
}

export function mapCharlieDiagnostics(
  wire: Schemas["CharlieAdminDiagnostics"],
): CharlieDiagnosticsView {
  return {
    overall: wire.overall,
    checks: wire.checks.map((check) => ({
      id: check.id,
      label: check.label,
      state: check.state,
      summary: check.summary,
      nextAction: check.next_action,
      checkedAt: check.checked_at,
      expiresAt: check.expires_at,
    })),
    correlationId: wire.correlation_id,
  };
}
