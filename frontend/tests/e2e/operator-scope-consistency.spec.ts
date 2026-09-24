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
