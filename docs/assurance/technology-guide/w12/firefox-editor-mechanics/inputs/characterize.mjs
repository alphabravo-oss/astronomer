import { firefox } from "../../node_modules/@playwright/test/index.mjs";
import { writeFile } from "node:fs/promises";
import { strict as assert } from "node:assert";

const baseURL = process.argv[2];
const receiptPath = process.argv[3];
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(baseURL) || !receiptPath) {
  throw new Error("expected loopback URL and external receipt path");
}
const manifest = JSON.stringify({ apiVersion: "v1", kind: "ConfigMap", metadata: {
  name: "live-config", namespace: "default", labels: { "fixture-stage": "applied" },
}, data: { message: "after-live-apply" } });
const browser = await firefox.launch();
const results = [];
try {
  for (const method of ["original-bulk-insertion", "native-key-events"]) {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.setDefaultTimeout(10_000);
    try {
      await page.goto(baseURL, { waitUntil: "domcontentloaded", timeout: 20_000 });
      const editor = page.locator(".monaco-editor");
      await editor.waitFor({ state: "visible" });
      await editor.click();
      const input = page.locator(".monaco-editor textarea.inputarea");
      assert(await input.evaluate((element) => element === document.activeElement), "native editor focus");
      await input.press("Control+A");
      await input.press("Backspace");
      if (method === "original-bulk-insertion") {
        await page.keyboard.insertText(`# live-browser apply fixture\n${manifest}\n`);
      } else {
        await input.pressSequentially(manifest);
      }
      await page.getByRole("button", { name: "Dry run", exact: true }).click();
      const submitted = await page.locator("#submitted").textContent();
      const markers = submitted.match(/after-live-apply/g)?.length || 0;
      const expected = method === "original-bulk-insertion" ? `# live-browser apply fixture\n${manifest}\n` : manifest;
      const exact = submitted === expected;
      results.push({ method, focused: true, fixtureMarkerCount: markers, exactSyntheticDocument: exact, submittedLength: submitted.length });
      if (method === "native-key-events") {
        assert.equal(markers, 1, "one synthetic document");
        assert(exact, "native key events must submit the exact manifest");
      }
    } finally {
      await context.close();
    }
  }
} finally {
  await browser.close();
  await writeFile(receiptPath, JSON.stringify({ schemaVersion: 1, scope: "controlled product YamlEditor keyboard only; no transport or authenticated journey qualification", results }, null, 2) + "\n", { mode: 0o600 });
}
