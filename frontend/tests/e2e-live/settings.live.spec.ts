import { expect, test } from "@playwright/test";

// Platform settings page gated by the admin role (real RBAC decision) and the
// identity endpoint behind it.
test("settings page loads for the bootstrap admin", async ({ page, context }) => {
  await page.goto("/dashboard/settings/general");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.getByRole("heading", { name: "General", exact: true })).toBeVisible();

  const me = await context.request.get("/api/v1/auth/me/");
  expect(me.status()).toBe(200);
  const body = (await me.json()) as { data?: { email?: string; is_superuser?: boolean } };
  expect(body.data?.email).toBe("admin@astronomer.local");
  expect(body.data?.is_superuser).toBe(true);
});
