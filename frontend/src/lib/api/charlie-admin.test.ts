import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generated from "@/lib/api/generated/client";

vi.mock("@/lib/api/generated/client", () => ({
  adminCharlieAccessGet: vi.fn(),
  adminCharlieAccessUpdate: vi.fn(),
  adminCharlieActionPolicyUpdate: vi.fn(),
  adminCharlieAlertPolicyGet: vi.fn(),
  adminCharlieAlertPolicyUpdate: vi.fn(),
  adminCharlieDiagnosticsRun: vi.fn(),
  adminCharlieDisconnect: vi.fn(),
  adminCharlieKubernetesVisibilityGet: vi.fn(),
  adminCharlieKubernetesVisibilityUpdate: vi.fn(),
  adminCharlieModeUpdate: vi.fn(),
  adminCharlieOnboardingConsume: vi.fn(),
  adminCharlieOnboardingValidate: vi.fn(),
  adminCharlieStatus: vi.fn(),
  adminCharlieTriggerEventRetry: vi.fn(),
  adminCharlieTriggerEventsList: vi.fn(),
  adminCharlieTriggerRuleCreate: vi.fn(),
  adminCharlieTriggerRuleDelete: vi.fn(),
  adminCharlieTriggerRulesList: vi.fn(),
  adminCharlieTriggerRuleUpdate: vi.fn(),
  charlieActivation: vi.fn(),
}));

import {
  disconnectCharlie,
  listCharlieTriggerEvents,
  retryCharlieTriggerEvent,
  updateCharlieActionPolicy,
  updateCharlieAlertPolicy,
  updateCharlieAutomation,
  updateCharlieMode,
  validateCharlieConnect,
  validateCharlieOnboarding,
} from "./charlie-admin";

const onboardingWire = {
  package_id: "p",
  product_id: "product",
  product_slug: "astronomer" as const,
  deployment_id: "d",
  logical_agent_id: "agent",
  integration_id: "integration",
  mcp_url: "https://charlie.example.test/mcp",
  route_id: "r",
  allowed_route_ids: ["r"],
  schema: "charlie.onboarding/v1" as const,
  central_api_version: "charlie/v1" as const,
  central_trust_fingerprint: "central-fingerprint",
  signing_key_id: "kid",
  signing_fingerprint: "signing-fingerprint",
  package_digest: "sha256:package",
  artifact: {
    image: "registry.example.test/charlie@sha256:image",
    manifest_digest: "sha256:manifest",
    chart: "oci://registry.example.test/charlie",
    chart_digest: "sha256:chart",
  },
  replica_count: 2,
  issued_at: "2026-08-24T00:00:00Z",
  expires_at: "2026-08-25T00:00:00Z",
  state: "validated" as const,
  idempotent: false,
};

const modeWire = {
  requested: "read_only" as const,
  authoritative: "read_only" as const,
  revision: 4,
  emergency_disabled: false,
  effects: [],
  workload_ceiling: "read_only" as const,
  workload_ceiling_ready: true,
  auto_readiness: { ready: true, blockers: [] },
};

const automationWire = {
  rules: [],
  action_policies: [],
  defaults_revision: 3,
  service_identity_enabled: true,
};

const alertPolicyWire = {
  enabled: true,
  minimum_severity: "high" as const,
  dedupe_window_seconds: 900,
  escalation_after_seconds: 3600,
  quiet_hours_enabled: true,
  quiet_hours_start: "22:00",
  quiet_hours_end: "07:00",
  quiet_hours_timezone: "UTC",
  revision: 2,
  channel_ids: ["channel-a"],
  channels: [],
  available_channels: [],
  in_app_enabled: true,
};

const actionPolicyWire = {
  capability: "astronomer.queue.retry_task",
  effect: "write" as const,
  risk: "low" as const,
  auto_eligible: true,
  central_allowlisted: true,
  central_state: "verified" as const,
  enabled: true,
  revision: 2,
  max_actions_per_incident: 1,
  max_actions_per_window: 3,
  budget_window_seconds: 3600,
  cooldown_seconds: 60,
  scope_summary: "queue tasks",
  preconditions: [],
  verification: "task state",
  circuit_state: "closed" as const,
};

describe("Charlie admin generated-operation boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("normalizes connect credentials and maps raw wire casing", async () => {
    vi.mocked(generated.adminCharlieOnboardingValidate).mockResolvedValue(
      onboardingWire,
    );

    const result = await validateCharlieConnect({
      endpoint: " https://charlie.example.test/charlie/v1/ ",
      connectToken: "charlie.connect.v1.abc\n",
    });

    expect(result).toEqual(expect.objectContaining({ packageId: "p", routeId: "r" }));
    expect(generated.adminCharlieOnboardingValidate).toHaveBeenCalledWith({
      body: {
        endpoint: "https://charlie.example.test/charlie/v1/",
        connect_token: "charlie.connect.v1.abc",
      },
      signal: undefined,
    });
  });

  it("forwards cancellation and the exact onboarding snake-case contract", async () => {
    vi.mocked(generated.adminCharlieOnboardingValidate).mockResolvedValue(
      onboardingWire,
    );
    const controller = new AbortController();

    await validateCharlieOnboarding(
      {
        package: {
          version: "charlie.onboarding/v1",
          enrollment_credential: "write-only",
        },
        signingPublicKey: "public",
        confirmedSigningKeyId: "kid",
        confirmedSigningFingerprint: "f".repeat(64),
        expectedDeploymentId: "d",
        expectedRouteId: "r",
      },
      controller.signal,
    );

    expect(generated.adminCharlieOnboardingValidate).toHaveBeenCalledWith({
      body: expect.objectContaining({
        signing_public_key: "public",
        confirmed_signing_key_id: "kid",
        confirmed_signing_fingerprint: "f".repeat(64),
        expected_deployment_id: "d",
        expected_route_id: "r",
      }),
      signal: controller.signal,
    });
  });

  it("preserves the exact disconnect confirmation phrase", async () => {
    vi.mocked(generated.adminCharlieDisconnect).mockResolvedValue({
      connection: { connected: false, disclosure_acknowledged: false },
      agent: {
        application_state: "inactive",
        desired_replicas: 0,
        ready_replicas: 0,
        standby_replicas: [],
        replicas: [],
      },
      mode: modeWire,
    });

    await expect(disconnectCharlie()).resolves.toEqual({
      connected: false,
      endpoint: undefined,
      productId: undefined,
      productSlug: undefined,
      deploymentId: undefined,
      routeId: undefined,
      centralVersion: undefined,
      signingKeyId: undefined,
      signingFingerprint: undefined,
      packageDigest: undefined,
      disclosureDigest: undefined,
      disclosureAcknowledged: false,
      updatedAt: undefined,
    });
    expect(generated.adminCharlieDisconnect).toHaveBeenCalledWith({
      body: { confirmation: "DISCONNECT CHARLIE" },
      signal: undefined,
    });
  });

  it("preserves cancellation and the long-running mode timeout", async () => {
    vi.mocked(generated.adminCharlieModeUpdate).mockResolvedValue(modeWire);
    const controller = new AbortController();

    await updateCharlieMode("read_only", 3, controller.signal);

    expect(generated.adminCharlieModeUpdate).toHaveBeenCalledWith({
      body: { mode: "read_only", revision: 3 },
      signal: controller.signal,
      timeoutMs: 180_000,
    });
  });

  it("serializes every trigger policy field without hidden client defaults", async () => {
    vi.mocked(generated.adminCharlieTriggerRuleUpdate).mockResolvedValue({
      id: "r",
      name: "Agent flap",
      source_type: "agent",
      enabled: true,
      severities: ["high"],
      scopes: ["prod"],
      cooldown_seconds: 1800,
      grace_period_seconds: 300,
      flap_window_seconds: 900,
      flap_count: 3,
      estate_threshold_percent: 25,
      fleet_threshold_percent: 25,
      suppressed: false,
      maximum_attempts: 5,
      dead_letter_enabled: true,
      service_identity: "system:charlie-automation",
      mode_ceiling: "auto",
    });
    vi.mocked(generated.adminCharlieAccessUpdate).mockResolvedValue({
      effective_permissions: [],
      automation_grants: [],
    });
    vi.mocked(generated.adminCharlieTriggerRulesList).mockResolvedValue(
      automationWire,
    );

    await updateCharlieAutomation({
      defaultsRevision: 3,
      serviceIdentityEnabled: true,
      actionPolicies: [],
      rules: [
        {
          id: "r",
          name: "Agent flap",
          enabled: true,
          sourceType: "agent",
          severities: ["high"],
          scopes: ["prod"],
          cooldownSeconds: 1800,
          gracePeriodSeconds: 300,
          flapWindowSeconds: 900,
          flapCount: 3,
          estateThresholdPercent: 25,
          minimumAgentVersion: "1.2.3",
          suppressed: false,
          maximumAttempts: 5,
          deadLetterEnabled: true,
          serviceIdentity: "system:charlie-automation",
          modeCeiling: "auto",
        },
      ],
    });

    expect(generated.adminCharlieTriggerRuleUpdate).toHaveBeenCalledWith({
      path: { rule_id: "r" },
      body: expect.objectContaining({
        cooldown_seconds: 1800,
        grace_period_seconds: 300,
        flap_window_seconds: 900,
        flap_count: 3,
        estate_threshold_percent: 25,
        maximum_attempts: 5,
        dead_letter_enabled: true,
        mode_ceiling: "auto",
      }),
      signal: undefined,
    });
    expect(generated.adminCharlieAccessUpdate).toHaveBeenCalledWith({
      body: { automation_service_identity_enabled: true },
      signal: undefined,
    });
  });

  it("bounds event queries and sends a fresh UUID idempotency key", async () => {
    vi.mocked(generated.adminCharlieTriggerEventsList).mockResolvedValue({
      items: [],
    });
    vi.mocked(generated.adminCharlieTriggerEventRetry).mockResolvedValue({
      data: {
        operation_id: "00000000-0000-4000-8000-000000000010",
        event_id: "00000000-0000-4000-8000-000000000011",
        status: "pending",
        status_url: "/api/v1/charlie/operations/op-1",
      },
    });

    await listCharlieTriggerEvents("dead", -9, 400);
    expect(generated.adminCharlieTriggerEventsList).toHaveBeenCalledWith({
      query: { state: "dead", offset: 0, limit: 100 },
      signal: undefined,
    });

    await expect(retryCharlieTriggerEvent("event/a")).resolves.toEqual(
      expect.objectContaining({
        operationId: "00000000-0000-4000-8000-000000000010",
        status: "pending",
      }),
    );
    expect(generated.adminCharlieTriggerEventRetry).toHaveBeenCalledWith({
      path: { event_id: "event/a" },
      headerParams: {
        "Idempotency-Key": expect.stringMatching(/^[0-9a-f-]{36}$/),
      },
      body: { request_id: expect.stringMatching(/^[0-9a-f-]{36}$/) },
      signal: undefined,
    });
  });

  it("updates only the bounded local action-policy controls", async () => {
    vi.mocked(generated.adminCharlieActionPolicyUpdate).mockResolvedValue(
      actionPolicyWire,
    );

    await updateCharlieActionPolicy({
      capability: "astronomer.queue.retry_task",
      enabled: true,
      maxActionsPerIncident: 1,
      maxActionsPerWindow: 3,
      budgetWindowSeconds: 3600,
      cooldownSeconds: 60,
    });

    expect(generated.adminCharlieActionPolicyUpdate).toHaveBeenCalledWith({
      path: { capability: "astronomer.queue.retry_task" },
      body: {
        enabled: true,
        max_actions_per_incident: 1,
        max_actions_per_window: 3,
        budget_window_seconds: 3600,
        cooldown_seconds: 60,
      },
      signal: undefined,
    });
  });

  it("keeps alert routing product-local and sends channel references only", async () => {
    vi.mocked(generated.adminCharlieAlertPolicyUpdate).mockResolvedValue(
      alertPolicyWire,
    );

    await updateCharlieAlertPolicy({
      enabled: true,
      minimumSeverity: "high",
      dedupeWindowSeconds: 900,
      escalationAfterSeconds: 3600,
      quietHoursEnabled: true,
      quietHoursStart: "22:00",
      quietHoursEnd: "07:00",
      quietHoursTimezone: "UTC",
      revision: 1,
      channelIds: ["channel-a"],
      channels: [],
      availableChannels: [],
      inAppEnabled: true,
    });

    expect(generated.adminCharlieAlertPolicyUpdate).toHaveBeenCalledWith({
      body: {
        revision: 1,
        enabled: true,
        minimum_severity: "high",
        dedupe_window_seconds: 900,
        escalation_after_seconds: 3600,
        quiet_hours_enabled: true,
        quiet_hours_start: "22:00",
        quiet_hours_end: "07:00",
        quiet_hours_timezone: "UTC",
        channel_ids: ["channel-a"],
      },
      signal: undefined,
    });
    expect(
      JSON.stringify(
        vi.mocked(generated.adminCharlieAlertPolicyUpdate).mock.calls.at(-1),
      ),
    ).not.toMatch(/secret|api_key|approval/i);
  });

  it("surfaces stale policy revisions without implying a save", async () => {
    vi.mocked(generated.adminCharlieAlertPolicyUpdate).mockRejectedValue({
      response: { status: 409 },
    });
    await expect(
      updateCharlieAlertPolicy({
        ...alertPolicyWire,
        minimumSeverity: "high",
        dedupeWindowSeconds: 900,
        escalationAfterSeconds: 3600,
        quietHoursEnabled: false,
        quietHoursStart: "22:00",
        quietHoursEnd: "07:00",
        quietHoursTimezone: "UTC",
        revision: 3,
        channelIds: [],
        channels: [],
        availableChannels: [],
        inAppEnabled: true,
      }),
    ).rejects.toThrow(/changed.*refresh/i);

    vi.mocked(generated.adminCharlieActionPolicyUpdate).mockRejectedValue({
      response: { status: 409 },
    });
    await expect(
      updateCharlieActionPolicy({
        capability: "astronomer.queue.retry_task",
        enabled: true,
        maxActionsPerIncident: 1,
        maxActionsPerWindow: 3,
        budgetWindowSeconds: 3600,
        cooldownSeconds: 60,
      }),
    ).rejects.toThrow(/conflicts with current central allowlisting/i);
  });
});
