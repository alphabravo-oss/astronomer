import { expect, test } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
import { seedAuth } from "./helpers/auth";

const projectId = "project-pickers";
function pageData<T>(
  data: T[],
  offset: number,
  hasMore: boolean,
  next: number | null,
) {
  return {
    data,
    pagination: {
      limit: 25,
      offset,
      has_more: hasMore,
      next_offset: next,
    },
  };
}
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
test("delivery target picks a late bundle and advances past ineligible versions", async ({
  page,
}) => {
  const writes: unknown[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "");
    const offset = Number(url.searchParams.get("offset") || 0);
    if (path === `/projects/${projectId}`)
      return route.fulfill({
        json: {
          data: {
            id: projectId,
            name: "Picker project",
            resource_quota: {},
            namespaces: [],
          },
        },
      });
    if (path === "/delivery/bundles")
      return route.fulfill({
        json: pageData(
          Array.from({ length: offset === 225 ? 1 : 25 }, (_, index) => ({
            id: `b-${offset + index + 1}`,
            name: `Bundle ${offset + index + 1}`,
            project_id: projectId,
          })),
          offset,
          offset < 225,
          offset < 225 ? offset + 25 : null,
        ),
      });
    if (path === "/delivery/bundles/b-226/versions")
      return route.fulfill({
        json: pageData(
          [
            {
              id: offset ? "ready-version" : "pending-version",
              version: offset ? "2" : "1",
              state: offset ? "ready" : "pending",
              verification_status: offset ? "verified" : "pending",
            },
          ],
          offset,
          !offset,
          offset ? null : 7,
        ),
      });
    if (path === "/delivery/targets" && route.request().method() === "POST") {
      writes.push(route.request().postDataJSON());
      return route.fulfill({
        status: 403,
        json: { error: { message: "Target mutation denied" } },
      });
    }
    return route.fallback();
  });
  await page.goto(`/dashboard/delivery/targets?project=${projectId}`);
  await page.getByRole("button", { name: "New target" }).click();
  const dialog = page.getByRole("dialog");
  const bundle = dialog.getByRole("combobox", { name: "Bundle", exact: true });
  for (let index = 1; index <= 9; index++) {
    await dialog
      .getByRole("button", { name: "Next bundle page", exact: true })
      .click();
    await expect(
      bundle.locator(`option[value="b-${index * 25 + 1}"]`),
    ).toHaveCount(1);
  }
  await bundle.selectOption("b-226");
  const version = dialog.getByRole("combobox", {
    name: "Bundle version",
    exact: true,
  });
  await expect(version.locator('option[value="pending-version"]')).toHaveCount(
    0,
  );
  await dialog
    .getByRole("button", { name: "Next bundle version page" })
    .click();
  await version.selectOption("ready-version");
  await dialog.getByLabel("Name", { exact: true }).fill("late-target");
  await dialog
    .getByRole("checkbox", {
      name: "Select every eligible cluster in this project",
    })
    .check();
  await dialog
    .getByRole("button", { name: "Create target", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toMatchObject({
    project_id: projectId,
    bundle_version_id: "ready-version",
    name: "late-target",
  });
  await expect(dialog).toBeVisible();
  await expect(
    dialog.getByText("Target mutation denied", { exact: false }),
  ).toBeVisible();
  expect(await page.pageErrors()).toEqual([]);
});

test("cluster template binding failure is not an empty binding and selection is paged", async ({
  page,
}) => {
  let denied = true;
  const writes: unknown[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "");
    if (
      path === `/clusters/${SMOKE_CLUSTER_ID}/template` &&
      route.request().method() === "GET"
    )
      return route.fulfill(
        denied
          ? { status: 403, json: { error: { message: "Binding denied" } } }
          : { status: 404, json: { error: { message: "No binding" } } },
      );
    if (path === "/cluster-templates") {
      const offset = Number(url.searchParams.get("offset") || 0);
      return route.fulfill({
        json: pageData(
          [
            {
              id: offset ? "template-226" : "template-1",
              name: offset ? "Late template" : "First template",
              spec: {},
            },
          ],
          offset,
          !offset,
          offset ? null : 225,
        ),
      });
    }
    if (
      path === `/clusters/${SMOKE_CLUSTER_ID}/template` &&
      route.request().method() !== "GET"
    ) {
      writes.push(route.request().postDataJSON());
      return route.fulfill({
        status: 403,
        json: { error: { message: "Apply denied" } },
      });
    }
    return route.fallback();
  });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/template`);
  await expect(
    page.getByText("Permission required", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Apply Template", exact: true }),
  ).toHaveCount(0);
  denied = false;
  await page.reload();
  await page
    .getByRole("button", { name: "Next cluster template page" })
    .click();
  await page
    .getByRole("combobox", { name: "Cluster template", exact: true })
    .selectOption("template-226");
  await page
    .getByRole("button", { name: "Apply Template", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toMatchObject({ template_id: "template-226" });
  await expect(
    page.getByRole("combobox", { name: "Cluster template", exact: true }),
  ).toHaveValue("template-226");
  expect(await page.pageErrors()).toEqual([]);
});
