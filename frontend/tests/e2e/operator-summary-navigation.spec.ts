import { test, expect } from "@playwright/test";
import {
  clusterId,
  now,
  pageOf,
  jsonRoute,
  workflowAuth,
} from "./helpers/operator-workflows";
const base = `/dashboard/clusters/${clusterId}`;
const node = {
  name: "summary-worker",
  status: "Ready",
  roles: ["worker"],
  labels: {},
  annotations: {},
  createdAt: now,
  nodeInfo: {},
  kubeletVersion: "v1.31.0",
  cpuCapacity: 4,
  cpuUsage: 1,
  memoryCapacity: 8589934592,
  memoryUsage: 2147483648,
  podCapacity: 110,
  podCount: 4,
  addresses: [],
  conditions: [],
  taints: [],
  images: [],
  pods: [],
  events: [],
  unschedulable: false,
};
test.beforeEach(async ({ page, context }) => {
  await workflowAuth(page, context);
  await jsonRoute(page, `/api/v1/clusters/${clusterId}/nodes`, pageOf([node]));
  await jsonRoute(page, `/api/v1/clusters/${clusterId}/nodes/${node.name}`, {
    data: node,
  });
  await jsonRoute(
    page,
    `/api/v1/clusters/${clusterId}/namespaces`,
    pageOf([
      {
        name: "payments",
        clusterId,
        status: "Active",
        createdAt: now,
        podCount: 4,
        cpuUsage: 1,
        memoryUsage: 2147483648,
      },
    ]),
  );
  await jsonRoute(page, `/api/v1/clusters/${clusterId}/metrics/summary`, {
    data: {
      cpuUsage: 1,
      cpuCapacity: 4,
      cpuPercentage: 25,
      memoryUsage: 2147483648,
      memoryCapacity: 8589934592,
      memoryPercentage: 25,
      nodeCount: 1,
      podCount: 4,
      podCapacity: 110,
    },
  });
});
test("overview summary links open node, pod and metrics inventories by keyboard", async ({
  page,
}, info) => {
  for (const [label, path, heading] of [
    ["Nodes", "nodes", "Nodes"],
    ["Pods", "pods", "Pods"],
    ["CPU Usage", "metrics", "Metrics"],
    ["Memory Usage", "metrics", "Metrics"],
  ]) {
    await page.goto(base);
    const link = page
      .getByRole("main")
      .getByRole("link")
      .filter({ hasText: label })
      .first();
    await expect(link).toHaveAttribute("href", `${base}/${path}`);
    await link.focus();
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(
      new RegExp(`/clusters/${clusterId}/${path}(?:\\?.*)?$`),
    );
    await expect(
      page.getByRole("heading", { name: heading, exact: true }).first(),
    ).toBeVisible();
  }
  await page.screenshot({
    path: info.outputPath("summary-metrics.png"),
    animations: "disabled",
  });
});
test("metrics node and namespace links reach exact node and scoped pods", async ({
  page,
}, info) => {
  await jsonRoute(
    page,
    `/api/v1/clusters/${clusterId}/monitoring/stack/status`,
    {
      data: {
        status: "ready",
        grafanaAvailable: true,
        grafanaProxyPath: `/api/v1/clusters/${clusterId}/observability/grafana/`,
      },
    },
  );
  await page.route(
    `**/api/v1/clusters/${clusterId}/observability/grafana/dashboards`,
    (route) =>
      route.fulfill({
        contentType: "text/html",
        body: "<!doctype html><html><body>Fixture Grafana dashboard</body></html>",
      }),
  );
  await page.goto(`${base}/metrics`);
  await page.getByRole("tab", { name: "Grafana", exact: true }).click();
  await expect(page).toHaveURL(/view=grafana/);
  await expect(page.locator('iframe[title="Cluster Grafana"]')).toHaveAttribute(
    "src",
    `/api/v1/clusters/${clusterId}/observability/grafana/dashboards`,
  );
  await page.reload();
  await expect(
    page.getByRole("tab", { name: "Grafana", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: "Overview", exact: true }).click();
  const nodeLink = page
    .getByRole("main")
    .getByRole("link", { name: node.name, exact: true });
  await expect(nodeLink).toHaveAttribute("href", `${base}/nodes/${node.name}`);
  await nodeLink.click();
  await expect(page).toHaveURL(new RegExp(`/nodes/${node.name}$`));
  await expect(
    page.getByRole("heading", { name: node.name, exact: true }),
  ).toBeVisible();
  await page.goBack();
  const namespaceLink = page
    .getByRole("main")
    .getByRole("link", { name: "payments", exact: true });
  await expect(namespaceLink).toHaveAttribute(
    "href",
    `${base}/pods?namespaces=payments`,
  );
  await namespaceLink.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/pods\\?namespaces=payments`));
  await expect(
    page.getByRole("heading", { name: "Pods", exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(page).toHaveURL(/namespaces=payments/);
  await expect(
    page.getByRole("heading", { name: "Pods", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath("namespace-pods-destination.png"),
    animations: "disabled",
  });
});

test("metrics read denial is recoverable without claiming monitoring is missing", async ({
  page,
}, info) => {
  let denied = true;
  let reads = 0;
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") ===
      `/api/v1/clusters/${clusterId}/metrics`,
    (route) => {
      reads++;
      return route.fulfill(
        denied
          ? {
              status: 403,
              json: { error: { message: "Metrics access denied" } },
            }
          : { json: { data: { available: false } } },
      );
    },
  );
  await page.goto(`${base}/metrics`);
  await expect(
    page.getByText("Metrics history unavailable", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(/Permission to read these metrics was denied/),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Set up monitoring stack", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText(/Live rolling window/)).toHaveCount(0);
  await page
    .getByText("Metrics history unavailable", { exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({ path: info.outputPath("metrics-read-denied.png") });
  denied = false;
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "Set up monitoring stack", exact: true }),
  ).toBeVisible();
  expect(reads).toBeGreaterThanOrEqual(2);
});
