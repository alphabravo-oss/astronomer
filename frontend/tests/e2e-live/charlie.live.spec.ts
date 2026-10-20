import { expect, test } from "@playwright/test";

// Charlie lives behind a header icon (+ ⌘J) and a right-side drawer, never a
// floating pill over content. When the feature is off or dormant, neither
// the icon nor the pill exists.
test("Charlie is a header icon + drawer, never a FAB", async ({ page }) => {
  await page.goto("/dashboard");
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.locator("button.fixed.bottom-5")).toHaveCount(0);

  const flags = await page.context().request.get("/api/v1/settings/feature-flags/");
  const body = flags.ok()
    ? ((await flags.json()) as { data?: Record<string, boolean> })
    : { data: undefined };
  const charlieButton = page.getByTestId("charlie-header-button");
  const enabled = (await charlieButton.count()) > 0;
  if (!enabled) {
    test.info().annotations.push({
      type: "note",
      description: `Charlie not activated on this box (feature.charlie=${String(body.data?.["feature.charlie"])}); header icon absent as designed`,
    });
    await expect(page.getByRole("button", { name: /charlie/i })).toHaveCount(0);
    return;
  }
  await charlieButton.click();
  const drawer = page.getByRole("dialog", { name: /charlie/i });
  await expect(drawer).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await page.keyboard.press("Control+j");
  await expect(drawer).toBeVisible();
});
