import { test, expect } from "@playwright/test";
test("shipped ESM editor saves YAML and runs JSON diagnostics with same-origin workers", async ({
  page,
}) => {
  const failures: string[] = [];
  const external: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") failures.push(message.text());
  });
  page.on("pageerror", (error) => failures.push(error.message));
  page.on("request", (request) => {
    if (!request.url().startsWith("http://127.0.0.1:"))
      external.push(request.url());
  });
  const response = await page.goto("/?editor");
  expect(response?.headers()["content-security-policy"]).toContain(
    "script-src 'self' 'wasm-unsafe-eval'",
  );
  const input = page
    .getByRole("region", { name: "YAML editor" })
    .locator(".monaco-editor");
  await expect(input).toBeVisible();
  await expect(page.getByLabel("JSON diagnostics")).not.toHaveText("0");
  await page
    .getByRole("region", { name: "YAML editor" })
    .locator(".view-lines")
    .click();
  const selectAll = await page.evaluate(() =>
    /Mac/.test(navigator.userAgent) ? "Meta+a" : "Control+a",
  );
  await page.keyboard.press(selectAll);
  await page.keyboard.type("kind: ConfigMap");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByLabel("Saved YAML")).toHaveText("kind: ConfigMap");
  await expect(page.getByLabel("JSON diagnostics")).not.toHaveText("0");
  await page.getByRole("button", { name: "Load external YAML" }).click();
  await expect(
    page.getByRole("region", { name: "YAML editor" }).locator(".view-lines"),
  ).toContainText("kind: Secret");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByLabel("Saved YAML")).toHaveText("kind: Secret");
  expect(external).toEqual([]);
  expect(failures).toEqual([]);
});
