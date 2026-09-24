import { expect, test } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { adminStoreUser } from "../e2e-smoke/stub-overrides";
import { seedAuth } from "./helpers/auth";

test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
for (const status of [403, 404, 503]) {
  test(`CIS detail renders ${status} without an endless loading skeleton`, async ({
    page,
  }) => {
    await page.route("**/api/v1/security/scans/scan-smoke-1/", (route) =>
      route.fulfill({
        status,
        json: { error: { message: "Scan unavailable" } },
      }),
    );
    await page.goto("/dashboard/security/scans/scan-smoke-1");
    await expect(
      page.getByText(
        status === 403
          ? "Permission required"
          : status === 404
            ? "Scan not found"
            : "Failed to load scan",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      page.getByText("Loading CIS scan", { exact: true }),
    ).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Re-run Scan" })).toHaveCount(
      0,
    );
    expect(await page.pageErrors()).toEqual([]);
  });
}
test("CIS re-run failure retains review and sends exactly the reviewed cluster/profile on retry", async ({
  page,
}) => {
  const scan = {
    id: "scan-smoke-1",
    cluster_id: "cluster-225",
    status: "completed",
    scan_type: "cis-1.8",
    findings: [],
  };
  const requests: unknown[] = [];
  await page.route("**/api/v1/security/scans/scan-smoke-1/", (route) =>
    route.fulfill({ json: { data: scan } }),
  );
  await page.route("**/api/v1/security/scans/", (route) => {
    requests.push(route.request().postDataJSON());
    return route.fulfill(
      requests.length === 1
        ? { status: 403, json: { error: { message: "Scan creation denied" } } }
        : { status: 201, json: { data: { ...scan, id: "new-scan" } } },
    );
  });
  await page.goto("/dashboard/security/scans/scan-smoke-1");
  await page.getByRole("button", { name: "Re-run Scan" }).click();
  const review = page.getByRole("dialog", { name: "Re-run CIS scan?" });
  await review.getByRole("button", { name: "Re-run", exact: true }).click();
  await expect.poll(() => requests.length).toBe(1);
  await expect(review).toBeVisible();
  await expect(page).toHaveURL(/scan-smoke-1$/);
  await review.getByRole("button", { name: "Re-run", exact: true }).click();
  await expect(page).toHaveURL(/new-scan$/);
  expect(requests).toEqual(
    Array.from({ length: 2 }, () => ({
      cluster_id: "cluster-225",
      profile: "cis-1.8",
    })),
  );
  expect(await page.pageErrors()).toEqual([]);
});
