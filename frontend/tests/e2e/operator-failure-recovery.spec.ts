import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
test("custom resource returns to canonical collection", async ({
  page,
}, info) => {
  const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  await page.route(
    "**/k8s/apis/cert-manager.io/v1/namespaces/default/certificates/review-cert**",
    (route) =>
      route.fulfill({
        json: {
          apiVersion: "cert-manager.io/v1",
          kind: "Certificate",
          metadata: {
            name: "review-cert",
            namespace: "default",
            uid: "review-cert-uid",
          },
          spec: { secretName: "review-cert-tls" },
          status: { conditions: [] },
        },
      }),
  );
  await page.goto(
    `${base}/custom-resources/cert-manager.io/v1/certificates/default/review-cert`,
  );
  await expect(
    page.getByRole("heading", { name: "review-cert", exact: true }),
  ).toBeVisible();
  const back = page.getByRole("link", { name: "Back", exact: true });
  await expect(back).toHaveAttribute(
    "href",
    `${base}/custom-resources/cert-manager.io/v1/certificates`,
  );
  await back.click();
  await expect(page.getByText(/Unknown resource type/i)).toHaveCount(0);
  await page.screenshot({ path: info.outputPath("custom-back.png") });
});

test("Velero access failure does not advertise installation", async ({
  page,
}, info) => {
  let statusRequests = 0;
  await page.route("**/api/v1/clusters/*/velero-status**", (route) => {
    statusRequests++;
    return route.fulfill({
      status: 403,
      json: {
        error: { code: "FORBIDDEN", message: "Review fixture: access denied" },
      },
    });
  });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/snapshots`);
  await expect(
    page.getByText("Velero is not installed", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page
      .getByRole("main")
      .getByText(/denied|permission/i)
      .first(),
  ).toBeVisible();
  expect(statusRequests).toBeGreaterThan(0);
  await page.screenshot({ path: info.outputPath("velero-failed-read.png") });
});
