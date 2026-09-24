import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";

test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
async function openNavigation(page: import("@playwright/test").Page) {
  const trigger = page.getByRole("button", {
    name: "Open navigation",
    exact: true,
  });
  if (await trigger.isVisible()) await trigger.click();
}
test("canonical sidebar has one active destination on cluster and global pages", async ({
  page,
}) => {
  for (const path of [
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}`,
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/pods`,
    "/dashboard/monitoring",
    "/dashboard/delivery/rollouts",
  ]) {
    await page.goto(path);
    await expect(
      page.getByRole("button", { name: "User menu", exact: true }),
    ).toBeVisible();
    await openNavigation(page);
    const active = page.locator('aside a[aria-current="page"]');
    await expect(active).toHaveCount(1);
    await expect(active).toHaveAttribute(
      "href",
      new RegExp(path.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "/?(?:\\?.*)?$"),
    );
  }
});
test("all eight Delivery destinations preserve project context", async ({
  page,
}) => {
  await page.goto("/dashboard/delivery/rollouts?project=project-b");
  await expect(
    page.getByRole("button", { name: "User menu", exact: true }),
  ).toBeVisible();
  await openNavigation(page);
  const group = page
    .locator("aside")
    .getByRole("button", { name: "Continuous Delivery", exact: true });
  if ((await group.getAttribute("aria-expanded")) === "false")
    await group.click();
  for (const [name, suffix] of [
    ["Estate", ""],
    ["Sources", "/sources"],
    ["Bundles", "/bundles"],
    ["Targets", "/targets"],
    ["Rollouts", "/rollouts"],
    ["Deployments", "/deployments"],
    ["Templates", "/configuration-templates"],
    ["Overrides", "/override-sets"],
  ]) {
    await expect(
      page.locator("aside").getByRole("link", { name, exact: true }),
    ).toHaveAttribute("href", `/dashboard/delivery${suffix}?project=project-b`);
  }
});
test("page palette finds custom types beyond sidebar display cap without count fanout", async ({
  page,
}) => {
  const requests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/k8s/")) requests.push(request.url());
  });
  await page.route("**/resources/discovery**", (route) =>
    route.fulfill({
      json: {
        data: {
          cluster_id: SMOKE_CLUSTER_ID,
          resources: [],
          crds: Array.from({ length: 55 }, (_, index) => ({
            name: `widgets${index}.example.io`,
            group: "example.io",
            plural: `widgets${index}`,
            kind: `Widget${index}`,
            scope: "Namespaced",
            versions: [{ name: "v1", storage: true }],
          })),
          crd_continue: "",
          partial: false,
          errors: {},
        },
      },
    }),
  );
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}`);
  await page.getByRole("button", { name: "Go to page", exact: true }).click();
  await page
    .getByPlaceholder("Search clusters, pages, actions...")
    .fill("Widget54");
  await expect(page.getByRole("option", { name: /Widget54/ })).toBeVisible();
  await page.getByRole("option", { name: /Widget54/ }).click();
  await expect(page).toHaveURL(/custom-resources\/example.io\/v1\/widgets54/);
  const countPaths = requests.filter((url) => url.includes("limit=1"));
  expect(new Set(countPaths).size).toBeLessThanOrEqual(15);
});
