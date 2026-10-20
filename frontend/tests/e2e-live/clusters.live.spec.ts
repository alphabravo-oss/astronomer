import { expect, test } from "@playwright/test";

// Cluster list through the real API with the persisted real session.
test("cluster list renders from the live API", async ({ page, context }) => {
  const listResponse = page.waitForResponse(
    (res) => res.url().includes("/api/v1/clusters") && res.request().method() === "GET",
  );
  await page.goto("/dashboard/clusters");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Clusters", exact: true })).toBeVisible();
  expect((await listResponse).status()).toBe(200);

  // The same cookie jar authorizes direct API calls: the list contract is live.
  const api = await context.request.get("/api/v1/clusters/");
  expect(api.status()).toBe(200);
  const body = (await api.json()) as { data?: unknown };
  expect(body).toHaveProperty("data");
});
