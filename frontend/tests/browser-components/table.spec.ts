import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("grid")).toBeVisible();
});

test.afterEach(async ({ page }) => {
  expect(await page.pageErrors()).toEqual([]);
});

test("group headers have logical row indices and keyboard navigation skips them", async ({
  page,
}) => {
  await page.getByPlaceholder("Filter items").fill("Item 000");
  const grid = page.getByRole("grid");
  await expect(grid).toHaveAttribute("aria-rowcount", "14");
  const first = grid.locator('[data-row-index="0"]');
  await expect(first).toHaveAttribute("aria-rowindex", "3");
  await grid.focus();
  await page.keyboard.press("ArrowDown");
  await expect(first).toBeFocused();
  for (let index = 1; index <= 4; index++) {
    await page.keyboard.press("ArrowDown");
    await expect(grid.locator(`[data-row-index="${index}"]`)).toBeFocused();
  }
  const beta = grid.locator('[data-row-index="4"]');
  await expect(beta).toContainText("Item 0001");
  await expect(beta).toHaveAttribute("aria-rowindex", "8");
  await page.keyboard.press("Enter");
  await expect(page.getByLabel("Opened row")).toHaveText("0001");
  const group = grid.getByRole("rowheader", { name: "beta", exact: true });
  expect(
    (await group.boundingBox())!.y + (await group.boundingBox())!.height,
  ).toBeLessThanOrEqual((await beta.boundingBox())!.y + 1);
});

test("nested editable controls retain their native arrow-key behavior", async ({
  page,
}) => {
  const input = page.getByRole("textbox", { name: "Edit 0000", exact: true });
  await input.focus();
  await page.keyboard.press("ArrowDown");
  await expect(input).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(input).toBeFocused();
  await expect(page.getByLabel("Opened row")).toHaveText("None");
});

test("focus recovers after a far virtual row is filtered out", async ({
  page,
}) => {
  const grid = page.getByRole("grid");
  await grid.evaluate((element) => {
    element.scrollTop = element.scrollHeight;
  });
  const last = grid.locator('[data-row-index="1199"]');
  await expect(last).toBeVisible();
  await last.focus();
  await expect(last).toBeFocused();
  await page.getByPlaceholder("Filter items").fill("Item 0000");
  await expect(grid).toHaveAttribute("aria-rowcount", "3");
  await grid.focus();
  await page.keyboard.press("ArrowDown");
  await expect(grid.locator('[data-row-index="0"]')).toBeFocused();
});

test("selection preserves exact IDs across virtual windows, grouping, sorting and filtering", async ({
  page,
}) => {
  const grid = page.getByRole("grid");
  await expect(grid).toHaveAttribute("aria-rowcount", "1204");
  expect(await grid.locator("[data-row-index]").count()).toBeLessThan(60);
  await page
    .getByRole("checkbox", { name: "Select row 0000", exact: true })
    .check();
  await expect(
    page.getByRole("checkbox", { name: "Select row 0003", exact: true }),
  ).toBeDisabled();
  await grid.evaluate((element) => {
    element.scrollTop = element.scrollHeight;
  });
  await page
    .getByRole("checkbox", { name: "Select row 1199", exact: true })
    .check();
  await expect(page.getByLabel("Selected IDs")).toHaveText("0000,1199");
  expect(await grid.locator("[data-row-index]").count()).toBeLessThan(60);
  await page.getByLabel("Group namespaces").uncheck();
  await expect(grid).toHaveAttribute("aria-rowcount", "1201");
  await grid
    .getByRole("columnheader", { name: "Name", exact: true })
    .press("Enter");
  await grid
    .getByRole("columnheader", { name: "Name", exact: true })
    .press("Enter");
  await page.getByPlaceholder("Filter items").fill("Item 0000");
  await expect(grid).toHaveAttribute("aria-rowcount", "2");
  await expect(
    page.getByRole("checkbox", { name: "Select row 0000", exact: true }),
  ).toBeChecked();
  await expect(page.getByLabel("Selected IDs")).toHaveText("0000,1199");
  await expect(page.getByLabel("Opened row")).toHaveText("None");
});
