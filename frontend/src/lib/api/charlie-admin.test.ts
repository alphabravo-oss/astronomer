import { beforeEach, describe, expect, it, vi, type Mocked } from "vitest";
import api from "@/lib/api";
vi.mock("@/lib/api", () => ({
  default: { post: vi.fn(), put: vi.fn(), patch: vi.fn(), get: vi.fn() },
}));
import {
  listCharlieTriggerEvents,
  retryCharlieTriggerEvent,
  updateCharlieAlertPolicy,
  updateCharlieActionPolicy,
  updateCharlieAutomation,
  validateCharlieOnboarding,
} from "./charlie-admin";
const mockedApi = api as Mocked<typeof api>;

describe("Charlie admin wire boundary", () => {
  beforeEach(() => vi.clearAllMocks());
  it("sends onboarding confirmation in the exact snake-case contract", async () => {
    mockedApi.post.mockResolvedValue({
      data: {
        data: {
          packageId: "p",
          deploymentId: "d",
          routeId: "r",
          state: "validated",
          idempotent: false,
        },
      },
    });
    await validateCharlieOnboarding({
      package: {
        version: "charlie.onboarding/v1",
        enrollment_credential: "write-only",
      },
      signingPublicKey: "public",
      confirmedSigningKeyId: "kid",
      confirmedSigningFingerprint: "f".repeat(64),
      expectedDeploymentId: "d",
      expectedRouteId: "r",
    });
    expect(mockedApi.post).toHaveBeenCalledWith(
      "/admin/charlie/onboarding/validate/",
      expect.objectContaining({
        signing_public_key: "public",
        confirmed_signing_fingerprint: "f".repeat(64),
        expected_deployment_id: "d",
      }),
    );
  });
  it("serializes every trigger policy field without hidden client defaults", async () => {
    mockedApi.patch.mockResolvedValue({ data: {} });
    mockedApi.put.mockResolvedValue({ data: {} });
    mockedApi.get.mockResolvedValue({
      data: {
        data: { rules: [], defaultsRevision: 1, serviceIdentityEnabled: true },
      },
    });
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
          fleetThresholdPercent: 25,
          minimumAgentVersion: "1.2.3",
          suppressed: false,
          maximumAttempts: 5,
          deadLetterEnabled: true,
          serviceIdentity: "system:charlie-automation",
          modeCeiling: "auto",
        },
      ],
    });
    expect(mockedApi.patch).toHaveBeenCalledWith(
      "/admin/charlie/trigger-rules/r/",
      expect.objectContaining({
        cooldown_seconds: 1800,
        grace_period_seconds: 300,
        flap_window_seconds: 900,
        flap_count: 3,
        fleet_threshold_percent: 25,
        maximum_attempts: 5,
        dead_letter_enabled: true,
        mode_ceiling: "auto",
      }),
    );
    expect(mockedApi.put).toHaveBeenCalledWith("/admin/charlie/access/", {
      automation_service_identity_enabled: true,
    });
  });
  it("bounds trigger-event queries and sends a fresh retry idempotency key", async () => {
    mockedApi.get.mockResolvedValue({ data: { data: { items: [] } } });
    mockedApi.post.mockResolvedValue({
      data: { data: { event: { id: "retry-a", state: "retry" } } },
    });

    await listCharlieTriggerEvents("dead", -9, 400);
    expect(mockedApi.get).toHaveBeenCalledWith(
      "/admin/charlie/trigger-events/",
      { params: { state: "dead", offset: 0, limit: 100 } },
    );

    const event = await retryCharlieTriggerEvent("event/a");
    expect(event).toEqual(expect.objectContaining({ id: "retry-a" }));
    expect(mockedApi.post).toHaveBeenCalledWith(
      "/admin/charlie/trigger-events/event%2Fa/retry/",
      { request_id: expect.stringMatching(/^[0-9a-f-]{36}$/) },
    );
  });
  it("updates only the bounded local action-policy controls", async () => {
    mockedApi.put.mockResolvedValue({
      data: { data: { capability: "astronomer.queue.retry_task", enabled: true } },
    });
    await updateCharlieActionPolicy({
      capability: "astronomer.queue.retry_task",
      enabled: true,
      maxActionsPerIncident: 1,
      maxActionsPerWindow: 3,
      budgetWindowSeconds: 3600,
      cooldownSeconds: 60,
    });
    expect(mockedApi.put).toHaveBeenCalledWith(
      "/admin/charlie/action-policies/astronomer.queue.retry_task/",
      {
        enabled: true,
        max_actions_per_incident: 1,
        max_actions_per_window: 3,
        budget_window_seconds: 3600,
        cooldown_seconds: 60,
      },
    );
  });
  it("keeps Charlie alert routing product-local and sends channel references only", async () => {
    mockedApi.put.mockResolvedValue({ data: { data: { revision: 2 } } });
    await updateCharlieAlertPolicy({
      enabled: true, minimumSeverity: "high", dedupeWindowSeconds: 900,
      escalationAfterSeconds: 3600, quietHoursEnabled: true,
      quietHoursStart: "22:00", quietHoursEnd: "07:00", quietHoursTimezone: "UTC",
      revision: 1, channelIds: ["channel-a"], channels: [], availableChannels: [], inAppEnabled: true,
    });
    expect(mockedApi.put).toHaveBeenCalledWith("/admin/charlie/alert-policy/", {
      revision: 1, enabled: true, minimum_severity: "high", dedupe_window_seconds: 900,
      escalation_after_seconds: 3600, quiet_hours_enabled: true,
      quiet_hours_start: "22:00", quiet_hours_end: "07:00", quiet_hours_timezone: "UTC",
      channel_ids: ["channel-a"],
    });
    expect(JSON.stringify(mockedApi.put.mock.calls.at(-1))).not.toMatch(/secret|api_key|approval/i);
  });
  it("surfaces stale alert-policy revisions without implying a save", async () => {
    mockedApi.put.mockRejectedValue({ response: { status: 409 } });
    await expect(updateCharlieAlertPolicy({
      enabled: true, minimumSeverity: "high", dedupeWindowSeconds: 900,
      escalationAfterSeconds: 3600, quietHoursEnabled: false,
      quietHoursStart: "22:00", quietHoursEnd: "07:00", quietHoursTimezone: "UTC",
      revision: 3, channelIds: [], channels: [], availableChannels: [], inAppEnabled: true,
    })).rejects.toThrow(/changed.*refresh/i);
  });
  it("surfaces action-policy conflicts without implying a save", async () => {
    mockedApi.put.mockRejectedValue({ response: { status: 409 } });
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
