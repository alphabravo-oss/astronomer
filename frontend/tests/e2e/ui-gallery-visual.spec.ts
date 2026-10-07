import { expect, test } from "@playwright/test";

import { seedAuth } from "./helpers/auth";
import { adminStoreUser } from "../e2e-smoke/stub-overrides";
import { collectErrors, filterAllowed, installStubs } from "../e2e-smoke/stubs";

// Stub observations are fixed; wall-clock age labels must be fixed as well.
test.use({ locale: "en-US", timezoneId: "UTC" });
test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date("2026-09-22T12:00:00Z"));
});

// Component gallery (plan 031 phase 8). Desktop only (the mobile and tablet
// projects ignore this file in playwright.config.ts), light/dark x density.
// The route exists only in builds made with VITE_UI_GALLERY=1 (the Playwright
// web server sets it). Overlay triggers are not opened, so frames are static.
for (const theme of ["dark", "light"] as const) {
  for (const density of ["comfortable", "compact"] as const) {
    test(`ui-gallery ${theme} ${density} visual baseline`, async ({
      page,
      context,
    }) => {
      const errors = collectErrors(page);
      await installStubs(page);
      await seedAuth(context, page, adminStoreUser, {
        preferences: { theme, table_density: density },
      });
      await page.addInitScript((selectedTheme) => {
        window.localStorage.setItem("astronomer-theme", selectedTheme);
      }, theme);
      await page.goto("/dashboard/dev/ui");
      await expect(page.getByTestId("app-shell")).toBeVisible();
      await expect(page.getByTestId("gallery-table")).toBeVisible();
      await expect(page.getByText("prod-eu-1").first()).toBeVisible();
      // The shell scrolls inside <main>, so fullPage alone would clip the long
      // gallery. Grow the viewport to the content height instead.
      const contentHeight = await page.evaluate(() => {
        const main = document.getElementById("main");
        return main ? main.scrollHeight + main.offsetTop : 0;
      });
      await page.setViewportSize({ width: 1280, height: contentHeight + 8 });
      await expect(page.getByTestId("gallery-table")).toBeVisible();
      await expect(page).toHaveScreenshot(
        `ui-gallery-${theme}-${density}.png`,
        {
          animations: "disabled",
          fullPage: true,
          maxDiffPixelRatio: 0.002,
        },
      );
      expect(filterAllowed(errors)).toEqual([]);
    });
  }
}
