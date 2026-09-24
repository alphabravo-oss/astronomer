import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
test("workload to pod retains failing status, URL logs state, and browser Back context", async ({
  page,
  context,
}) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  await page.route("**/namespaces/**", (route) => {
    if (new URL(route.request().url()).pathname.endsWith("/namespaces/"))
      return route.fulfill({
        json: {
          data: [
            {
              name: "default",
              clusterId: SMOKE_CLUSTER_ID,
              createdAt: "2026-01-01T00:00:00Z",
            },
          ],
          pagination: {
            limit: 200,
            offset: 0,
            total: 1,
            has_more: false,
            next_offset: null,
          },
        },
      });
    return route.fallback();
  });
  await page.route(
    "**/k8s/apis/apps/v1/namespaces/default/deployments/review-web**",
    (route) =>
      route.fulfill({
        json: {
          apiVersion: "apps/v1",
          kind: "Deployment",
          metadata: { name: "review-web", namespace: "default" },
          spec: {
            replicas: 1,
            selector: { matchLabels: { app: "review" } },
            template: {
              spec: { containers: [{ name: "app", image: "app:v1" }] },
            },
          },
          status: { availableReplicas: 0 },
        },
      }),
  );
  await page.route(
    "**/workloads/deployments/default/review-web/pods/**",
    (route) =>
      route.fulfill({
        json: {
          data: [
            {
              name: "review-pod",
              namespace: "default",
              clusterId: SMOKE_CLUSTER_ID,
              phase: "Running",
              status: "CrashLoopBackOff",
              ready: "0/1",
              restarts: 7,
              containers: [
                { name: "app", image: "app:v1", ready: false, restartCount: 7 },
              ],
              createdAt: "2026-01-01T00:00:00Z",
            },
          ],
        },
      }),
  );
  await page.route(
    "**/k8s/api/v1/namespaces/default/pods/review-pod**",
    (route) =>
      route.fulfill({
        json: {
          apiVersion: "v1",
          kind: "Pod",
          metadata: { name: "review-pod", namespace: "default" },
          spec: { containers: [{ name: "app", image: "app:v1" }] },
          status: {
            phase: "Running",
            containerStatuses: [
              {
                name: "app",
                ready: false,
                restartCount: 7,
                state: { waiting: { reason: "CrashLoopBackOff" } },
              },
            ],
          },
        },
      }),
  );
  const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  await page.goto(
    `${base}/deployments/default/review-web?tab=workload-pods&namespaces=default`,
  );
  await expect(
    page.getByText("CrashLoopBackOff", { exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "review-pod", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "Back to workload", exact: true }),
  ).toHaveAttribute(
    "href",
    `${base}/deployments/default/review-web?namespaces=default&tab=workload-pods`,
  );
  await page.getByRole("tab", { name: "Logs", exact: true }).click();
  await expect(page).toHaveURL(/tab=logs/);
  await page.reload();
  await expect(
    page.getByRole("tab", { name: "Logs", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await page.goBack();
  await expect(page).toHaveURL(
    /deployments\/default\/review-web.*tab=workload-pods/,
  );
  await expect(
    page.getByRole("link", { name: "review-pod", exact: true }),
  ).toBeVisible();
});
