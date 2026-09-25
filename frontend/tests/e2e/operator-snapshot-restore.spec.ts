import { test, expect } from "@playwright/test";
import {
  clusterId,
  now,
  pageOf,
  workflowAuth,
  jsonRoute,
  errorBody,
} from "./helpers/operator-workflows";
const target = "target-restore-cluster";
const snapshotId = "snapshot-workflow-1";
const restoreId = "restore-workflow-1";
const snapshot = {
  id: snapshotId,
  cluster_id: clusterId,
  velero_name: "checkout-backup",
  phase: "Completed",
  source: "adhoc",
  created_at: now,
  spec: {},
  errors_count: 0,
  warnings_count: 0,
};
const restore = {
  id: restoreId,
  snapshot_id: snapshotId,
  source_cluster_id: clusterId,
  target_cluster_id: target,
  velero_name: "checkout-restore",
  phase: "New",
  created_at: now,
  spec: {},
  errors_count: 0,
  warnings_count: 0,
  last_poll_error: "",
  start_time: null,
  completion_time: null,
};
function cluster(id: string, name: string) {
  return {
    id,
    name,
    display_name: name,
    status: "active",
    provider: "generic",
    environment: "test",
    region: "local",
    distribution: "kubernetes",
    kubernetes_version: "1.31",
    health: { status: "active", components: [] },
    labels: {},
    annotations: {},
    node_count: 1,
    pod_count: 1,
    namespace_count: 1,
    cpu_capacity: 2,
    cpu_usage: 0,
    memory_capacity: 1024,
    memory_usage: 0,
    created_at: now,
    updated_at: now,
  };
}
test.beforeEach(async ({ page, context }) => {
  await workflowAuth(page, context);
  await jsonRoute(
    page,
    "/api/v1/clusters",
    pageOf([
      cluster(clusterId, "Smoke East"),
      cluster(target, "Restore target"),
    ]),
  );
  await jsonRoute(page, `/api/v1/clusters/${target}`, {
    data: cluster(target, "Restore target"),
  });
  for (const id of [clusterId, target]) {
    await jsonRoute(page, `/api/v1/clusters/${id}/velero-status`, {
      data: {
        installed: true,
        namespace: "velero",
        storage_ready: true,
        storage_locations: [
          {
            name: "default",
            default: true,
            provider: "aws",
            phase: "Available",
            bucket: "fixture-bucket",
          },
        ],
      },
    });
    await jsonRoute(page, `/api/v1/clusters/${id}/snapshots`, {
      data: { items: id === clusterId ? [snapshot] : [] },
    });
    await jsonRoute(page, `/api/v1/clusters/${id}/snapshot-schedules`, {
      data: { items: [] },
    });
    await jsonRoute(
      page,
      `/api/v1/clusters/${id}/snapshot-restores`,
      pageOf(id === target ? [restore] : []),
    );
  }
  await jsonRoute(
    page,
    `/api/v1/clusters/${target}/snapshot-restores/${restoreId}`,
    { data: restore },
  );
});
test("cross-cluster restore tracks its own queued receipt through reload", async ({
  page,
}, info) => {
  const writes: { method: string; body: unknown; key: string | undefined }[] =
    [];
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") ===
      `/api/v1/clusters/${clusterId}/snapshots/${snapshotId}/restore`,
    (route) => {
      writes.push({
        method: route.request().method(),
        body: route.request().postDataJSON(),
        key: route.request().headers()["idempotency-key"],
      });
      return route.fulfill({
        status: 202,
        headers: {
          Location: `/api/v1/clusters/${target}/snapshot-restores/${restoreId}/`,
        },
        json: { data: restore },
      });
    },
  );
  const incorrectPolls: string[] = [];
  page.on("request", (request) => {
    if (
      request.method() === "GET" &&
      /\/backups\/restores\//.test(request.url())
    )
      incorrectPolls.push(request.url());
  });
  await page.goto(`/dashboard/clusters/${clusterId}/snapshots`);
  await page.getByRole("button", { name: "Restore", exact: true }).click();
  const dialog = page.getByRole("dialog", {
    name: "Restore from checkout-backup",
  });
  await dialog.getByRole("combobox", { name: "Target cluster" }).click();
  await dialog.getByRole("option", { name: /Restore target/ }).click();
  await dialog.getByText("Advanced", { exact: true }).click();
  await dialog
    .getByLabel("Included namespaces (comma-separated)")
    .fill("payments");
  await dialog
    .getByLabel("Excluded namespaces (comma-separated)")
    .fill("kube-system");
  await dialog.getByRole("button", { name: "Restore", exact: true }).click();
  await expect(page).toHaveURL(
    new RegExp(`/clusters/${target}/snapshots\\?restore=${restoreId}`),
  );
  const receipt = page.getByRole("dialog", {
    name: "Snapshot restore",
    exact: true,
  });
  await expect(receipt.getByText(snapshotId, { exact: true })).toBeVisible();
  await expect(receipt.getByText(clusterId, { exact: true })).toBeVisible();
  await expect(receipt.getByText(target, { exact: true })).toBeVisible();
  await expect(
    receipt.getByText("Not completed", { exact: true }),
  ).toBeVisible();
  expect(writes).toHaveLength(1);
  expect(writes[0].method).toBe("POST");
  expect(writes[0].body).toEqual({
    target_cluster_id: target,
    spec: {
      includedNamespaces: ["payments"],
      excludedNamespaces: ["kube-system"],
      restorePVs: true,
    },
  });
  expect(writes[0].key).toBeTruthy();
  await page.reload();
  await expect(receipt.getByText(restoreId, { exact: true })).toBeVisible();
  expect(incorrectPolls).toEqual([]);
  await page.screenshot({
    path: info.outputPath("restore-source-target-receipt.png"),
    fullPage: true,
    animations: "disabled",
  });
});
test("restore observation error and denied reads do not become completed outcomes", async ({
  page,
}, info) => {
  await jsonRoute(
    page,
    `/api/v1/clusters/${target}/snapshot-restores/${restoreId}`,
    {
      data: {
        ...restore,
        phase: "InProgress",
        last_poll_error: "Velero observation timed out",
        last_poll_at: now,
      },
    },
  );
  await page.goto(
    `/dashboard/clusters/${target}/snapshots?restore=${restoreId}`,
  );
  const dialog = page.getByRole("dialog", {
    name: "Snapshot restore",
    exact: true,
  });
  await expect(
    dialog
      .getByRole("alert")
      .filter({ hasText: "displayed phase may be stale" }),
  ).toBeVisible();
  await expect(
    dialog.getByText("Not completed", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("restore-observation-error.png"),
    fullPage: true,
    animations: "disabled",
  });
  await jsonRoute(
    page,
    `/api/v1/clusters/${target}/snapshot-restores/${restoreId}`,
    errorBody("Restore access denied"),
    403,
  );
  await page.reload();
  await expect(dialog.getByText(/clusters:read/).first()).toBeVisible();
  await expect(dialog.getByText(restoreId, { exact: true })).toHaveCount(0);
});
