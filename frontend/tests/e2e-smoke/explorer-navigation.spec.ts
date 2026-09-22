import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { seedAuth } from "../e2e/helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "./stub-overrides";
import { installStubs } from "./stubs";

test("discovered CRD navigation is reachable and accessible", async ({
  page,
  context,
}, testInfo) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}`);
  const sidebar = page.locator("aside");
  if (testInfo.project.name.includes("mobile")) {
    await page
      .getByRole("button", {
        name: "Open navigation",
        exact: true,
      })
      .click();
  }
  const more = sidebar.getByRole("button", {
    name: "More Resources",
    exact: true,
  });
  await more.click();
  await expect(
    sidebar.getByText("Cert Manager", { exact: true }),
  ).toBeVisible();
  const certificate = sidebar.getByRole("link", {
    name: "Certificate",
    exact: true,
  });
  await expect(certificate).toHaveAttribute(
    "href",
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/custom-resources/cert-manager.io/v1/certificates`,
  );
  await expect(
    sidebar.getByRole("link", { name: "Gateways", exact: true }),
  ).toHaveCount(0);
  let destination = certificate;
  let auditTarget = "aside";
  if (!testInfo.project.name.includes("mobile")) {
    await sidebar
      .getByRole("button", { name: "Collapse sidebar", exact: true })
      .click();
    await sidebar
      .getByRole("button", { name: "More Resources", exact: true })
      .click();
    const flyout = page.getByRole("navigation", {
      name: "More Resources",
      exact: true,
    });
    const discovered = flyout.getByRole("link", {
      name: "Certificate",
      exact: true,
    });
    await expect(discovered).toBeVisible();
    destination = discovered;
    auditTarget = 'nav[aria-label="More Resources"]';
  }
  await page.addStyleTag({
    content:
      "*, *::before, *::after { animation: none !important; transition: none !important; }",
  });
  const audit = await new AxeBuilder({ page })
    .include(auditTarget)
    .withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"])
    .analyze();
  expect(
    audit.violations.filter(
      (violation) =>
        violation.impact === "serious" || violation.impact === "critical",
    ),
  ).toEqual([]);
  await page.screenshot({
    path: testInfo.outputPath("explorer-navigation.png"),
  });
  await destination.click();
  await expect(page).toHaveURL(
    /custom-resources\/cert-manager.io\/v1\/certificates/,
  );
});
