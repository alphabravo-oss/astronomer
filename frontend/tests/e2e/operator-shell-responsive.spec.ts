import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});

test("all header controls fit at the five reviewed widths", async ({
  page,
}, info) => {
  for (const width of [390, 412, 768, 1024, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}`);
    await expect(
      page.getByRole("button", { name: "User menu", exact: true }),
    ).toBeVisible();
    await expect(
      page
        .locator("header")
        .getByRole("button", { name: "Import", exact: true }),
    ).toBeVisible();
    for (const label of ["Go to page", "User menu", "Import"])
      await expect(
        page
          .locator("header")
          .getByRole("button", { name: label, exact: true }),
      ).toBeVisible();
    await expect(
      page.locator("header").getByRole("button", { name: /Notifications/ }),
    ).toBeVisible();
    await expect(
      page.locator("header").getByRole("button", { name: /Namespace scope/ }),
    ).toBeVisible();
    for (const name of ["User menu", "Notifications"]) {
      const trigger = page
        .locator("header")
        .getByRole("button", { name: new RegExp(`^${name}`) });
      await trigger.click();
      const popover = page.locator("[data-header-popover]");
      await expect(popover).toBeVisible();
      const rect = await popover.boundingBox();
      expect(rect!.x).toBeGreaterThanOrEqual(0);
      expect(rect!.x + rect!.width).toBeLessThanOrEqual(width);
      await page.keyboard.press("Escape");
      await expect(trigger).toBeFocused();
    }
    const outside = await page.locator("header button").evaluateAll((buttons) =>
      buttons
        .filter((el) => {
          const r = el.getBoundingClientRect();
          return (
            r.width > 0 && r.height > 0 && (r.x < 0 || r.right > innerWidth)
          );
        })
        .map((el) => el.getAttribute("aria-label") || el.textContent),
    );
    expect(outside).toEqual([]);
    await page.screenshot({ path: info.outputPath(`shell-${width}.png`) });
    if (width < 1024) {
      await expect(page.locator("aside")).toHaveAttribute("inert", "");
      await expect(page.locator("aside")).toHaveAttribute(
        "aria-hidden",
        "true",
      );
      await page
        .getByRole("link", { name: "Skip to main content", exact: true })
        .focus();
      await page.keyboard.press("Tab");
      expect(
        await page.evaluate(() =>
          Boolean(document.activeElement?.closest("aside")),
        ),
      ).toBe(false);
      await page
        .getByRole("button", { name: "Open navigation", exact: true })
        .click();
      await expect(page.locator("aside")).not.toHaveAttribute("inert", "");
      await page.keyboard.press("Escape");
      await expect(
        page.getByRole("button", { name: "Open navigation", exact: true }),
      ).toBeFocused();
    }
  }
});

test("long cluster labels retain viewport-safe context and actions", async ({
  page,
}, info) => {
  const { overrides } = await import("../e2e-smoke/stub-overrides");
  const original = (
    overrides.find(
      (entry) => entry.path === `/api/v1/clusters/${SMOKE_CLUSTER_ID}`,
    )!.body as { data: Record<string, unknown> }
  ).data;
  const name =
    "Production customer services cluster in the western region with a very long descriptive name";
  await page.route(
    new RegExp(`/api/v1/clusters/${SMOKE_CLUSTER_ID}/?(?:\\?.*)?$`),
    (route) =>
      route.fulfill({ json: { data: { ...original, display_name: name } } }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}`);
  await expect(
    page.locator("header").getByRole("button", { name, exact: true }),
  ).toBeVisible();
  await expect(
    page.locator("header").getByRole("button", { name: "Import", exact: true }),
  ).toBeVisible();
  expect(
    await page
      .locator("header")
      .evaluate((header) => header.scrollWidth <= innerWidth),
  ).toBe(true);
  await page.screenshot({ path: info.outputPath("shell-long-name-390.png") });
});
