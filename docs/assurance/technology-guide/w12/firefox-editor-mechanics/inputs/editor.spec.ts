import { expect, test } from "@playwright/test";

const object = {
  apiVersion: "v1",
  kind: "ConfigMap",
  metadata: { name: "live-config", namespace: "default", labels: { "fixture-stage": "applied" } },
  data: { message: "after-live-apply" },
};
const manifest = JSON.stringify(object);

test.beforeEach(async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.locator(".monaco-editor").click();
  const input = page.locator(".monaco-editor textarea.inputarea");
  await expect(input).toBeFocused();
  await input.press("Control+A");
  await input.press("Backspace");
});

test("original Firefox bulk insertion submits one synthetic document", async ({ page }) => {
  const expected = `# live-browser apply fixture\n${manifest}\n`;
  await page.keyboard.insertText(expected);
  await page.getByRole("button", { name: "Dry run", exact: true }).click();
  await expect(page.locator("#submitted")).toHaveText(expected);
  const submitted = await page.locator("#submitted").textContent();
  expect(submitted?.match(/after-live-apply/g)?.length).toBe(1);
});

test("native Firefox key events submit the exact synthetic manifest", async ({ page }) => {
  const input = page.locator(".monaco-editor textarea.inputarea");
  await input.pressSequentially(manifest);
  await expect(page.locator(".view-lines")).toContainText("after-live-apply");
  await page.getByRole("button", { name: "Dry run", exact: true }).click();
  await expect(page.locator("#submitted")).toHaveText(manifest);
  const submitted = await page.locator("#submitted").textContent();
  expect(submitted?.match(/after-live-apply/g)?.length).toBe(1);
  expect(JSON.parse(submitted!)).toEqual(object);
});
