import { beforeEach, describe, expect, it, vi, type Mocked } from "vitest";
import api from "@/lib/api";
vi.mock("@/lib/api", () => ({
  default: { post: vi.fn(), put: vi.fn(), patch: vi.fn(), get: vi.fn() },
}));
import {
  listCharlieTriggerEvents,
  retryCharlieTriggerEvent,
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
          serviceIdentity: "charlie-automation",
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
});
