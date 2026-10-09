# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: table.spec.ts >> selection preserves exact IDs across virtual windows, grouping, sorting and filtering
- Location: tests/browser-components/table.spec.ts:68:1

# Error details

```
Error: expect(received).toEqual(expected) // deep equality

- Expected  - 1
+ Received  + 3

- Array []
+ Array [
+   [ResizeObserver loop completed with undelivered notifications.],
+ ]
```

# Page snapshot

```yaml
- main [ref=e3]:
  - heading "Virtualized grouped table" [level=1] [ref=e4]
  - generic [ref=e5]:
    - checkbox "Group namespaces" [ref=e6]
    - text: Group namespaces
  - status "Opened row" [ref=e7]: None
  - generic [ref=e8]:
    - generic [ref=e9]:
      - generic [ref=e11]:
        - textbox "Filter items" [active] [ref=e15]: Item 0000
        - button "Clear search" [ref=e16]
      - button "Columns" [ref=e22]
    - generic [ref=e24]:
      - generic [ref=e25]: 2 rows selected
      - status "Selected IDs" [ref=e27]: 0000,1199
    - grid [ref=e29]:
      - row [ref=e30]:
        - columnheader [ref=e31]:
          - checkbox "Select all rows on this page" [checked] [ref=e33] [cursor=pointer]
        - columnheader "Name" [ref=e34] [cursor=pointer]
        - columnheader "Namespace" [ref=e39] [cursor=pointer]
        - columnheader "Control" [ref=e45]
      - row [selected] [ref=e49] [cursor=pointer]:
        - gridcell [ref=e50]:
          - checkbox "Select row 0000" [checked] [ref=e52]
        - gridcell "Item 0000" [ref=e53]
        - gridcell "alpha" [ref=e55]
        - gridcell [ref=e57]:
          - textbox "Edit 0000" [ref=e59]: "0000"
```

# Test source

```ts
  1   | import { expect, test } from "@playwright/test";
  2   | 
  3   | test.beforeEach(async ({ page }) => {
  4   |   await page.goto("/");
  5   |   await expect(page.getByRole("grid")).toBeVisible();
  6   | });
  7   | 
  8   | test.afterEach(async ({ page }) => {
> 9   |   expect(await page.pageErrors()).toEqual([]);
      |                                   ^ Error: expect(received).toEqual(expected) // deep equality
  10  | });
  11  | 
  12  | test("group headers have logical row indices and keyboard navigation skips them", async ({
  13  |   page,
  14  | }) => {
  15  |   await page.getByPlaceholder("Filter items").fill("Item 000");
  16  |   const grid = page.getByRole("grid");
  17  |   await expect(grid).toHaveAttribute("aria-rowcount", "14");
  18  |   const first = grid.locator('[data-row-index="0"]');
  19  |   await expect(first).toHaveAttribute("aria-rowindex", "3");
  20  |   await grid.focus();
  21  |   await page.keyboard.press("ArrowDown");
  22  |   await expect(first).toBeFocused();
  23  |   for (let index = 1; index <= 4; index++) {
  24  |     await page.keyboard.press("ArrowDown");
  25  |     await expect(grid.locator(`[data-row-index="${index}"]`)).toBeFocused();
  26  |   }
  27  |   const beta = grid.locator('[data-row-index="4"]');
  28  |   await expect(beta).toContainText("Item 0001");
  29  |   await expect(beta).toHaveAttribute("aria-rowindex", "8");
  30  |   await page.keyboard.press("Enter");
  31  |   await expect(page.getByLabel("Opened row")).toHaveText("0001");
  32  |   const group = grid.getByRole("rowheader", { name: "beta", exact: true });
  33  |   expect(
  34  |     (await group.boundingBox())!.y + (await group.boundingBox())!.height,
  35  |   ).toBeLessThanOrEqual((await beta.boundingBox())!.y + 1);
  36  | });
  37  | 
  38  | test("nested editable controls retain their native arrow-key behavior", async ({
  39  |   page,
  40  | }) => {
  41  |   const input = page.getByRole("textbox", { name: "Edit 0000", exact: true });
  42  |   await input.focus();
  43  |   await page.keyboard.press("ArrowDown");
  44  |   await expect(input).toBeFocused();
  45  |   await page.keyboard.press("ArrowUp");
  46  |   await expect(input).toBeFocused();
  47  |   await expect(page.getByLabel("Opened row")).toHaveText("None");
  48  | });
  49  | 
  50  | test("focus recovers after a far virtual row is filtered out", async ({
  51  |   page,
  52  | }) => {
  53  |   const grid = page.getByRole("grid");
  54  |   await grid.evaluate((element) => {
  55  |     element.scrollTop = element.scrollHeight;
  56  |   });
  57  |   const last = grid.locator('[data-row-index="1199"]');
  58  |   await expect(last).toBeVisible();
  59  |   await last.focus();
  60  |   await expect(last).toBeFocused();
  61  |   await page.getByPlaceholder("Filter items").fill("Item 0000");
  62  |   await expect(grid).toHaveAttribute("aria-rowcount", "3");
  63  |   await grid.focus();
  64  |   await page.keyboard.press("ArrowDown");
  65  |   await expect(grid.locator('[data-row-index="0"]')).toBeFocused();
  66  | });
  67  | 
  68  | test("selection preserves exact IDs across virtual windows, grouping, sorting and filtering", async ({
  69  |   page,
  70  | }) => {
  71  |   const grid = page.getByRole("grid");
  72  |   await expect(grid).toHaveAttribute("aria-rowcount", "1204");
  73  |   expect(await grid.locator("[data-row-index]").count()).toBeLessThan(60);
  74  |   await page
  75  |     .getByRole("checkbox", { name: "Select row 0000", exact: true })
  76  |     .check();
  77  |   await expect(
  78  |     page.getByRole("checkbox", { name: "Select row 0003", exact: true }),
  79  |   ).toBeDisabled();
  80  |   await grid.evaluate((element) => {
  81  |     element.scrollTop = element.scrollHeight;
  82  |   });
  83  |   await page
  84  |     .getByRole("checkbox", { name: "Select row 1199", exact: true })
  85  |     .check();
  86  |   await expect(page.getByLabel("Selected IDs")).toHaveText("0000,1199");
  87  |   expect(await grid.locator("[data-row-index]").count()).toBeLessThan(60);
  88  |   await page.getByLabel("Group namespaces").uncheck();
  89  |   await expect(grid).toHaveAttribute("aria-rowcount", "1201");
  90  |   await grid
  91  |     .getByRole("columnheader", { name: "Name", exact: true })
  92  |     .press("Enter");
  93  |   await grid
  94  |     .getByRole("columnheader", { name: "Name", exact: true })
  95  |     .press("Enter");
  96  |   await page.getByPlaceholder("Filter items").fill("Item 0000");
  97  |   await expect(grid).toHaveAttribute("aria-rowcount", "2");
  98  |   await expect(
  99  |     page.getByRole("checkbox", { name: "Select row 0000", exact: true }),
  100 |   ).toBeChecked();
  101 |   await expect(page.getByLabel("Selected IDs")).toHaveText("0000,1199");
  102 |   await expect(page.getByLabel("Opened row")).toHaveText("None");
  103 | });
  104 | 
```