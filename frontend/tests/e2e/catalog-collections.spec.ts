import { expect, test } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
import { seedAuth } from "./helpers/auth";

const now = "2026-09-22T00:00:00Z";
const projectId = "project-catalog";
const chart = {
  id: "chart-demo",
  repository_id: "repo-demo",
  name: "demo",
  display_name: "Demo chart",
  keywords: [],
  category: "monitoring",
  created_at: now,
  updated_at: now,
};
const versions = Array.from({ length: 226 }, (_, i) => ({
  id: `v-${i + 1}`,
  chart_id: chart.id,
  version: `1.0.${i + 1}`,
  app_version: "1",
  created_at: now,
  default_values: "replicas: 1",
}));
function page<T>(rows: T[], url: URL) {
  const offset = Number(url.searchParams.get("offset") || 0);
  const limit = Number(url.searchParams.get("limit") || 25);
  const hasMore = offset + limit < rows.length;
  return {
    data: rows.slice(offset, offset + limit),
    pagination: {
      total: rows.length,
      limit,
      offset,
      has_more: hasMore,
      next_offset: hasMore ? offset + limit : null,
    },
  };
}
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
test("Catalog installed pages keep recovery outside denied content", async ({
  page: browser,
}) => {
  const rows = Array.from({ length: 51 }, (_, i) => ({
    id: `i-${i}`,
    cluster_id: SMOKE_CLUSTER_ID,
    release_name: `release-${i + 1}`,
    namespace: "default",
    status: "installed",
    revision: 1,
    created_at: now,
    updated_at: now,
  }));
  await browser.route("**/api/v1/catalog/installed/**", (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.get("offset") === "25")
      return route.fulfill({
        status: 403,
        json: { error: { message: "Denied page" } },
      });
    return route.fulfill({ json: page(rows, url) });
  });
  await browser.goto("/dashboard/catalog?tab=installed");
  await expect(browser.getByText("release-1", { exact: true })).toBeVisible();
  await browser
    .getByRole("button", { name: "Next installed charts page" })
    .click();
  await expect(
    browser.getByText("Permission required", { exact: true }),
  ).toBeVisible();
  await expect(browser.getByText("release-1", { exact: true })).toHaveCount(0);
  await browser
    .getByRole("button", { name: "Previous installed charts page" })
    .click();
  await expect(browser.getByText("release-1", { exact: true })).toBeVisible();
  expect(await browser.pageErrors()).toEqual([]);
});
test("Apps install chooses a version beyond 200 and keeps versions/defaults project-scoped", async ({
  page: browser,
}) => {
  const reads: URL[] = [];
  const writes: unknown[] = [];
  await browser.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "");
    if (path === `/projects/${projectId}`)
      return route.fulfill({
        json: {
          data: {
            id: projectId,
            name: "Catalog project",
            cluster_id: SMOKE_CLUSTER_ID,
            resource_quota: {},
            namespaces: [],
            created_at: now,
            updated_at: now,
          },
        },
      });
    if (path === "/catalog/charts")
      return route.fulfill({ json: page([chart], url) });
    if (path === `/catalog/charts/${chart.id}/versions`) {
      reads.push(url);
      return route.fulfill({ json: page(versions, url) });
    }
    if (path === `/catalog/charts/${chart.id}/values`) {
      reads.push(url);
      return route.fulfill({
        json: {
          chart: chart.name,
          version: url.searchParams.get("version"),
          default_values: "replicas: 1",
        },
      });
    }
    if (path === "/catalog/installed" && route.request().method() === "POST") {
      writes.push(route.request().postDataJSON());
      return route.fulfill({
        status: 201,
        json: {
          data: { installation: { id: "installed-demo" }, operation: {} },
        },
      });
    }
    return route.fallback();
  });
  await browser.goto(
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/apps?section=browse&project=${projectId}`,
  );
  await browser.getByRole("button", { name: "Install →", exact: true }).click();
  const dialog = browser.getByRole("dialog");
  const select = dialog.getByRole("combobox", { name: "Version", exact: true });
  await expect(select).toHaveValue("v-1");
  for (let index = 1; index <= 9; index++) {
    await dialog.getByRole("button", { name: "Next versions page" }).click();
    await expect(
      select.locator(`option[value="v-${index * 25 + 1}"]`),
    ).toHaveCount(1);
  }
  await select.selectOption("v-226");
  await expect
    .poll(() =>
      reads.some((url) => url.searchParams.get("version") === "1.0.226"),
    )
    .toBe(true);
  await dialog.getByRole("button", { name: "Install", exact: true }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toMatchObject({
    project_id: projectId,
    cluster_id: SMOKE_CLUSTER_ID,
    chart_version_id: "v-226",
  });
  expect(
    reads.every(
      (url) =>
        url.searchParams.get("project_id") === projectId &&
        !url.searchParams.has("cluster_id"),
    ),
  ).toBe(true);
  expect(
    reads
      .filter((url) => url.pathname.endsWith("/versions/"))
      .every((url) => url.searchParams.get("limit") === "25"),
  ).toBe(true);
  expect(await browser.pageErrors()).toEqual([]);
});
