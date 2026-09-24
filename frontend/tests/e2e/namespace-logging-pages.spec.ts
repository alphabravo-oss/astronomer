import { expect, test } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
import { seedAuth } from "./helpers/auth";
const now = "2026-09-22T00:00:00Z";
const envelope = (data: unknown[], offset = 0, more = false) => ({
  data,
  pagination: {
    limit: 25,
    offset,
    has_more: more,
    next_offset: more ? 225 : null,
  },
});
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
test("namespace resources remain scoped and recover from a denied later page", async ({
  page,
}) => {
  const reads: URL[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/\/$/, "");
    if (path === `/api/v1/clusters/${SMOKE_CLUSTER_ID}/namespaces`)
      return route.fulfill({
        json: envelope([
          {
            name: "team-a",
            clusterId: SMOKE_CLUSTER_ID,
            createdAt: now,
            status: "Active",
          },
        ]),
      });
    if (
      path ===
      `/api/v1/clusters/${SMOKE_CLUSTER_ID}/resources/generic/configmaps`
    ) {
      reads.push(url);
      if (url.searchParams.get("offset") === "225")
        return route.fulfill({
          status: 403,
          json: { error: { message: "Configuration page denied" } },
        });
      return route.fulfill({
        json: envelope(
          [
            {
              name: "scoped-config",
              namespace: "team-a",
              clusterId: SMOKE_CLUSTER_ID,
              createdAt: now,
              labels: {},
              annotations: {},
            },
          ],
          0,
          true,
        ),
      });
    }
    return route.fallback();
  });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/namespaces/team-a`);
  await page.getByRole("tab", { name: "Configuration", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "scoped-config", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Next configmaps page" }).click();
  await expect(
    page.getByText("Permission required", { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "scoped-config", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Previous configmaps page" }).click();
  await expect(
    page.getByRole("link", { name: "scoped-config", exact: true }),
  ).toBeVisible();
  expect(
    reads.every(
      (url) =>
        url.searchParams.get("namespace") === "team-a" &&
        url.searchParams.get("limit") === "25",
    ),
  ).toBe(true);
  expect(await page.pageErrors()).toEqual([]);
});
test("logging operation pages preserve server filters and failure recovery", async ({
  page,
}) => {
  const reads: URL[] = [];
  await page.route("**/api/v1/logging/operations**", (route) => {
    const url = new URL(route.request().url());
    reads.push(url);
    if (url.searchParams.get("offset") === "225")
      return route.fulfill({
        status: 503,
        json: { error: { message: "Operations unavailable" } },
      });
    return route.fulfill({
      json: envelope(
        [
          {
            id: "op-a",
            targetType: "output",
            targetKey: "output-a",
            operationType: "apply",
            status: "running",
            attemptCount: 1,
            createdAt: now,
            updatedAt: now,
            errorMessage: "",
          },
        ],
        0,
        true,
      ),
    });
  });
  await page.goto(
    "/dashboard/logging?tab=operations&op_status=running&op_target=output",
  );
  await page
    .getByRole("button", { name: "Next logging operations page" })
    .click();
  await expect(
    page.getByText("Operations unavailable", { exact: false }).first(),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Previous logging operations page" })
    .click();
  await expect(page.getByText("Running", { exact: true }).last()).toBeVisible();
  expect(reads.some((url) => url.searchParams.get("offset") === "225")).toBe(
    true,
  );
  expect(
    reads.every(
      (url) =>
        url.searchParams.get("status") === "running" &&
        url.searchParams.get("targetType") === "output" &&
        url.searchParams.get("limit") === "25",
    ),
  ).toBe(true);
  expect(await page.pageErrors()).toEqual([]);
});
