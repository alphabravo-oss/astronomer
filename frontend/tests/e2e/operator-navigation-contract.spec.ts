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
    "/dashboard/monitoring/stacks",
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

test("collapsed sidebar exposes one canonical active link and restores keyboard focus", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/dashboard/monitoring");
  await page
    .getByRole("button", { name: "Collapse sidebar", exact: true })
    .click();
  const trigger = page
    .locator("aside")
    .getByRole("button", { name: "Observability", exact: true });
  await trigger.click();
  const flyout = page.getByRole("navigation", {
    name: "Observability",
    exact: true,
  });
  await expect(flyout.locator('a[aria-current="page"]')).toHaveCount(1);
  await expect(flyout.locator('a[aria-current="page"]')).toHaveAttribute(
    "href",
    "/dashboard/monitoring",
  );
  await page.keyboard.press("Escape");
  await expect(trigger).toBeFocused();
});

test("installed tools retain the Management onboarding template destination", async ({
  page,
}, info) => {
  let statusReads = 0;
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") ===
      `/api/v1/clusters/${SMOKE_CLUSTER_ID}/tools/status`,
    (route) => {
      statusReads++;
      return route.fulfill({
        json: {
          data: ["velero", "monitoring", "cert-manager", "dex"].map((slug) => ({
            slug,
            name: slug,
            status: "installed",
            release_name: slug,
            namespace: "astronomer-system",
            preset_used: "default",
            error: "",
          })),
        },
      });
    },
  );
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") ===
      `/api/v1/clusters/${SMOKE_CLUSTER_ID}/template`,
    (route) =>
      route.fulfill({
        json: {
          data: {
            template_id: "applied-navigation-template",
            template_name: "Applied navigation template",
            status: "applied",
            applied_at: "2026-09-01T00:00:00Z",
            spec_snapshot: { tools: [] },
          },
        },
      }),
  );
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}`);
  await expect.poll(() => statusReads).toBeGreaterThan(0);
  await expect(
    page.locator("header").getByRole("button", { name: "Import", exact: true }),
  ).toBeVisible();
  await openNavigation(page);
  const management = page
    .locator("aside")
    .getByRole("button", { name: "Cluster Management", exact: true });
  if ((await management.getAttribute("aria-expanded")) === "false")
    await management.click();
  const link = page
    .locator("aside")
    .getByRole("link", { name: "Onboarding template", exact: true });
  await expect(link).toHaveAttribute(
    "href",
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/template`,
  );
  await link.click();
  await expect(
    page.getByRole("heading", { name: "Template", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Applied navigation template", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("installed-tools-template.png"),
  });
});

test("keyboard selection distinguishes the cluster and workload Overview commands", async ({
  page,
}) => {
  const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  await page.goto(base);
  await expect(
    page.getByRole("button", { name: "Go to page", exact: true }),
  ).toBeVisible();
  for (const destination of [base, `${base}/workloads`]) {
    await page.keyboard.press("ControlOrMeta+k");
    const search = page.getByPlaceholder("Search clusters, pages, actions...");
    await expect(search).toBeFocused();
    await page.keyboard.type("Overview");
    const expected = page.locator(
      `[cmdk-item][data-value^="${destination} Overview "]`,
    );
    await expect(expected).toBeVisible();
    const selected = page.locator('[cmdk-item][data-selected="true"]');
    const limit = await page.getByRole("option").count();
    for (
      let index = 0;
      index < limit &&
      (await selected.getAttribute("data-value")) !==
        (await expected.getAttribute("data-value"));
      index++
    ) {
      const previous = await selected.getAttribute("data-value");
      await page.keyboard.press("ArrowDown");
      await expect(selected).not.toHaveAttribute("data-value", previous!);
    }
    await expect(expected).toHaveAttribute("data-selected", "true");
    await page.keyboard.press("Enter");
    await expect(search).toBeHidden();
    await expect(page).toHaveURL(
      (url) => url.pathname.replace(/\/$/, "") === destination,
    );
  }
});
