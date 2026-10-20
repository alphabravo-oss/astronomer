import { mkdir } from "node:fs/promises";
import { dirname } from "node:path";
import { expect, test as setup } from "@playwright/test";

import { LIVE_ADMIN_AUTH_STATE, loginViaForm, SESSION_COOKIE } from "./live.helpers";

// Real login against the running server; the HttpOnly session cookie the
// backend issues is persisted as storageState for every dependent spec.
setup("bootstrap admin signs in and persists the real session", async ({ page }) => {
  await loginViaForm(page);
  const session = (await page.context().cookies()).find((c) => c.name === SESSION_COOKIE);
  expect(session?.httpOnly, "backend must issue an HttpOnly session cookie").toBe(true);
  await mkdir(dirname(LIVE_ADMIN_AUTH_STATE), { recursive: true });
  await page.context().storageState({ path: LIVE_ADMIN_AUTH_STATE });
});
