import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import {
  adminStoreUser,
  SMOKE_CLUSTER_ID,
  overrides,
} from "../e2e-smoke/stub-overrides";
const original = (
  overrides.find(
    (entry) => entry.path === `/api/v1/clusters/${SMOKE_CLUSTER_ID}`,
  )!.body as { data: Record<string, unknown> }
).data;
const clusters = [
  original,
  ...["b", "c"].map((id) => ({
    ...original,
    id: `cluster-${id}`,
    name: `cluster-${id}`,
    display_name: `Target ${id.toUpperCase()}`,
  })),
];
async function fixture(page: Page) {
  await page.route(/\/api\/v1\/clusters\/?(?:\?.*)?$/, (route) =>
    route.fulfill({
      json: {
        data: clusters,
        pagination: {
          limit: 50,
          offset: 0,
          total: 3,
          has_more: false,
          next_offset: null,
        },
      },
    }),
  );
  await page.route(/\/api\/v1\/clusters\/cluster-[bc]\/?(?:\?.*)?$/, (route) =>
    route.fulfill({
      json: {
        data: clusters.find((cluster) =>
          route.request().url().includes(String(cluster.id)),
        ),
      },
    }),
  );
}
function namespaces(id: string) {
  return {
    data: [
      { name: "default", clusterId: id, createdAt: "2026-01-01T00:00:00Z" },
    ],
    pagination: {
      limit: 200,
      offset: 0,
      total: 1,
      has_more: false,
      next_offset: null,
    },
  };
}
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  await fixture(page);
});
test("latest rapid cluster selection wins and drops source resource identity", async ({
  page,
}) => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(
    "**/api/v1/clusters/cluster-b/namespaces/**",
    async (route) => {
      await pending;
      await route.fulfill({ json: namespaces("cluster-b") });
    },
  );
  await page.route("**/api/v1/clusters/cluster-c/namespaces/**", (route) =>
    route.fulfill({ json: namespaces("cluster-c") }),
  );
  await page.goto(
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/pods/default/same-name?tab=logs&container=app`,
  );
  await page
    .locator("header")
    .getByTitle("Switch cluster (Ctrl/Cmd+J)")
    .click();
  await page.getByRole("option", { name: /Target B/ }).click();
  await expect(page.getByText("Resolving target cluster scope…")).toBeVisible();
  await page.getByRole("option", { name: /Target C/ }).click();
  await expect(page).toHaveURL(/\/clusters\/cluster-c\/pods(?:\?|$)/);
  const lateResponse = page.waitForResponse((response) =>
    response.url().includes("/clusters/cluster-b/namespaces"),
  );
  release();
  await (await lateResponse).finished();
  await expect(page).toHaveURL(/\/clusters\/cluster-c\/pods(?:\?|$)/);
  await expect(
    page.locator("header").getByTitle("Switch cluster (Ctrl/Cmd+J)"),
  ).toContainText("Target C");
  expect(new URL(page.url()).searchParams.has("container")).toBe(false);
  expect(new URL(page.url()).searchParams.has("tab")).toBe(false);
});
test("clearing an invalid remembered project recovers the target scope", async ({
  page,
}) => {
  await page.addInitScript(() =>
    localStorage.setItem(
      "astronomer-cluster-scope",
      JSON.stringify({
        state: {
          namespacesByCluster: { "cluster-b": ["old"] },
          projectByCluster: { "cluster-b": "missing" },
          recentClusterIds: [],
        },
        version: 2,
      }),
    ),
  );
  let missingReads = 0;
  await page.route("**/api/v1/projects/missing/**", (route) => {
    missingReads++;
    return route.fulfill({
      status: 404,
      json: { error: { message: "Project no longer available" } },
    });
  });
  await page.route("**/api/v1/clusters/cluster-b/namespaces/**", (route) =>
    route.fulfill({ json: namespaces("cluster-b") }),
  );
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments`);
  await page
    .locator("header")
    .getByTitle("Switch cluster (Ctrl/Cmd+J)")
    .click();
  await page.getByRole("option", { name: /Target B/ }).click();
  await page
    .getByRole("button", {
      name: "Clear remembered scope and switch",
      exact: true,
    })
    .click();
  await expect(page).toHaveURL(/\/clusters\/cluster-b\/deployments/);
  await expect(
    page.locator("header").getByRole("button", { name: /Namespace scope/ }),
  ).toBeEnabled();
  expect(missingReads).toBe(1);
  expect(new URL(page.url()).searchParams.has("project")).toBe(false);
});
