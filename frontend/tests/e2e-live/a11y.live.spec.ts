import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// axe pass on the ten main pages (serious + critical must be zero).
const pages: Array<[string, string]> = [
  ["overview", "/dashboard"],
  ["clusters", "/dashboard/clusters"],
  ["register", "/dashboard/clusters/register"],
  ["delivery", "/dashboard/delivery"],
  ["alerting", "/dashboard/alerting"],
  ["audit", "/dashboard/audit"],
  ["access-control", "/dashboard/rbac"],
  ["agents", "/dashboard/agents"],
  ["settings-general", "/dashboard/settings/general"],
  ["settings-platform", "/dashboard/settings/platform"],
];

async function scan(page: Page, path: string) {
  await page.goto(path);
  // First paint of a cold Vite dev server can exceed the default expect
  // timeout; the scan itself is what this spec measures.
  await expect(page.getByTestId("app-shell")).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle").catch(() => undefined);
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "best-practice"])
    // Third-party editor/terminal canvases are out of scope.
    .exclude(".monaco-editor")
    .exclude("[data-wterm]")
    .analyze();
  return results.violations.filter(
    (violation) => violation.impact === "serious" || violation.impact === "critical",
  );
}

for (const [name, path] of pages) {
  test(`axe: ${name} has no serious or critical violations`, async ({ page }) => {
    const serious = await scan(page, path);
    expect(
      serious.map((v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(" ")).slice(0, 3).join(", ")}`),
    ).toEqual([]);
  });
}
