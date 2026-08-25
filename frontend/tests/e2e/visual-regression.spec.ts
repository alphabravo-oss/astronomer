import { expect, test } from "@playwright/test";

import { seedAuth } from "./helpers/auth";
import { adminStoreUser } from "../e2e-smoke/stub-overrides";
import { collectErrors, filterAllowed, installStubs } from "../e2e-smoke/stubs";

const routes = [
  { name: "estate", url: "/dashboard" },
  { name: "cluster-overview", url: "/dashboard/clusters/c-smoke-1" },
  {
    name: "resource-explorer",
    url: "/dashboard/clusters/c-smoke-1/deployments",
  },
  { name: "logging", url: "/dashboard/logging" },
];

for (const theme of ["dark", "light"] as const) {
  for (const route of routes) {
    test(`${route.name} ${theme} visual baseline`, async ({
      page,
      context,
    }) => {
      const errors = collectErrors(page);
      await seedAuth(context, page, adminStoreUser);
      await page.addInitScript((selectedTheme) => {
        window.localStorage.setItem("astronomer-theme", selectedTheme);
      }, theme);
      await installStubs(page);
      await page.goto(route.url);
      await expect(page.getByTestId("app-shell")).toBeVisible();
      await expect(page).toHaveScreenshot(`${route.name}-${theme}.png`, {
        animations: "disabled",
        fullPage: true,
        maxDiffPixelRatio: 0.002,
      });
      expect(filterAllowed(errors)).toEqual([]);
    });
  }
}
