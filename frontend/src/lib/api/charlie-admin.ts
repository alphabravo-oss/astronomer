import api from "@/lib/api";

export type CharlieMode = "disabled" | "read_only" | "approval" | "auto";
export type HealthState = "healthy" | "degraded" | "unavailable" | "unknown";

export interface CharlieOnboardingInput {
  package: Record<string, unknown>;
  signingPublicKey: string;
  confirmedSigningKeyId: string;
  confirmedSigningFingerprint: string;
  expectedDeploymentId: string;
  expectedRouteId: string;
}
export interface CharlieOnboardingView {
  packageId: string;
  productId: string;
  productSlug: "astronomer";
  deploymentId: string;
  logicalAgentId: string;
  routeId: string;
  allowedRouteIds: string[];
  schema: "charlie.onboarding/v1";
  centralApiVersion: "charlie/v1";
  centralTrustFingerprint: string;
  signingKeyId: string;
  signingFingerprint: string;
  packageDigest: string;
  artifact: {
    image: string;
    manifestDigest: string;
    chart: string;
    chartDigest: string;
  };
  replicaCount: number;
  issuedAt: string;
  expiresAt: string;
  state: "validated" | "consumed";
  idempotent: boolean;
}
export interface CharlieConnectionView {
  connected: boolean;
  productId?: string;
  productSlug?: string;
  deploymentId?: string;
  routeId?: string;
  centralVersion?: string;
  signingKeyId?: string;
  signingFingerprint?: string;
  packageDigest?: string;
  disclosureDigest?: string;
  disclosureAcknowledged?: boolean;
  updatedAt?: string;
}
export interface CharlieAgentView {
  applicationState: string;
  desiredReplicas: number;
  readyReplicas: number;
  leaderReplica?: string;
  standbyReplicas: string[];
  replicas: Array<{
    ordinal: number;
    instanceId?: string;
    role: "leader" | "standby" | "unknown";
    state: "ready" | "degraded" | "unavailable" | "unknown";
    lastHeartbeatAt?: string;
    version?: string;
  }>;
  fencingEpoch?: number;
  lastHeartbeatAt?: string;
  agentVersion?: string;
  chartVersion?: string;
  chartDigest?: string;
  imageDigest?: string;
}
export interface CharlieModeView {
  requested: CharlieMode;
  authoritative: CharlieMode;
  revision: number;
  emergencyDisabled: boolean;
  disablePending?: boolean;
  disclosureDigest?: string;
  acknowledgedDisclosureDigest?: string;
  effects: string[];
  autoReadiness?: {
    ready: boolean;
    blockers: Array<{
      code: string;
      message: string;
      nextAction: string;
    }>;
  };
}
export interface CharlieTriggerRule {
  id: string;
  name: string;
  enabled: boolean;
  sourceType: string;
  severities: string[];
  scopes: string[];
  cooldownSeconds: number;
  gracePeriodSeconds: number;
  flapWindowSeconds: number;
  flapCount: number;
  fleetThresholdPercent: number;
  minimumAgentVersion?: string;
  suppressed: boolean;
  maximumAttempts: number;
  deadLetterEnabled: boolean;
  serviceIdentity: string;
}
export interface CharlieAutomationView {
  rules: CharlieTriggerRule[];
  actionPolicies: CharlieActionPolicy[];
  defaultsRevision: number;
  serviceIdentityEnabled: boolean;
}
export interface CharlieAlertChannel {
  id: string;
  name: string;
  type: "slack" | "pagerduty" | "msteams" | "webhook" | string;
  enabled: boolean;
  destinationConfigured: boolean;
}
export interface CharlieAlertPolicy {
  enabled: boolean;
  minimumSeverity: "info" | "low" | "medium" | "warning" | "high" | "critical";
  dedupeWindowSeconds: number;
  escalationAfterSeconds: number;
  quietHoursEnabled: boolean;
  quietHoursStart: string;
  quietHoursEnd: string;
  quietHoursTimezone: string;
  revision: number;
  channelIds: string[];
  channels: CharlieAlertChannel[];
  availableChannels: CharlieAlertChannel[];
  inAppEnabled: true;
}
export interface CharlieActionPolicy {
  capability: string;
  effect: string;
  risk: string;
  autoEligible: boolean;
  centralAllowlisted: boolean;
  centralState: "verified" | "unavailable" | string;
  enabled: boolean;
  revision: number;
  maxActionsPerIncident: number;
  maxActionsPerWindow: number;
  budgetWindowSeconds: number;
  cooldownSeconds: number;
  scopeSummary: string;
  preconditions: string[];
  verification: string;
  circuitState: string;
}
export type CharlieActionPolicyInput = Pick<
  CharlieActionPolicy,
  | "capability"
  | "enabled"
  | "maxActionsPerIncident"
  | "maxActionsPerWindow"
  | "budgetWindowSeconds"
  | "cooldownSeconds"
>;
export type CharlieTriggerEventState =
  | "pending"
  | "dispatching"
  | "dispatched"
  | "retry"
  | "dead"
  | "completed"
  | "suppressed";
export interface CharlieTriggerEvent {
  id: string;
  retryOfEventId?: string;
  ruleId: string;
  eventType: string;
  resourceType: string;
  resourceId: string;
  state: CharlieTriggerEventState;
  repeatCount: number;
  attemptCount: number;
  lastErrorCode?: string;
  firstOccurredAt: string;
  lastOccurredAt: string;
  deadLetteredAt?: string;
  updatedAt: string;
}
export interface CharlieAccessView {
  effectivePermissions: Array<{
    permission: string;
    scope: string;
    source: string;
  }>;
  automationGrants: Array<{
    permission: string;
    scope: string;
    source: string;
  }>;
}
export interface CharlieDiagnosticCheck {
  id:
    | "local_config"
    | "product_bridge_mtls"
    | "agent_primary"
    | "agent_standby"
    | "central_via_agent"
    | "leader_epoch"
    | "route_rag"
    | "mcp_tls_discovery"
    | "oci_artifacts"
    | "credential_expiry"
    | string;
  label: string;
  state: HealthState;
  summary: string;
  nextAction?: string;
  checkedAt?: string;
  expiresAt?: string;
}
export interface CharlieDiagnosticsView {
  overall: HealthState;
  checks: CharlieDiagnosticCheck[];
  correlationId?: string;
}
interface CharlieAdminStatusView {
  connection: CharlieConnectionView;
  agent: CharlieAgentView;
  mode: CharlieModeView;
}

function onboardingWire(input: CharlieOnboardingInput) {
  return {
    package: input.package,
    signing_public_key: input.signingPublicKey,
    confirmed_signing_key_id: input.confirmedSigningKeyId,
    confirmed_signing_fingerprint: input.confirmedSigningFingerprint,
    expected_deployment_id: input.expectedDeploymentId,
    expected_route_id: input.expectedRouteId,
  };
}
function payload<T>(data: unknown): T {
  const value = data as { data?: T };
  return (value?.data ?? data) as T;
}

export async function validateCharlieOnboarding(
  input: CharlieOnboardingInput,
): Promise<CharlieOnboardingView> {
  const { data } = await api.post(
    "/admin/charlie/onboarding/validate/",
    onboardingWire(input),
  );
  return payload(data);
}
export async function consumeCharlieOnboarding(
  input: CharlieOnboardingInput,
): Promise<CharlieOnboardingView> {
  const { data } = await api.post(
    "/admin/charlie/onboarding/consume/",
    onboardingWire(input),
  );
  return payload(data);
}
async function getCharlieAdminStatus(): Promise<CharlieAdminStatusView> {
  const { data } = await api.get("/admin/charlie/status/");
  return payload(data);
}
export async function getCharlieConnection(): Promise<CharlieConnectionView> {
  return (await getCharlieAdminStatus()).connection;
}
export async function disconnectCharlie(): Promise<void> {
  await api.post("/admin/charlie/disconnect/", {
    confirmation: "DISCONNECT CHARLIE",
  });
}
export async function uninstallCharlieAgent(): Promise<void> {
  await api.post("/admin/charlie/agent/uninstall/", {
    confirmation: "UNINSTALL CHARLIE",
  });
}
export async function runCharlieAgentAction(
  action: "install" | "upgrade" | "rollback" | "rotate",
): Promise<CharlieAgentView> {
  const { data } = await api.post(`/admin/charlie/agent/${action}/`, {});
  return payload(data);
}
export async function getCharlieAgent(): Promise<CharlieAgentView> {
  return (await getCharlieAdminStatus()).agent;
}
export async function getCharlieMode(): Promise<CharlieModeView> {
  return (await getCharlieAdminStatus()).mode;
}
export async function updateCharlieMode(
  mode: CharlieMode,
  revision: number,
): Promise<CharlieModeView> {
  const { data } = await api.patch("/admin/charlie/mode/", { mode, revision });
  return payload(data);
}
export async function emergencyDisableCharlie(
  revision: number,
): Promise<CharlieModeView> {
  const { data } = await api.patch("/admin/charlie/mode/", {
    mode: "disabled",
    revision,
    emergency_disable: true,
  });
  return payload(data);
}
export async function acknowledgeCharlieDisclosure(
  digest: string,
): Promise<CharlieModeView> {
  const { data } = await api.patch("/admin/charlie/mode/", {
    acknowledge_disclosure_digest: digest,
  });
  return payload(data);
}
export async function getCharlieAutomation(): Promise<CharlieAutomationView> {
  const { data } = await api.get("/admin/charlie/trigger-rules/");
  const value = payload<
    | CharlieAutomationView
    | {
        items: CharlieTriggerRule[];
        defaultsRevision?: number;
        serviceIdentityEnabled?: boolean;
    }
  >(data);
  if (value && !Array.isArray(value) && 'rules' in value && Array.isArray(value.rules)) {
    return { ...value, actionPolicies: value.actionPolicies ?? [] };
  }
  if (value && !Array.isArray(value) && 'items' in value && Array.isArray(value.items)) {
    return {
      rules: value.items,
      actionPolicies: [],
      defaultsRevision: value.defaultsRevision ?? 0,
      serviceIdentityEnabled: value.serviceIdentityEnabled ?? false,
    };
  }
  throw new Error('Charlie trigger-rule response is unavailable');
}
export async function getCharlieAlertPolicy(): Promise<CharlieAlertPolicy> {
  const { data } = await api.get("/admin/charlie/alert-policy/");
  return payload(data);
}
export async function updateCharlieAlertPolicy(
  input: CharlieAlertPolicy,
): Promise<CharlieAlertPolicy> {
  try {
    const { data } = await api.put("/admin/charlie/alert-policy/", {
      revision: input.revision,
      enabled: input.enabled,
      minimum_severity: input.minimumSeverity,
      dedupe_window_seconds: input.dedupeWindowSeconds,
      escalation_after_seconds: input.escalationAfterSeconds,
      quiet_hours_enabled: input.quietHoursEnabled,
      quiet_hours_start: input.quietHoursStart,
      quiet_hours_end: input.quietHoursEnd,
      quiet_hours_timezone: input.quietHoursTimezone,
      channel_ids: input.channelIds,
    });
    return payload(data);
  } catch (error) {
    const status = (error as { response?: { status?: number } }).response?.status;
    if (status === 409) {
      throw new Error("This alert policy changed. Refresh before trying again.");
    }
    throw new Error("Astronomer could not confirm the alert-policy update.");
  }
}
export async function updateCharlieAutomation(
  input: CharlieAutomationView,
): Promise<CharlieAutomationView> {
  await Promise.all(
    input.rules.map((rule) =>
      (rule.id ? api.patch : api.post)(
        rule.id
          ? `/admin/charlie/trigger-rules/${encodeURIComponent(rule.id)}/`
          : "/admin/charlie/trigger-rules/",
        triggerRuleWire(rule),
      ),
    ),
  );
  await api.put("/admin/charlie/access/", {
    automation_service_identity_enabled: input.serviceIdentityEnabled,
  });
  return getCharlieAutomation();
}

export async function updateCharlieActionPolicy(
  input: CharlieActionPolicyInput,
): Promise<CharlieActionPolicy> {
  try {
    const { data } = await api.put(
      `/admin/charlie/action-policies/${encodeURIComponent(input.capability)}/`,
      {
        enabled: input.enabled,
        max_actions_per_incident: input.maxActionsPerIncident,
        max_actions_per_window: input.maxActionsPerWindow,
        budget_window_seconds: input.budgetWindowSeconds,
        cooldown_seconds: input.cooldownSeconds,
      },
    );
    return payload(data);
  } catch (error) {
    const status = (error as { response?: { status?: number } }).response?.status;
    if (status === 409) {
      throw new Error(
        "This action policy conflicts with current central allowlisting or bounded budget rules. Refresh before trying again.",
      );
    }
    if (status === 503) {
      throw new Error(
        "Charlie central verification is unavailable. The policy remains fail-closed.",
      );
    }
    throw new Error("Astronomer could not confirm the action-policy update.");
  }
}

function triggerRuleWire(rule: CharlieTriggerRule) {
  return {
    ...(rule.id ? { id: rule.id } : {}),
    name: rule.name,
    enabled: rule.enabled,
    source_type: rule.sourceType,
    severities: rule.severities,
    scopes: rule.scopes,
    cooldown_seconds: rule.cooldownSeconds,
    grace_period_seconds: rule.gracePeriodSeconds,
    flap_window_seconds: rule.flapWindowSeconds,
    flap_count: rule.flapCount,
    fleet_threshold_percent: rule.fleetThresholdPercent,
    minimum_agent_version: rule.minimumAgentVersion,
    suppressed: rule.suppressed,
    maximum_attempts: rule.maximumAttempts,
    dead_letter_enabled: rule.deadLetterEnabled,
    service_identity: rule.serviceIdentity,
  };
}

export async function deleteCharlieAutomationRule(id: string): Promise<void> {
  if (!id) return;
  await api.delete(
    `/admin/charlie/trigger-rules/${encodeURIComponent(id)}/`,
    { data: { confirmation: "DELETE TRIGGER" } },
  );
}
export async function listCharlieTriggerEvents(
  state: CharlieTriggerEventState = "dead",
  offset = 0,
  limit = 20,
): Promise<CharlieTriggerEvent[]> {
  const { data } = await api.get("/admin/charlie/trigger-events/", {
    params: {
      state,
      offset: Math.max(0, Math.trunc(offset)),
      limit: Math.min(100, Math.max(1, Math.trunc(limit))),
    },
  });
  const value = payload<{ items: CharlieTriggerEvent[] }>(data);
  return Array.isArray(value?.items) ? value.items : [];
}
export async function retryCharlieTriggerEvent(
  eventId: string,
): Promise<CharlieTriggerEvent> {
  const { data } = await api.post(
    `/admin/charlie/trigger-events/${encodeURIComponent(eventId)}/retry/`,
    { request_id: crypto.randomUUID() },
  );
  return payload<{ event: CharlieTriggerEvent }>(data).event;
}
export async function getCharlieAccess(): Promise<CharlieAccessView> {
  const { data } = await api.get("/admin/charlie/access/");
  return payload(data);
}
export async function getCharlieDiagnostics(): Promise<CharlieDiagnosticsView> {
  const { data } = await api.post("/admin/charlie/diagnostics/run/", {});
  return payload(data);
}
