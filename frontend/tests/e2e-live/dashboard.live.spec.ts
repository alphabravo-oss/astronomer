import { expect, test } from "@playwright/test";

// Every cluster count on the overview derives from one selector over the
// clusters list: the verdict strip, the health board, the agents panel and
// the capacity list must agree with each other and with the API.
test("overview counts agree with each other and with the clusters API", async ({
  page,
  context,
}) => {
  const list = await context.request.get("/api/v1/clusters/?limit=200");
  expect(list.status()).toBe(200);
  const body = (await list.json()) as {
    pagination?: { total?: number };
    data?: Array<{ status: string; pod_count?: number }>;
  };
  const rows = body.data ?? [];
  const total = body.pagination?.total ?? rows.length;
  const byStatus = (status: string) =>
    rows.filter((row) => row.status === status).length;
  const expected = {
    healthy: byStatus("active"),
    disconnected: byStatus("disconnected"),
    registering: byStatus("pending"),
    attention: rows.length - byStatus("active") - byStatus("disconnected") - byStatus("pending"),
  };
  const pods = rows.reduce((sum, row) => sum + (row.pod_count ?? 0), 0);

  await page.goto("/dashboard");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Overview", exact: true })).toBeVisible();

  const verdict = page.getByTestId("fleet-verdict");
  const number = (id: string) =>
    verdict.locator(`[data-verdict-number="${id}"] span`).first();
  await expect(number("clusters")).toHaveText(String(total));
  await expect(number("disconnected")).toHaveText(String(expected.disconnected));
  await expect(number("pods")).toHaveText(pods.toLocaleString("en-US"));

  for (const key of ["healthy", "attention", "disconnected", "registering"] as const) {
    await expect(page.getByTestId(`health-${key}-count`)).toHaveText(String(expected[key]));
    await expect(
      page.getByTestId(`agents-${key}`).locator("[data-value]"),
    ).toHaveText(String(expected[key]));
  }
  // The board accounts for every cluster exactly once.
  expect(Object.values(expected).reduce((a, b) => a + b, 0)).toBe(Math.min(total, 200));

  if (total > 0) {
    await expect(page.getByTestId("capacity-list").locator("li")).toHaveCount(
      Math.min(total, 8),
    );
  }
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
