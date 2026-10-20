import { expect, test } from "@playwright/test";

// Starts unauthenticated on purpose: this journey exercises the real login path.
test.use({ storageState: { cookies: [], origins: [] } });

import {
  CSRF_HEADER,
  SESSION_COOKIE,
  csrfToken,
  loginViaForm,
} from "./live.helpers";

test("real browser refresh rotates HttpOnly credentials and rejects reuse", async ({
  page,
  context,
  browser,
}) => {
  await loginViaForm(page);
  const previous = await context.cookies();
  const previousRefresh = previous.find(
    (cookie) => cookie.name === "astronomer_refresh",
  );
  expect(Boolean(previousRefresh?.httpOnly)).toBe(true);
  const oldCSRF = await csrfToken(context);
  const rotated = await context.request.post("/api/v1/auth/refresh/", {
    headers: { [CSRF_HEADER]: oldCSRF },
  });
  expect(rotated.status()).toBe(200);
  const current = await context.cookies();
  const currentRefresh = current.find(
    (cookie) => cookie.name === "astronomer_refresh",
  );
  const currentAccess = current.find(
    (cookie) => cookie.name === SESSION_COOKIE,
  );
  expect(Boolean(currentRefresh?.httpOnly && currentAccess?.httpOnly)).toBe(
    true,
  );
  // Compare booleans so assertion diagnostics cannot print credentials.
  expect(previousRefresh?.value === currentRefresh?.value).toBe(false);
  expect((await context.request.get("/api/v1/auth/me/")).status()).toBe(200);

  const replay = await browser.newContext({
    storageState: { cookies: previous, origins: [] },
  });
  try {
    const rejected = await replay.request.post(
      new URL("/api/v1/auth/refresh/", page.url()).href,
      {
        headers: { [CSRF_HEADER]: oldCSRF },
      },
    );
    expect(rejected.status()).toBe(401);
    // Reuse revokes the entire family, including the newly rotated access token.
    expect((await context.request.get("/api/v1/auth/me/")).status()).toBe(401);
    // The active page is already redirecting after family revocation. Observe
    // its completed login UI before initiating an independent protected-page
    // navigation, rather than racing two navigations in the same document.
    await expect(page).toHaveURL(/\/auth\/login/);
    await expect(
      page.getByRole("button", { name: "Sign in", exact: true }),
    ).toBeVisible();
    await expect(page.locator("#identifier")).toBeVisible();
    await expect(page.locator("#password")).toBeVisible();
    const protectedPage = await context.newPage();
    try {
      await protectedPage.goto(new URL("/dashboard", page.url()).href, {
        waitUntil: "commit",
      });
      await expect(protectedPage).toHaveURL(/\/auth\/login/);
      await expect(
        protectedPage.getByRole("button", { name: "Sign in", exact: true }),
      ).toBeVisible();
      await expect(protectedPage.locator("#identifier")).toBeVisible();
      await expect(protectedPage.locator("#password")).toBeVisible();
    } finally {
      await protectedPage.close();
    }
  } finally {
    await replay.close();
  }
});
