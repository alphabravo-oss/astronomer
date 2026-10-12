import { expect, test } from "@playwright/test";

test("Enter submits once; server error persists, focuses, and clears after a successful retry", async ({
  page,
}) => {
  let submissions = 0;
  await page.route("**/siem-forwarders/**", async (route) => {
    if (route.request().method() !== "POST")
      return route.fulfill({ json: { data: [] } });
    submissions++;
    // Make the pending window observable and exercise repeat Enter.
    await new Promise((resolve) => setTimeout(resolve, 200));
    await route.fulfill(
      submissions === 1
        ? {
            status: 400,
            json: { error: { message: "Endpoint refused connection" } },
          }
        : { json: { data: { id: "forwarder-1" } } },
    );
  });
  await page.goto("/?modal");
  await page.getByLabel("Name", { exact: true }).fill("corp");
  const endpoint = page.getByLabel("Endpoint", { exact: true });
  await endpoint.fill("siem.example:6514");
  await endpoint.press("Enter");
  await endpoint.press("Enter");
  const summary = page.getByRole("alert");
  await expect(summary).toContainText("Endpoint refused connection");
  await expect(summary).toBeFocused();
  expect(submissions).toBe(1);
  await page.getByLabel("Name", { exact: true }).fill("corrected");
  await expect(summary).toBeVisible();
  await endpoint.press("Enter");
  await expect(page.getByText("Saved and closed")).toBeVisible();
  expect(submissions).toBe(2);
  expect(await page.pageErrors()).toEqual([]);
});

test("closing a pending dialog preserves the durable save request", async ({
  page,
}) => {
  let submissions = 0;
  let releaseResponse: () => void = () => {};
  const responseGate = new Promise<void>((resolve) => {
    releaseResponse = resolve;
  });
  await page.route("**/siem-forwarders/**", async (route) => {
    if (route.request().method() !== "POST")
      return route.fulfill({ json: { data: [] } });
    submissions++;
    // Keep the request pending until the real Cancel action has completed.
    // A fixed delay races WebKit animation/actionability and can close on save.
    await responseGate;
    await route.fulfill({ json: { data: { id: "forwarder-1" } } });
  });
  await page.goto("/?modal");
  await page.getByLabel("Name", { exact: true }).fill("corp");
  await page.getByLabel("Endpoint", { exact: true }).fill("siem.example:6514");
  const committed = page.waitForResponse(
    (response) =>
      response.url().includes("siem-forwarders") &&
      response.request().method() === "POST",
  );
  await page.getByLabel("Endpoint", { exact: true }).press("Enter");
  await expect(
    page.getByRole("button", { name: "Create Forwarder", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.getByText("Saved and closed")).toBeVisible();
  releaseResponse();
  expect((await committed).ok()).toBe(true);
  expect(submissions).toBe(1);
  expect(await page.pageErrors()).toEqual([]);
});
