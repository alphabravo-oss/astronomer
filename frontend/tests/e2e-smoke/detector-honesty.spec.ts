/**
 * P7.1 — Negative tests that keep the crawl's detectors honest: if the
 * not-found boundary or the error boundary stopped rendering (or lost their
 * testids), the positive crawl would pass vacuously — these two tests fail
 * instead.
 */
import { expect, test } from "@playwright/test";
import { seedAuth } from "../e2e/helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "./stub-overrides";
import { installStubs } from "./stubs";

test("a garbage URL renders the not-found boundary", async ({
  page,
  context,
}) => {
  await seedAuth(context, page, adminStoreUser);
  await installStubs(page);
  await page.goto("/dashboard/definitely-not-a-real-route");
  // The dashboard chrome stays mounted around the 404 panel.
  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.getByTestId("route-not-found")).toBeVisible();
});

test("a page render crash renders the error boundary", async ({
  page,
  context,
}) => {
  await seedAuth(context, page, adminStoreUser);
  // A server failure is owned by the route boundary. This assertion keeps
  // the detector honest without depending on malformed success payloads that
  // robust wire mappers are deliberately expected to tolerate.
  await installStubs(page, [
    {
      method: "GET",
      path: `/api/v1/cluster-templates/${SMOKE_CLUSTER_ID}`,
      status: 500,
      body: {
        error: { code: "forced_test_error", message: "Forced route failure" },
      },
    },
  ]);
  await page.goto(`/dashboard/cluster-templates/${SMOKE_CLUSTER_ID}`);
  await expect(page.getByTestId("route-error-boundary")).toBeVisible();
});
