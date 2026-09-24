import { test, expect, type Page, type BrowserContext } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth, authMeWire } from "./helpers/auth";
import { readOnlyAuthUser } from "./helpers/auth-state";
import {
  clusterId,
  now,
  pageOf,
  jsonRoute,
} from "./helpers/operator-workflows";
const projectId = "73e417ca-5e29-45d8-96d8-60895ab2cae4";
const project = {
  id: projectId,
  name: "payments",
  display_name: "Payments delivery",
  cluster_id: clusterId,
  description: "Restricted fixture project",
  namespaces: ["payments"],
  resource_quota: {},
  limit_range: {},
  pod_security_profile: "baseline",
  network_policy_mode: "none",
  resource_quota_cpu_limit: "",
  resource_quota_memory_limit: "",
  resource_quota_pod_count: 0,
  created_by_id: null,
  created_at: now,
  updated_at: now,
};
const source = {
  id: "source-restricted",
  project_id: projectId,
  name: "Payments source",
  type: "git",
  url: "https://example.invalid/payments.git",
  auth_mode: "none",
  credential: { configured: false, key_version: 0, epoch: 0 },
  trust_policy: { allow_unsigned: true },
  status: "ready",
  created_at: now,
  updated_at: now,
};
async function seedRestrictedAuth(
  context: BrowserContext,
  page: Page,
  user: unknown,
) {
  await seedAuth(context, page, user);
  await jsonRoute(page, "/api/v1/auth/me", { data: authMeWire(user) });
}
async function openNavigation(page: Page) {
  const button = page.getByRole("button", {
    name: "Open navigation",
    exact: true,
  });
  if (await button.isVisible()) await button.click();
}
async function closeNavigation(page: Page) {
  const button = page.getByRole("button", {
    name: "Close navigation",
    exact: true,
  });
  if (await button.isVisible()) await button.click();
}
test("project-only Delivery lists remain reachable without granting read-only destinations or mutations", async ({
  page,
  context,
}, info) => {
  await installStubs(page);
  const resources = [
    "delivery_sources",
    "delivery_targets",
    "delivery_rollouts",
    "delivery_deployments",
    "delivery_configuration_templates",
  ];
  await seedRestrictedAuth(context, page, {
    ...readOnlyAuthUser,
    globalRoles: [],
    roles: {
      global: [],
      cluster: [],
      project: [
        {
          projectId,
          roleName: "Project delivery list",
          roleRules: [
            ...resources.map((resource) => ({ resource, verbs: ["list"] })),
            { resource: "delivery_bundles", verbs: ["read"] },
          ],
        },
      ],
    },
  });
  await jsonRoute(page, "/api/v1/projects", pageOf([project]));
  await jsonRoute(page, `/api/v1/projects/${projectId}`, { data: project });
  const deliveryReads: URL[] = [];
  const mutations: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname.startsWith("/api/v1/delivery/")) {
      if (request.method() === "GET") deliveryReads.push(url);
      else mutations.push(`${request.method()} ${url.pathname}`);
    }
  });
  await page.route(
    (url) => url.pathname.replace(/\/$/, "") === "/api/v1/delivery/sources",
    (route) => {
      const status = new URL(route.request().url()).searchParams.get("status");
      return route.fulfill({
        json: pageOf(status === "degraded" ? [] : [source]),
      });
    },
  );
  await page.goto(`/dashboard/delivery?project=${projectId}`);
  await expect(
    page.getByRole("heading", { name: "Delivery overview", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Delivery project", exact: true }),
  ).toContainText("Payments delivery");
  await expect(
    page.getByRole("button", { name: "Delivery project", exact: true }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("main").getByRole("link").filter({ hasText: "Bundles" }),
  ).toHaveCount(0);
  await expect(
    page
      .getByRole("main")
      .getByRole("link")
      .filter({ hasText: "Incompatible clusters" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "Estate", exact: true }),
  ).toHaveCount(0);
  await openNavigation(page);
  const group = page
    .locator("aside")
    .getByRole("button", { name: "Continuous Delivery", exact: true });
  if ((await group.getAttribute("aria-expanded")) === "false")
    await group.click();
  for (const [name, suffix] of [
    ["Estate", ""],
    ["Sources", "/sources"],
    ["Targets", "/targets"],
    ["Rollouts", "/rollouts"],
    ["Deployments", "/deployments"],
    ["Templates", "/configuration-templates"],
    ["Overrides", "/override-sets"],
  ]) {
    await expect(
      page.locator("aside").getByRole("link", { name, exact: true }),
    ).toHaveAttribute(
      "href",
      `/dashboard/delivery${suffix}?project=${projectId}`,
    );
  }
  await expect(
    page.locator("aside").getByRole("link", { name: "Bundles", exact: true }),
  ).toHaveCount(0);
  await page
    .locator("aside")
    .getByRole("link", { name: "Sources", exact: true })
    .click();
  await expect(page).toHaveURL(
    new RegExp(`/delivery/sources\\?project=${projectId}`),
  );
  await expect(page.getByText(source.name, { exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", {
      name: /Add source|Verify Payments source|Delete Payments source/i,
    }),
  ).toHaveCount(0);
  await page.reload();
  await expect(page.getByText(source.name, { exact: true })).toBeVisible();
  await openNavigation(page);
  await expect(page.locator('aside a[aria-current="page"]')).toHaveCount(1);
  await expect(page.locator('aside a[aria-current="page"]')).toHaveAttribute(
    "href",
    `/dashboard/delivery/sources?project=${projectId}`,
  );
  await closeNavigation(page);
  expect(deliveryReads.some((url) => url.pathname.includes("/estate"))).toBe(
    false,
  );
  expect(deliveryReads.some((url) => url.pathname.includes("/bundles"))).toBe(
    false,
  );
  expect(
    deliveryReads
      .filter(
        (url) =>
          url.pathname.endsWith("/sources/") ||
          url.pathname.endsWith("/sources"),
      )
      .every((url) => url.searchParams.get("project_id") === projectId),
  ).toBe(true);
  expect(mutations).toEqual([]);
  await page
    .getByRole("heading", { name: "Sources", exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({
    path: info.outputPath("project-only-sources.png"),
    animations: "disabled",
  });
});
test("cluster reader gets one active inventory link and no secret or write controls", async ({
  page,
  context,
}, info) => {
  await installStubs(page);
  await seedRestrictedAuth(context, page, {
    ...readOnlyAuthUser,
    globalRoles: [],
    roles: {
      global: [],
      project: [],
      cluster: [
        {
          clusterId,
          roleName: "Cluster inventory reader",
          roleRules: ["clusters", "nodes", "pods", "namespaces"].map(
            (resource) => ({ resource, verbs: ["list", "read"] }),
          ),
        },
      ],
    },
  });
  await jsonRoute(
    page,
    `/api/v1/clusters/${clusterId}/nodes`,
    pageOf([
      {
        name: "reader-worker",
        status: "Ready",
        roles: ["worker"],
        createdAt: now,
        conditions: [],
        labels: {},
        addresses: [],
        cpuCapacity: 4,
        memoryCapacity: 8589934592,
        podCount: 1,
        podCapacity: 110,
      },
    ]),
  );
  await jsonRoute(page, `/api/v1/clusters/${clusterId}/nodes/reader-worker`, {
    data: {
      name: "reader-worker",
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
      podCount: 1,
      addresses: [],
      conditions: [],
      taints: [],
      images: [],
      pods: [],
      events: [],
      unschedulable: false,
    },
  });
  await page.goto(`/dashboard/clusters/${clusterId}/nodes`);
  await expect(page.getByText("reader-worker", { exact: true })).toBeVisible();
  await openNavigation(page);
  await expect(page.locator('aside a[aria-current="page"]')).toHaveCount(1);
  await expect(page.locator('aside a[aria-current="page"]')).toHaveAttribute(
    "href",
    `/dashboard/clusters/${clusterId}/nodes`,
  );
  await expect(
    page.locator("aside").getByRole("link", { name: "Secrets", exact: true }),
  ).toHaveCount(0);
  await closeNavigation(page);
  await page.getByText("reader-worker", { exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "reader-worker", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Cordon", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Drain", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: info.outputPath("cluster-reader-nodes.png"),
    animations: "disabled",
  });
});
