import { beforeEach, describe, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/lib/api/transport", () => ({ default: { request } }));

import {
  createQuotaPlan,
  deleteQuotaPlan,
  getMyQuota,
  getProjectEffectiveQuota,
  getQuotaPlan,
  getQuotaUsage,
  listQuotaPlans,
  updateQuotaPlan,
  type QuotaPlanWriteRequest,
} from "./quotas";

const PLAN = {
  name: "enterprise",
  enforcement: "hard",
  description: "Enterprise tenant limits",
  max_clusters_per_project: 20,
  max_namespaces_per_project: 200,
  max_members_per_project: 100,
  max_projects_per_user: 25,
  max_tokens_per_user: 50,
  max_streams_per_user: 10,
  max_total_clusters: 500,
  max_total_users: 5000,
} as const;

const BODY: QuotaPlanWriteRequest = { ...PLAN };

const USAGE = {
  global: {
    total_clusters: 175,
    max_total_clusters: 500,
    total_users: 900,
    max_total_users: 5000,
  },
  project_offenders: [
    {
      project_id: "10000000-0000-0000-0000-000000000001",
      project_name: "Platform",
      quota_plan: "enterprise",
      limit: "max_clusters_per_project",
      current: 18,
      maximum: 20,
      usage_pct: 90,
    },
  ],
  user_offenders: [
    {
      user_id: "20000000-0000-0000-0000-000000000001",
      username: "admin@example.test",
      quota_plan: "enterprise",
      limit: "max_tokens_per_user",
      current: 45,
      maximum: 50,
      usage_pct: 90,
    },
  ],
} as const;

const PROJECT = {
  project_id: "10000000-0000-0000-0000-000000000001",
  quota_plan: "enterprise",
  enforcement: "hard",
  limits: {
    max_clusters_per_project: 20,
    max_namespaces_per_project: 200,
    max_members_per_project: 100,
  },
  usage: {
    max_clusters_per_project: 8,
    max_namespaces_per_project: 42,
    max_members_per_project: 17,
  },
  usage_pct: {
    max_clusters_per_project: 40,
    max_namespaces_per_project: 21,
    max_members_per_project: 17,
  },
  overrides: {},
} as const;

const MY_QUOTA = {
  user_id: "20000000-0000-0000-0000-000000000001",
  quota_plan: "enterprise",
  enforcement: "hard",
  limits: {
    max_projects_per_user: 25,
    max_tokens_per_user: 50,
    max_streams_per_user: 10,
  },
  usage: {
    max_projects_per_user: 4,
    max_tokens_per_user: 9,
    max_streams_per_user: 0,
  },
  usage_pct: {
    max_projects_per_user: 16,
    max_tokens_per_user: 18,
    max_streams_per_user: 0,
  },
} as const;

function lastRequest() {
  return request.mock.calls.at(-1)?.[0];
}

beforeEach(() => {
  request.mockReset();
  request.mockImplementation(async (config) => {
    const url = String(config.url);
    if (config.method === "DELETE") return { data: undefined };
    if (url === "/api/v1/admin/quota-plans" && config.method === "GET") {
      return { data: { data: [PLAN], pagination: {} } };
    }
    if (url.includes("/admin/quota-plans")) {
      return { data: { data: PLAN } };
    }
    if (url === "/api/v1/admin/quota-usage") {
      return { data: { data: USAGE } };
    }
    if (url.includes("/projects/") && url.endsWith("/quota")) {
      return { data: { data: PROJECT } };
    }
    if (url === "/api/v1/auth/me/quota") {
      return { data: { data: MY_QUOTA } };
    }
    throw new Error(`Unexpected request: ${url}`);
  });
});

describe("generated quota API boundary", () => {
  it("maps paginated plans and preserves exact mutation wire bodies", async () => {
    await expect(listQuotaPlans()).resolves.toEqual([
      expect.objectContaining({
        name: "enterprise",
        maxClustersPerProject: 20,
        maxTotalUsers: 5000,
      }),
    ]);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/admin/quota-plans",
        params: { limit: 200 },
      }),
    );

    await createQuotaPlan(BODY);
    expect(lastRequest()?.data).toEqual(BODY);
    await updateQuotaPlan(PLAN.name, BODY);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "PUT",
        url: `/api/v1/admin/quota-plans/${PLAN.name}`,
        data: BODY,
      }),
    );
    await deleteQuotaPlan(PLAN.name);
    expect(lastRequest()?.method).toBe("DELETE");
  });

  it("fails closed when a single-plan envelope is malformed", async () => {
    request.mockResolvedValueOnce({ data: {} });
    await expect(getQuotaPlan(PLAN.name)).rejects.toThrow(
      "getQuotaPlan returned no data payload",
    );
  });

  it("maps exact fleet usage without inventing legacy quota dimensions", async () => {
    await expect(getQuotaUsage()).resolves.toEqual({
      fleetTotals: {
        total_clusters: 175,
        max_total_clusters: 500,
        total_users: 900,
        max_total_users: 5000,
      },
      rows: [
        expect.objectContaining({
          scope: "project",
          usage: { max_clusters_per_project: 18 },
        }),
        expect.objectContaining({
          scope: "user",
          usage: { max_tokens_per_user: 45 },
        }),
      ],
      topOffenders: expect.any(Array),
    });
  });

  it("maps project usage and returns the exact current-user snapshot", async () => {
    await expect(getProjectEffectiveQuota(PROJECT.project_id)).resolves.toEqual(
      expect.objectContaining({
        projectId: PROJECT.project_id,
        clustersUsed: 8,
        clustersLimit: 20,
        namespacesUsed: 42,
        membersLimit: 100,
      }),
    );
    await expect(getMyQuota()).resolves.toEqual(MY_QUOTA);
  });
});
