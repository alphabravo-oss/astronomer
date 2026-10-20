import { expect, test } from "@playwright/test";

// The overview's Clusters KPI reads from the same source the Clusters list
// is paged from, so the tile and the list total can never disagree.
test("dashboard Clusters KPI equals the clusters count", async ({ page, context }) => {
  await page.goto("/dashboard");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Overview", exact: true })).toBeVisible();

  const list = await context.request.get("/api/v1/clusters/?page_size=1");
  expect(list.status()).toBe(200);
  const body = (await list.json()) as { pagination?: { total?: number }; data?: unknown[] };
  const total = body.pagination?.total ?? body.data?.length ?? 0;

  const tile = page.getByRole("link", { name: /^Clusters\s/ }).first();
  await expect(tile).toBeVisible();
  await expect(tile).toContainText(String(total));
  // Verdict strip carries the same number.
  await expect(page.getByRole("status").filter({ hasText: "clusters" }).first()).toContainText(String(total));
});

// Settings is two layers: the settings sidebar → the page (no hub cards).
test("settings is two layers deep and the hub redirects to General", async ({ page }) => {
  await page.goto("/dashboard/settings");
  await expect(page).toHaveURL(/\/dashboard\/settings\/general/);
  const settingsNav = page.getByRole("navigation", { name: "Settings" }).last();
  await expect(settingsNav).toBeVisible();
  await expect(settingsNav.getByRole("link", { name: "General" })).toHaveAttribute("aria-current", "page");
  await expect(settingsNav.getByRole("link", { name: "Authentication" })).toBeVisible();
  // No hub card grid repeating the sidebar.
  await expect(page.getByRole("heading", { name: "General", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /Group mappings/ })).toHaveCount(1);
  await settingsNav.getByRole("link", { name: "Authentication" }).click();
  await expect(page).toHaveURL(/\/dashboard\/settings\/auth/);
  await expect(settingsNav.getByRole("link", { name: "Authentication" })).toHaveAttribute("aria-current", "page");
});
