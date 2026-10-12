import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

for (const fixture of ["", "?modal"]) {
  test(`automated WCAG checks cover ${fixture ? "save dialog" : "virtual table"}`, async ({
    page,
  }) => {
    await page.goto(`/${fixture}`);
    await expect(
      fixture ? page.getByRole("dialog") : page.getByRole("grid"),
    ).toBeVisible();
    const results = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
      .analyze();
    expect(results.violations).toEqual([]);
    expect(await page.pageErrors()).toEqual([]);
  });
}
