import {
  adminCharlieAccessGet,
  adminCharlieAccessUpdate,
  adminCharlieActionPolicyUpdate,
  adminCharlieAlertPolicyGet,
  adminCharlieAlertPolicyUpdate,
  adminCharlieDiagnosticsRun,
  adminCharlieDisconnect,
  adminCharlieKubernetesVisibilityGet,
  adminCharlieKubernetesVisibilityUpdate,
  adminCharlieModeUpdate,
  adminCharlieOnboardingConsume,
  adminCharlieOnboardingValidate,
  adminCharlieStatus,
  adminCharlieTriggerEventRetry,
  adminCharlieTriggerEventsList,
  adminCharlieTriggerRuleCreate,
  adminCharlieTriggerRuleDelete,
  adminCharlieTriggerRulesList,
  adminCharlieTriggerRuleUpdate,
  charlieActivation,
} from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";
import {
  mapCharlieAccess,
  mapCharlieActionPolicy,
  mapCharlieAdminStatus,
  mapCharlieAlertPolicy,
  mapCharlieAutomation,
  mapCharlieConnection,
  mapCharlieDiagnostics,
  mapCharlieKubernetesVisibility,
  mapCharlieMode,
  mapCharlieOnboarding,
  mapCharlieTriggerEvent,
} from "@/lib/api/charlie-admin-mappers";

export type CharlieMode = "disabled" | "read_only" | "approval" | "auto";
export type HealthState =
  "healthy" | "degraded" | "unavailable" | "inactive" | "ready" | "unknown";

export interface CharlieOnboardingInput {
  package: Record<string, unknown>;
  signingPublicKey: string;
  confirmedSigningKeyId: string;
  confirmedSigningFingerprint: string;
  expectedDeploymentId: string;
  expectedRouteId: string;
}
export interface CharlieConnectInput {
  endpoint: string;
  connectToken: string;
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
  endpoint?: string;
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
  workloadCeiling: CharlieMode;
  workloadCeilingReady: boolean;
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
  estateThresholdPercent: number;
  minimumAgentVersion?: string;
  suppressed: boolean;
  maximumAttempts: number;
  deadLetterEnabled: boolean;
  serviceIdentity: string;
  modeCeiling: "read_only" | "approval" | "auto";
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
export type CharlieTriggerRetryReceipt = CamelizeKeys<
  OpenAPIComponents["schemas"]["CharlieTriggerRetryReceipt"]
>;
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
export type CharlieKubernetesVisibilityProfile =
  "disabled" | "product_namespace" | "cluster_diagnostics";
export interface CharlieKubernetesVisibilityView {
  schema: "charlie.kubernetes-visibility/v1";
  profile: CharlieKubernetesVisibilityProfile;
  revision: number;
  state: "not_configured" | "disabled" | "enabled";
  instanceId: string;
  namespaces: string[];
  productOwnedOnly: true;
  clusterScoped: boolean;
  podLogs: boolean;
  downstreamTargets: false;
  secretValues: false;
  exec: false;
  attach: false;
  portForward: false;
  apiProxy: false;
  requiresRediscovery: boolean;
  requiresCentralReview: boolean;
  requiresProductAcknowledgement: boolean;
  candidateDisclosureDigest?: string;
  availableProfiles: CharlieKubernetesVisibilityProfile[];
  scopeSummary: string;
}
export interface CharlieAdminStatusView {
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
function connectWire(input: CharlieConnectInput) {
  return {
    endpoint: input.endpoint.trim(),
    connect_token: input.connectToken.replace(/\s+/g, ""),
  };
}
export async function validateCharlieOnboarding(
  input: CharlieOnboardingInput,
  signal?: AbortSignal,
): Promise<CharlieOnboardingView> {
  return mapCharlieOnboarding(
    await adminCharlieOnboardingValidate({ body: onboardingWire(input), signal }),
  );
}
export async function consumeCharlieOnboarding(
  input: CharlieOnboardingInput,
  signal?: AbortSignal,
): Promise<CharlieOnboardingView> {
  return mapCharlieOnboarding(
    await adminCharlieOnboardingConsume({ body: onboardingWire(input), signal }),
  );
}
export async function validateCharlieConnect(
  input: CharlieConnectInput,
  signal?: AbortSignal,
): Promise<CharlieOnboardingView> {
  return mapCharlieOnboarding(
    await adminCharlieOnboardingValidate({ body: connectWire(input), signal }),
  );
}
export async function consumeCharlieConnect(
  input: CharlieConnectInput,
  signal?: AbortSignal,
): Promise<CharlieOnboardingView> {
  return mapCharlieOnboarding(
    await adminCharlieOnboardingConsume({ body: connectWire(input), signal }),
  );
}
async function getCharlieAdminStatus(
  signal?: AbortSignal,
): Promise<CharlieAdminStatusView> {
  return mapCharlieAdminStatus(await adminCharlieStatus({ signal }));
}
export async function getCharlieActivation(signal?: AbortSignal): Promise<{
  activated: boolean;
  endpoint?: string;
}> {
  const value = await charlieActivation({ signal });
  const endpoint = value.endpoint?.trim();
  return {
    activated: value.activated === true,
    endpoint: endpoint || undefined,
  };
}
export async function getCharlieConnection(
  signal?: AbortSignal,
): Promise<CharlieConnectionView> {
  return (await getCharlieAdminStatus(signal)).connection;
}
export async function getCharlieAgent(
  signal?: AbortSignal,
): Promise<CharlieAgentView> {
  return (await getCharlieAdminStatus(signal)).agent;
}
export async function getCharlieMode(
  signal?: AbortSignal,
): Promise<CharlieModeView> {
  return (await getCharlieAdminStatus(signal)).mode;
}
export async function getCharlieKubernetesVisibility(
  signal?: AbortSignal,
): Promise<CharlieKubernetesVisibilityView> {
  return mapCharlieKubernetesVisibility(
    await adminCharlieKubernetesVisibilityGet({ signal }),
  );
}
export async function updateCharlieKubernetesVisibility(
  input: {
    profile: CharlieKubernetesVisibilityProfile;
    podLogs: boolean;
    revision: number;
  },
  signal?: AbortSignal,
): Promise<CharlieKubernetesVisibilityView> {
  return mapCharlieKubernetesVisibility(
    await adminCharlieKubernetesVisibilityUpdate({
      body: {
        profile: input.profile,
        pod_logs: input.podLogs,
        revision: input.revision,
      },
      signal,
    }),
  );
}
export async function updateCharlieMode(
  mode: CharlieMode,
  revision: number,
  signal?: AbortSignal,
): Promise<CharlieModeView> {
  return mapCharlieMode(
    await adminCharlieModeUpdate({
      body: { mode, revision },
      signal,
      timeoutMs: 180_000,
    }),
  );
}
export async function disconnectCharlie(
  signal?: AbortSignal,
): Promise<CharlieConnectionView> {
  const status = await adminCharlieDisconnect({
    body: { confirmation: "DISCONNECT CHARLIE" },
    signal,
  });
  return mapCharlieConnection(status.connection);
}
export async function emergencyDisableCharlie(
  revision: number,
  signal?: AbortSignal,
): Promise<CharlieModeView> {
  return mapCharlieMode(
    await adminCharlieModeUpdate({
      body: { mode: "disabled", revision, emergency_disable: true },
      signal,
      timeoutMs: 180_000,
    }),
  );
}
export async function acknowledgeCharlieDisclosure(
  digest: string,
  signal?: AbortSignal,
): Promise<CharlieModeView> {
  return mapCharlieMode(
    await adminCharlieModeUpdate({
      body: { acknowledge_disclosure_digest: digest },
      signal,
    }),
  );
}
export async function getCharlieAutomation(
  signal?: AbortSignal,
): Promise<CharlieAutomationView> {
  return mapCharlieAutomation(await adminCharlieTriggerRulesList({ signal }));
}
export async function getCharlieAlertPolicy(
  signal?: AbortSignal,
): Promise<CharlieAlertPolicy> {
  return mapCharlieAlertPolicy(await adminCharlieAlertPolicyGet({ signal }));
}
export async function updateCharlieAlertPolicy(
  input: CharlieAlertPolicy,
  signal?: AbortSignal,
): Promise<CharlieAlertPolicy> {
  try {
    return mapCharlieAlertPolicy(
      await adminCharlieAlertPolicyUpdate({
        body: {
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
        },
        signal,
      }),
    );
  } catch (error) {
    const status = (error as { response?: { status?: number } }).response
      ?.status;
    if (status === 409) {
      throw new Error(
        "This alert policy changed. Refresh before trying again.",
      );
    }
    throw new Error("Astronomer could not confirm the alert-policy update.");
  }
}
export async function updateCharlieAutomation(
  input: CharlieAutomationView,
  signal?: AbortSignal,
): Promise<CharlieAutomationView> {
  await Promise.all(
    input.rules.map((rule) => {
      const body = triggerRuleWire(rule);
      return rule.id
        ? adminCharlieTriggerRuleUpdate({
            path: { rule_id: rule.id },
            body,
            signal,
          })
        : adminCharlieTriggerRuleCreate({ body, signal });
    }),
  );
  await adminCharlieAccessUpdate({
    body: {
      automation_service_identity_enabled: input.serviceIdentityEnabled,
    },
    signal,
  });
  return getCharlieAutomation(signal);
}

export async function updateCharlieActionPolicy(
  input: CharlieActionPolicyInput,
  signal?: AbortSignal,
): Promise<CharlieActionPolicy> {
  try {
    return mapCharlieActionPolicy(
      await adminCharlieActionPolicyUpdate({
        path: { capability: input.capability },
        body: {
          enabled: input.enabled,
          max_actions_per_incident: input.maxActionsPerIncident,
          max_actions_per_window: input.maxActionsPerWindow,
          budget_window_seconds: input.budgetWindowSeconds,
          cooldown_seconds: input.cooldownSeconds,
        },
        signal,
      }),
    );
  } catch (error) {
    const status = (error as { response?: { status?: number } }).response
      ?.status;
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
    severities: rule.severities.map(triggerSeverity),
    scopes: rule.scopes,
    cooldown_seconds: rule.cooldownSeconds,
    grace_period_seconds: rule.gracePeriodSeconds,
    flap_window_seconds: rule.flapWindowSeconds,
    flap_count: rule.flapCount,
    estate_threshold_percent: rule.estateThresholdPercent,
    fleet_threshold_percent: rule.estateThresholdPercent,
    minimum_agent_version: rule.minimumAgentVersion,
    suppressed: rule.suppressed,
    maximum_attempts: rule.maximumAttempts,
    dead_letter_enabled: rule.deadLetterEnabled,
    service_identity: rule.serviceIdentity,
    mode_ceiling: rule.modeCeiling,
  };
}

function triggerSeverity(
  value: string,
): "info" | "low" | "medium" | "high" | "critical" {
  switch (value) {
    case "info":
    case "low":
    case "medium":
    case "high":
    case "critical":
      return value;
    default:
      throw new Error(`Unsupported Charlie trigger severity: ${value}`);
  }
}

export async function deleteCharlieAutomationRule(
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  if (!id) return;
  await adminCharlieTriggerRuleDelete({
    path: { rule_id: id },
    body: { confirmation: "DELETE TRIGGER" },
    signal,
  });
}
export async function listCharlieTriggerEvents(
  state: CharlieTriggerEventState = "dead",
  offset = 0,
  limit = 20,
  signal?: AbortSignal,
): Promise<CharlieTriggerEvent[]> {
  const response = await adminCharlieTriggerEventsList({
    query: {
      state,
      offset: Math.max(0, Math.trunc(offset)),
      limit: Math.min(100, Math.max(1, Math.trunc(limit))),
    },
    signal,
  });
  return response.items.map(mapCharlieTriggerEvent);
}
export async function retryCharlieTriggerEvent(
  eventId: string,
  signal?: AbortSignal,
): Promise<CharlieTriggerRetryReceipt> {
  const response = await adminCharlieTriggerEventRetry({
    path: { event_id: eventId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body: { request_id: createIdempotencyKey() },
    signal,
  });
  const receipt = response.data;
  if (!receipt) throw new Error("Charlie trigger retry omitted its operation receipt");
  return {
    operationId: receipt.operation_id,
    eventId: receipt.event_id,
    status: receipt.status,
    statusUrl: receipt.status_url,
  };
}
export async function getCharlieAccess(
  signal?: AbortSignal,
): Promise<CharlieAccessView> {
  return mapCharlieAccess(await adminCharlieAccessGet({ signal }));
}
export async function getCharlieDiagnostics(
  signal?: AbortSignal,
): Promise<CharlieDiagnosticsView> {
  return mapCharlieDiagnostics(await adminCharlieDiagnosticsRun({ signal }));
}
