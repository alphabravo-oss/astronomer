import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
test("namespace selection is sent before workload pagination", async ({
  page,
}, info) => {
  await page.route("**/api/v1/clusters/*/namespaces/**", (route) =>
    route.fulfill({
      json: {
        data: ["team-a", "team-b"].map((name) => ({
          name,
          clusterId: SMOKE_CLUSTER_ID,
          status: "Active",
          createdAt: "2026-01-01T00:00:00Z",
        })),
        pagination: {
          limit: 200,
          offset: 0,
          total: 2,
          has_more: false,
          next_offset: null,
        },
      },
    }),
  );
  const requests: string[] = [];
  await page.route(
    /\/api\/v1\/clusters\/[^/]+\/workloads\/?(?:\?.*)?$/,
    (route) => {
      requests.push(route.request().url());
      const u = new URL(route.request().url());
      const scoped = u.searchParams.get("namespace") === "team-b";
      return route.fulfill({
        json: {
          data: Array.from({ length: scoped ? 1 : 50 }, (_, index) => ({
            id: `workload-review-${index}`,
            name: scoped ? "wanted-app" : `unrelated-app-${index}`,
            namespace: scoped ? "team-b" : "team-a",
            clusterId: SMOKE_CLUSTER_ID,
            kind: "Deployment",
            status: "Running",
            replicas: 1,
            desiredReplicas: 1,
            createdAt: "2026-01-01T00:00:00Z",
          })),
          pagination: {
            limit: 50,
            offset: 0,
            total: scoped ? 1 : 51,
            has_more: !scoped,
            next_offset: scoped ? null : 50,
          },
        },
      });
    },
  );
  await page.goto(
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments?namespaces=team-b`,
  );
  await expect(
    page.getByRole("button", { name: "Namespace scope: team-b", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "wanted-app", exact: true }),
  ).toBeVisible();
  expect(requests.length).toBeGreaterThan(0);
  expect(
    requests.every(
      (url) => new URL(url).searchParams.get("namespace") === "team-b",
    ),
  ).toBeTruthy();
  await page.screenshot({ path: info.outputPath("workload-scope.png") });
});

test("multi-namespace scope is sent as repeated API parameters and empty scope sends no collection request", async ({
  page,
}) => {
  await page.route("**/api/v1/clusters/*/namespaces/**", (route) =>
    route.fulfill({
      json: {
        data: ["team-a", "team-b"].map((name) => ({
          name,
          clusterId: SMOKE_CLUSTER_ID,
          createdAt: "2026-01-01T00:00:00Z",
        })),
        pagination: {
          limit: 200,
          offset: 0,
          total: 2,
          has_more: false,
          next_offset: null,
        },
      },
    }),
  );
  const requests: string[] = [];
  await page.route(
    /\/api\/v1\/clusters\/[^/]+\/workloads\/?(?:\?.*)?$/,
    (route) => {
      requests.push(route.request().url());
      return route.fulfill({
        json: {
          data: [],
          pagination: {
            limit: 50,
            offset: 0,
            total: 0,
            has_more: false,
            next_offset: null,
          },
        },
      });
    },
  );
  await page.goto(
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments?namespaces=team-a,team-b`,
  );
  await expect.poll(() => requests.length).toBeGreaterThan(0);
  expect(
    requests.every(
      (url) =>
        new URL(url).searchParams.getAll("namespaces").join(",") ===
        "team-a,team-b",
    ),
  ).toBeTruthy();
  await page.goto("about:blank");
  requests.length = 0;
  await page.goto(
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments?namespaces=`,
  );
  await expect(
    page.getByText("No namespaces selected", { exact: false }).first(),
  ).toBeVisible();
  expect(requests).toEqual([]);
});

test("direct secondary-project entry resolves its own namespaces before any workload request", async ({
  page,
}) => {
  await page.addInitScript(
    (cluster) =>
      localStorage.setItem(
        "astronomer-cluster-scope",
        JSON.stringify({
          state: {
            namespacesByCluster: { [cluster]: ["team-a"] },
            projectByCluster: {},
            recentClusterIds: [],
          },
          version: 2,
        }),
      ),
    SMOKE_CLUSTER_ID,
  );
  await page.route("**/api/v1/clusters/*/namespaces/**", (route) =>
    route.fulfill({
      json: {
        data: ["team-a", "team-b"].map((name) => ({
          name,
          clusterId: SMOKE_CLUSTER_ID,
          createdAt: "2026-01-01T00:00:00Z",
        })),
        pagination: {
          limit: 200,
          offset: 0,
          total: 2,
          has_more: false,
          next_offset: null,
        },
      },
    }),
  );
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/projects/project-b/**", async (route) => {
    await pending;
    await route.fulfill({
      json: {
        data: {
          id: "project-b",
          name: "B",
          display_name: "Project B",
          cluster_id: "primary-cluster",
          cluster_ids: ["primary-cluster", SMOKE_CLUSTER_ID],
          namespaces: ["team-a"],
          namespace_scopes: [
            { cluster_id: "primary-cluster", namespaces: ["team-a"] },
            { cluster_id: SMOKE_CLUSTER_ID, namespaces: ["team-b"] },
          ],
          resource_quota: {},
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
      },
    });
  });
  const requests: string[] = [];
  await page.route(
    /\/api\/v1\/clusters\/[^/]+\/workloads\/?(?:\?.*)?$/,
    (route) => {
      requests.push(route.request().url());
      return route.fulfill({
        json: {
          data: [],
          pagination: {
            limit: 50,
            offset: 0,
            total: 0,
            has_more: false,
            next_offset: null,
          },
        },
      });
    },
  );
  await page.goto(
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments?project=project-b`,
  );
  await expect(
    page.getByText("Resolving namespace scope", { exact: false }).first(),
  ).toBeVisible();
  expect(requests).toEqual([]);
  release();
  await expect.poll(() => requests.length).toBeGreaterThan(0);
  expect(
    requests.every(
      (url) => new URL(url).searchParams.get("namespace") === "team-b",
    ),
  ).toBeTruthy();
  await expect(page).toHaveURL(/namespaces=team-b/);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Namespace scope: team-b", exact: true }),
  ).toBeVisible();
  expect(
    requests.every(
      (url) => new URL(url).searchParams.get("namespace") === "team-b",
    ),
  ).toBeTruthy();
});

test("a removed remembered namespace never reaches the collection API", async ({
  page,
}) => {
  await page.addInitScript(
    (cluster) =>
      localStorage.setItem(
        "astronomer-cluster-scope",
        JSON.stringify({
          state: {
            namespacesByCluster: { [cluster]: ["removed"] },
            projectByCluster: {},
            recentClusterIds: [],
          },
          version: 2,
        }),
      ),
    SMOKE_CLUSTER_ID,
  );
  await page.route("**/api/v1/clusters/*/namespaces/**", (route) =>
    route.fulfill({
      json: {
        data: [
          {
            name: "current",
            clusterId: SMOKE_CLUSTER_ID,
            createdAt: "2026-01-01T00:00:00Z",
          },
        ],
        pagination: {
          limit: 200,
          offset: 0,
          total: 1,
          has_more: false,
          next_offset: null,
        },
      },
    }),
  );
  const requests: string[] = [];
  await page.route(
    /\/api\/v1\/clusters\/[^/]+\/workloads\/?(?:\?.*)?$/,
    (route) => {
      requests.push(route.request().url());
      return route.fulfill({
        json: {
          data: [],
          pagination: {
            limit: 50,
            offset: 0,
            total: 0,
            has_more: false,
            next_offset: null,
          },
        },
      });
    },
  );
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments`);
  await expect(
    page.getByText("No namespaces selected", { exact: false }).first(),
  ).toBeVisible();
  expect(requests).toEqual([]);
});
