import { mkdir } from "node:fs/promises";
import { dirname } from "node:path";
import { test as setup, type Page } from "@playwright/test";

import {
  ADMIN_AUTH_STATE,
  adminAuthUser,
  READ_ONLY_AUTH_STATE,
  readOnlyAuthUser,
} from "../e2e/helpers/auth-state";
import { CSRF_COOKIE, SESSION_COOKIE } from "../e2e/helpers/auth";

async function saveRoleState(page: Page, statePath: string, user: unknown) {
  await mkdir(dirname(statePath), { recursive: true });
  await page.context().addCookies([
    {
      name: SESSION_COOKIE,
      value: "e2e-session",
      domain: "127.0.0.1",
      path: "/",
    },
    {
      name: CSRF_COOKIE,
      value: "e2e-csrf",
      domain: "127.0.0.1",
      path: "/",
    },
  ]);
  await page.goto("/healthz");
  await page.evaluate((storedUser) => {
    window.localStorage.setItem(
      "astronomer-auth",
      JSON.stringify({
        state: { user: storedUser, isAuthenticated: true },
        version: 2,
      }),
    );
    window.localStorage.setItem("astronomer-theme", "light");
  }, user);
  await page.context().storageState({ path: statePath });
}

setup("admin role state", async ({ page }) => {
  await saveRoleState(page, ADMIN_AUTH_STATE, adminAuthUser);
});

setup("read-only role state", async ({ page }) => {
  await saveRoleState(page, READ_ONLY_AUTH_STATE, readOnlyAuthUser);
});
