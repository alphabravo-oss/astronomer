import { expect, test, type Page } from "@playwright/test";

// Sidebar model (A1): a global rail of pillars that stays visible, and a
// cluster panel (six groups) that opens beside it when a cluster is selected.
// "All clusters" is a real header breadcrumb back out of the cluster.

async function firstClusterId(page: Page): Promise<string | null> {
  const res = await page.context().request.get("/api/v1/clusters/?page_size=1");
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { data?: Array<{ id: string }> };
  return body.data?.[0]?.id ?? null;
}

test("global rail → cluster panel → back through the breadcrumb", async ({
  page,
}) => {
  await page.goto("/dashboard/clusters");
  await expect(page.getByTestId("app-shell")).toBeVisible();

  // Global context: the rail lists the seven pillars; the panel shows the
  // Clusters pillar's items; no raw counts in the sidebar.
  const nav = page.getByRole("navigation", { name: "Primary" });
  await expect(nav).toBeVisible();
  for (const pillar of [
    "Overview",
    "Clusters",
    "Delivery",
    "Observability",
    "Security",
    "Access",
    "Settings",
  ]) {
    await expect(
      nav
        .getByRole("link", { name: pillar, exact: true })
        .or(nav.getByRole("button", { name: pillar, exact: true }))
        .first(),
    ).toBeVisible();
  }
  await expect(
    nav.getByRole("link", { name: "Onboarding templates" }),
  ).toBeVisible();
  await expect(nav.locator("text=/^\\d+$/")).toHaveCount(0);
  // Depth 1: no breadcrumbs.
  await expect(
    page.getByRole("navigation", { name: "Breadcrumb" }),
  ).toHaveCount(0);

  const clusterId = await firstClusterId(page);
  test.skip(!clusterId, "no cluster registered on this box");

  await page.goto(`/dashboard/clusters/${clusterId}/pods`);
  const clusterNav = page.getByRole("navigation", { name: "Cluster" });
  await expect(clusterNav).toBeVisible();
  // Six groups, the active one (Workloads) expanded.
  for (const group of [
    "Overview",
    "Workloads",
    "Networking",
    "Storage",
    "Policy & access",
    "Manage",
  ]) {
    await expect(
      clusterNav.getByRole("button", { name: group, exact: true }),
    ).toBeVisible();
  }
  await expect(
    clusterNav.getByRole("link", { name: "Pods", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  // The global rail is still there (Clusters pillar active).
  await expect(
    clusterNav.getByRole("button", { name: "Clusters", exact: true }),
  ).toHaveAttribute("data-active", "true");

  // Header: "All clusters › Pods" breadcrumb; one Cluster actions menu.
  const crumbs = page.getByRole("navigation", { name: "Breadcrumb" });
  await expect(
    crumbs.getByRole("link", { name: "All clusters" }),
  ).toBeVisible();
  await expect(crumbs.getByText("Pods")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Cluster actions" }),
  ).toBeVisible();

  await crumbs.getByRole("link", { name: "All clusters" }).click();
  await expect(page).toHaveURL(/\/dashboard\/clusters\/?$/);
  await expect(page.getByRole("navigation", { name: "Primary" })).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Cluster actions" }),
  ).toHaveCount(0);
});

test("footer carries one product line and no Charlie FAB covers content", async ({
  page,
}) => {
  await page.goto("/dashboard");
  await expect(page.getByText(/Astronomer .+ · AlphaBravo/)).toBeVisible();
  await expect(page.locator("button.fixed.bottom-5")).toHaveCount(0);
});
