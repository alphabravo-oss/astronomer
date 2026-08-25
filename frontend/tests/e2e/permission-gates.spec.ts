/**
 * TEST-02: mocked permission-gate E2E — reader cannot see destructive controls.
 */
import { test, expect } from "@playwright/test";

import { authMeWire } from "./helpers/auth";

test.describe("permission-gated UI", () => {
  test.beforeEach(async ({ page }) => {
    // Minimal mock of /auth/me as a non-superuser with no write grants.
    await page.route("**/api/v1/**", async (route) => {
      const url = route.request().url();
      if (
        url.includes("/auth/me") ||
        url.endsWith("/me/") ||
        url.includes("/auth/me/")
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            status: 200,
            data: authMeWire({
              id: "u1",
              username: "reader",
              email: "reader@example.com",
              displayName: "Read Only",
              isSuperuser: false,
              enabled: true,
              roles: {
                global: [
                  {
                    roleName: "reader",
                    roleRules: [
                      { resource: "clusters", verbs: ["read", "list"] },
                    ],
                  },
                ],
                cluster: [],
                project: [],
              },
            }),
          }),
        });
        return;
      }
      if (url.includes("/backups")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: [], count: 0 }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: [] }),
      });
    });
  });

  test("global backups URL redirects away from Velero console", async ({
    page,
  }) => {
    await page.goto("/dashboard/backups");
    await expect(page).toHaveURL(/\/dashboard\/settings\/backup/);
    await expect(
      page.getByRole("button", { name: /Add Storage/i }),
    ).toHaveCount(0);
  });
});
