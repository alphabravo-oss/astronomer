import { test, expect } from "@playwright/test";
import {
  clusterId,
  now,
  pageOf,
  workflowAuth,
  jsonRoute,
  errorBody,
} from "./helpers/operator-workflows";
const releaseId = "release-workflow-1";
const operationId = "operation-workflow-1";
const savedValues = "replicaCount: 2\n";
const release = {
  id: releaseId,
  cluster_id: clusterId,
  chart_id: "chart-workflow",
  chart_version_id: "version-workflow",
  release_name: "checkout-release",
  namespace: "payments",
  chart_name: "checkout",
  chart_version: "1.2.3",
  display_name: "Checkout",
  source_kind: "app",
  status: "deployed",
  revision: 2,
  created_at: now,
  updated_at: now,
};
const operation = {
  id: operationId,
  operationType: "uninstall",
  status: "pending",
  journalStatus: "pending",
  deliveryPhase: "pending",
  attemptCount: 1,
  events: [],
};
test.beforeEach(async ({ page, context }) => {
  await workflowAuth(page, context);
  await jsonRoute(
    page,
    `/api/v1/clusters/${clusterId}/apps`,
    pageOf([release]),
  );
  await jsonRoute(page, `/api/v1/catalog/installed/${releaseId}`, {
    data: release,
  });
  await jsonRoute(page, `/api/v1/catalog/installed/${releaseId}/values`, {
    data: {
      release_name: release.release_name,
      namespace: release.namespace,
      values_override: savedValues,
    },
  });
  await jsonRoute(page, `/api/v1/catalog/installed/${releaseId}/revisions`, {
    data: {
      revisions: [
        {
          revision: 2,
          status: "deployed",
          description: "Rollout accepted by Flux",
        },
      ],
    },
  });
  await jsonRoute(page, `/api/v1/catalog/operations/${operationId}`, {
    data: operation,
  });
});
test("release metadata, values and history survive direct-link reload", async ({
  page,
}, info) => {
  await jsonRoute(page, `/api/v1/clusters/${clusterId}/apps`, pageOf([]));
  await page.goto(
    `/dashboard/clusters/${clusterId}/apps?section=installed&release=${releaseId}&operation=${operationId}`,
  );
  await expect(
    page.getByRole("heading", { name: "Installed release", exact: true }),
  ).toBeVisible();
  await page.getByText("Saved release values", { exact: true }).click();
  await expect(
    page.getByText("replicaCount: 2", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByText("Rollout accepted by Flux", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Inspect namespace pods" }),
  ).toHaveAttribute(
    "href",
    `/dashboard/clusters/${clusterId}/pods?namespaces=payments`,
  );
  await expect(
    page.getByText(`Operation ${operationId}`, { exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Installed release", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(`Operation ${operationId}`, { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("release-detail-operation.png"),
    fullPage: true,
    animations: "disabled",
  });
});
test("uninstall retains accepted operation and failed observation stays visible", async ({
  page,
}, info) => {
  const mutations: { method: string; key: string | undefined }[] = [];
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") ===
      `/api/v1/catalog/installed/${releaseId}`,
    (route) => {
      if (route.request().method() === "GET")
        return route.fulfill({ json: { data: release } });
      mutations.push({
        method: route.request().method(),
        key: route.request().headers()["idempotency-key"],
      });
      return route.fulfill({ status: 202, json: { data: operation } });
    },
  );
  await page.goto(`/dashboard/clusters/${clusterId}/apps?section=installed`);
  await page.getByRole("button", { name: "Uninstall", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Uninstall release" });
  await dialog.getByRole("textbox").fill(release.release_name);
  await dialog.getByRole("button", { name: "Uninstall", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`operation=${operationId}`));
  await expect(
    page.getByText(`Operation ${operationId}`, { exact: true }),
  ).toBeVisible();
  expect(mutations).toHaveLength(1);
  expect(mutations[0].method).toBe("DELETE");
  expect(mutations[0].key).toBeTruthy();
  await jsonRoute(page, `/api/v1/catalog/operations/${operationId}`, {
    data: {
      ...operation,
      status: "completed",
      journalStatus: "completed",
      deliveryPhase: "unknown",
      deliveryObservationError: "Flux observation unavailable",
    },
  });
  await page.getByRole("button", { name: "Refresh operation" }).click();
  await expect(
    page
      .getByRole("alert")
      .filter({ hasText: "workload outcome is unavailable" }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("alert").filter({ hasText: "Flux observation unavailable" }),
  ).toBeVisible();
  await jsonRoute(
    page,
    `/api/v1/catalog/installed/${releaseId}/values`,
    errorBody("Release values denied"),
    403,
  );
  await page.goto(
    `/dashboard/clusters/${clusterId}/apps?section=installed&release=${releaseId}`,
  );
  await expect(
    page.getByText("Saved release values", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page
      .getByRole("main")
      .getByText(/catalog:read/)
      .first(),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("release-denied-values.png"),
    fullPage: true,
    animations: "disabled",
  });
});

test("tool-owned release pivots to Tools without loading catalog mutation diagnostics", async ({
  page,
}, info) => {
  await jsonRoute(page, `/api/v1/catalog/installed/${releaseId}`, {
    data: { ...release, source_kind: "tool", tool_slug: "velero" },
  });
  const diagnosticReads: string[] = [];
  page.on("request", (request) => {
    if (
      new RegExp(`/catalog/installed/${releaseId}/(values|revisions)`).test(
        request.url(),
      )
    )
      diagnosticReads.push(request.url());
  });
  await page.goto(
    `/dashboard/clusters/${clusterId}/apps?section=installed&release=${releaseId}`,
  );
  await expect(
    page.getByRole("link", { name: "Manage in Tools", exact: true }),
  ).toHaveAttribute("href", `/dashboard/clusters/${clusterId}/tools`);
  expect(diagnosticReads).toEqual([]);
  await expect(
    page.getByText("Saved release values", { exact: true }),
  ).toHaveCount(0);
  await page.screenshot({
    path: info.outputPath("tool-release-owner.png"),
    fullPage: true,
    animations: "disabled",
  });
});

test("upgrade keeps saved values and follows the wrapped operation receipt", async ({
  page,
}, info) => {
  const projectId = "e674c0e2-16d9-47a0-b804-725cfbfdd4ab";
  const project = {
    id: projectId,
    name: "Payments",
    display_name: "Payments",
    cluster_id: clusterId,
    cluster_ids: [clusterId],
    namespaces: ["payments"],
    resource_quota: {},
    created_at: now,
    updated_at: now,
  };
  await jsonRoute(page, `/api/v1/projects/${projectId}`, { data: project });
  await jsonRoute(page, "/api/v1/projects", pageOf([project]));
  await jsonRoute(
    page,
    `/api/v1/catalog/charts/${release.chart_id}/versions`,
    pageOf([
      {
        id: release.chart_version_id,
        chart_id: release.chart_id,
        version: "1.2.3",
        app_version: "1.2.3",
        default_values: "replicaCount: 1\n",
        created_at: now,
      },
    ]),
  );
  await jsonRoute(page, `/api/v1/catalog/charts/${release.chart_id}/values`, {
    chart: "checkout",
    version: "1.2.3",
    default_values: "replicaCount: 1\n",
  });
  const writes: { method: string; body: unknown; key: string | undefined }[] =
    [];
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") ===
      `/api/v1/catalog/installed/${releaseId}/upgrade`,
    (route) => {
      writes.push({
        method: route.request().method(),
        body: route.request().postDataJSON(),
        key: route.request().headers()["idempotency-key"],
      });
      return route.fulfill({
        status: 202,
        json: {
          data: {
            installation: release,
            operation: { ...operation, operationType: "upgrade" },
          },
        },
      });
    },
  );
  await page.goto(
    `/dashboard/clusters/${clusterId}/apps?section=installed&project=${projectId}`,
  );
  await page.getByRole("button", { name: "Upgrade", exact: true }).click();
  const dialog = page.getByRole("dialog", {
    name: "Upgrade checkout",
    exact: true,
  });
  await expect(dialog.locator("textarea")).toHaveValue(savedValues);
  await dialog.getByRole("button", { name: "Upgrade", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`operation=${operationId}`));
  expect(writes).toHaveLength(1);
  expect(writes[0].method).toBe("PUT");
  expect(writes[0].body).toEqual({
    chart_version_id: release.chart_version_id,
    values_override: savedValues,
  });
  expect(writes[0].key).toBeTruthy();
  await expect(
    page.getByText(`Operation ${operationId}`, { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("upgrade-wrapped-receipt.png"),
    fullPage: true,
    animations: "disabled",
  });
});
