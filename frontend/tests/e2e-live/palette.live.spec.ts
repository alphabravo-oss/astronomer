import { expect, test } from "@playwright/test";

// One command palette style: sentence-case grouped rows with hotkey hints,
// opened from "/", Ctrl/⌘K and the header search field.
test("command palette opens from the header, / and Ctrl+K and navigates", async ({
  page,
}) => {
  await page.goto("/dashboard");
  await expect(page.getByTestId("app-shell")).toBeVisible();

  await page
    .getByRole("button", { name: "Search pages, clusters and resources" })
    .click();
  const dialog = page.getByRole("dialog", { name: "Command palette" });
  await expect(dialog).toBeVisible();
  // Groups are sentence case (never uppercase) and items keep their case.
  await expect(dialog.getByText("Pages", { exact: true })).toBeVisible();
  await expect(dialog.getByText("Audit log", { exact: true })).toBeVisible();
  await expect(dialog.getByText("AUDIT LOG", { exact: true })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);

  await page.keyboard.press("/");
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");

  await page.keyboard.press("Control+k");
  await expect(dialog).toBeVisible();
  await page
    .getByPlaceholder("Search pages, clusters, resources and actions")
    .fill("audit log");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/dashboard\/audit/);
  await expect(
    page.getByRole("heading", { name: "Audit log", exact: true }),
  ).toBeVisible();
});
