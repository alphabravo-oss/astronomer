import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { EntityResponse } from "@/lib/api/delivery-common";
import * as api from "@/lib/api/delivery-rollouts";
import { can } from "@/lib/permissions";
import { RolloutDetailPage } from "./index";

const access = vi.hoisted(() => ({ denied: new Set<string>() }));
vi.mock("@/lib/hooks/auth", () => ({
  useCurrentUser: () => ({ data: { id: "operator" } }),
}));
vi.mock("@/lib/permissions", () => ({
  can: vi.fn(
    (_user: unknown, resource: string, verb: string) =>
      !access.denied.has(`${resource}:${verb}`),
  ),
}));
vi.mock("@/lib/live/hooks", () => ({ useLiveQueryInvalidation: vi.fn() }));
vi.mock("@/lib/toast", () => ({ toastSuccess: vi.fn() }));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  Link: (await import("@/test/router-link")).RouterLinkStub,
  useParams: () => ({ rolloutId: "rollout-1" }),
}));
vi.mock("@/components/delivery/shared", async (original) => ({
  ...(await original<typeof import("@/components/delivery/shared")>()),
  DeliveryShell: ({ children }: { children: ReactNode }) => children,
  useDeliveryPageIndex: () => [0, vi.fn()],
  useDeliveryWorkspace: () => ({
    projectId: "project-1",
    projects: [{ id: "project-1", name: "Production" }],
    projectQuery: { isLoading: false, isError: false, refetch: vi.fn() },
    setProjectId: vi.fn(),
    listHref: () => "/dashboard/delivery/rollouts",
  }),
}));
vi.mock("@/lib/api/delivery-rollouts", async (original) => ({
  ...(await original<typeof import("@/lib/api/delivery-rollouts")>()),
  getDeliveryRollout: vi.fn(),
  listDeliveryRolloutClusters: vi.fn(),
  listDeliveryRolloutEvents: vi.fn(),
  actOnDeliveryRollout: vi.fn(),
  approveDeliveryRollout: vi.fn(),
}));

function detail(
  state: api.RolloutState = "paused",
): EntityResponse<api.DeliveryRolloutDetail> {
  const strategy: api.RolloutStrategy = {
    type: "rolling",
    maxConcurrent: 2,
    maxUnavailable: { type: "count", value: 1 },
    minReady: "30s",
    progressDeadline: "30m",
    failureThreshold: { type: "count", value: 1 },
    onFailure: "pause",
    respectMaintenanceWindows: true,
  };
  return {
    etag: '"fence-17"',
    data: {
      rollout: {
        id: "rollout-1",
        targetId: "target-1",
        targetGeneration: 2,
        toBundleVersionId: "version-2",
        placementDigest: "placement",
        planDigest: "plan",
        strategy,
        state,
        fencingGeneration: 17,
        totalClusters: 2,
        readyClusters: 1,
        failedClusters: 0,
        blockedClusters: 1,
        releasedClusters: 1,
        createdAt: "2026-09-10T00:00:00Z",
        updatedAt: "2026-09-10T00:00:00Z",
      },
      frozenPlan: {
        id: "plan",
        projectId: "project-1",
        targetId: "target-1",
        targetGeneration: 2,
        desired: {
          bundleVersionId: "version-2",
          specDigest: "spec",
          source: {
            sourceId: "source-1",
            type: "git",
            url: "https://example.test/repo",
            authMode: "none",
            trust: { allowUnsigned: false },
            revision: {
              kind: "commit",
              value: "abc",
              artifactDigest: "artifact",
            },
          },
        },
        placementDigest: "placement",
        strategy,
        strategyDigest: "strategy",
        approval: { required: true, digest: "sha256:exact-frozen-digest" },
        actor: "operator",
        requestDigest: "request",
        createdAt: "2026-09-10T00:00:00Z",
        deadline: "2026-09-10T00:30:00Z",
        cohorts: [],
        planDigest: "plan",
      },
      approvals: [],
      timeline: [],
    },
  };
}

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const invalidation = vi.spyOn(client, "invalidateQueries");
  render(
    <QueryClientProvider client={client}>
      <RolloutDetailPage />
    </QueryClientProvider>,
  );
  return { invalidation };
}

beforeEach(() => {
  vi.clearAllMocks();
  access.denied.clear();
  vi.mocked(api.getDeliveryRollout).mockResolvedValue(detail());
  const page = {
    data: [],
    pagination: {
      total: 0,
      limit: 50,
      offset: 0,
      has_more: false,
      next_offset: null,
    },
  };
  vi.mocked(api.listDeliveryRolloutClusters).mockResolvedValue(page);
  vi.mocked(api.listDeliveryRolloutEvents).mockResolvedValue(page);
});

describe("rollout detail behavior", () => {
  it("submits a reasoned action against the exact ETag and refreshes durable state", async () => {
    vi.mocked(api.actOnDeliveryRollout).mockResolvedValue({
      operation_id: "operation-1",
      operation: "delivery.rollout.resume",
      resource: "delivery_rollouts",
      resource_id: "rollout-1",
      project_id: "project-1",
      status: "accepted",
      status_url: "/api/v1/operations/operation-1/",
      accepted_at: "2026-09-10T00:00:00Z",
    });
    const { invalidation } = mount();
    fireEvent.click(await screen.findByRole("button", { name: /^resume$/i }));
    const dialog = screen.getByRole("dialog", { name: "Resume rollout" });
    fireEvent.change(within(dialog).getByLabelText("Audit reason code"), {
      target: { value: "  verified_capacity  " },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Confirm resume" }),
    );
    await waitFor(() =>
      expect(api.actOnDeliveryRollout).toHaveBeenCalledWith(
        "project-1",
        "rollout-1",
        "resume",
        '"fence-17"',
        "verified_capacity",
        expect.any(String),
      ),
    );
    expect(vi.mocked(api.actOnDeliveryRollout).mock.calls[0][5]).toMatch(
      /^[0-9a-f-]{36}$/,
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(invalidation).toHaveBeenCalledTimes(2);
  });

  it("keeps a rejected CAS visible and preserves the audit reason for correction", async () => {
    vi.mocked(api.actOnDeliveryRollout).mockRejectedValue(
      new Error("Rollout generation changed"),
    );
    mount();
    fireEvent.click(await screen.findByRole("button", { name: /^resume$/i }));
    fireEvent.change(screen.getByLabelText("Audit reason code"), {
      target: { value: "operator_resume" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Confirm resume" }));
    expect(
      await screen.findByText("Rollout generation changed"),
    ).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByLabelText("Audit reason code")).toHaveValue(
      "operator_resume",
    );
  });

  it("binds approval to the frozen digest, cohort, project, expiry and fence", async () => {
    mount();
    fireEvent.click(
      await screen.findByRole("button", { name: "Review approval" }),
    );
    const start = Date.now();
    fireEvent.click(
      screen.getByRole("button", { name: "Approve exact digest" }),
    );
    await waitFor(() =>
      expect(api.approveDeliveryRollout).toHaveBeenCalledWith(
        "rollout-1",
        {
          project_id: "project-1",
          cohort: -1,
          binding_digest: "sha256:exact-frozen-digest",
          decision: "approved",
          expires_at: expect.any(String),
        },
        '"fence-17"',
        expect.any(String),
      ),
    );
    const expiry = Date.parse(
      vi.mocked(api.approveDeliveryRollout).mock.calls[0][1].expires_at,
    );
    expect(expiry).toBeGreaterThanOrEqual(start + 30 * 60_000);
    expect(expiry).toBeLessThanOrEqual(Date.now() + 30 * 60_000);
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("keeps rollback and approval permissions separate from ordinary updates", async () => {
    access.denied.add("delivery_rollbacks:rollback");
    access.denied.add("delivery_approvals:approve");
    mount();
    expect(
      await screen.findByRole("button", { name: /^resume$/i }),
    ).toBeEnabled();
    expect(screen.getByRole("button", { name: /^rollback$/i })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Review approval" }),
    ).toBeDisabled();
    expect(can).toHaveBeenCalledWith(
      expect.anything(),
      "delivery_rollbacks",
      "rollback",
      { type: "project", id: "project-1" },
    );
    expect(api.actOnDeliveryRollout).not.toHaveBeenCalled();
  });

  it("does not fetch rollout or cluster state without project read permission", async () => {
    access.denied.add("delivery_rollouts:read");
    mount();
    expect(await screen.findByText(/permission required/i)).toBeInTheDocument();
    expect(api.getDeliveryRollout).not.toHaveBeenCalled();
    expect(api.listDeliveryRolloutClusters).not.toHaveBeenCalled();
    expect(api.listDeliveryRolloutEvents).not.toHaveBeenCalled();
  });
});
