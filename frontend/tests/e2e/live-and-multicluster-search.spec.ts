import { expect, type Page, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

import { authMeWire, seedAuth } from "./helpers/auth";

// Plan 031 phase 10: (1) a live-updating list changes a row from a stubbed
// stream event WITHOUT a refetch; (2) global search shows grouped
// multi-cluster results from the existing fan-out endpoint.

const adminUser = {
  id: "user-admin",
  username: "admin",
  email: "admin@example.com",
  displayName: "Admin User",
  provider: "local",
  globalRoles: ["admin"],
  isSuperuser: true,
  roles: { global: [], cluster: [], project: [] },
  enabled: true,
  lastLogin: new Date().toISOString(),
  createdAt: new Date().toISOString(),
};

const now = new Date().toISOString();
const cluster = {
  id: "cluster-1",
  name: "prod-east",
  display_name: "Prod East",
  status: "active",
  provider: "aws",
  environment: "production",
  region: "us-east-1",
  distribution: "eks",
  kubernetes_version: "1.30",
  node_count: 3,
  pod_count: 42,
  cpu_percentage: 25,
  memory_percentage: 33,
  labels: {},
  annotations: {},
  agent_version: "e2e",
  last_heartbeat: now,
  created_at: now,
  updated_at: now,
  is_local: false,
};

const searchRow = (
  clusterId: string,
  clusterName: string,
  name: string,
  status: string,
) => ({
  cluster_id: clusterId,
  cluster_name: clusterName,
  clusterId,
  clusterName,
  name,
  namespace: "default",
  status,
  type: "Pod",
  age: "2d",
});

async function mockApi(page: Page, counters: { clusters: number }) {
  await page.addInitScript(() => {
    // Controllable stand-in for the SSE connection so a test can push
    // envelopes (a real fulfilled body would end and trigger the
    // stream-closed catch-up refetch).
    const sources: unknown[] = [];
    class StubEventSource {
      onopen: (() => void) | null = null;
      onmessage: ((ev: { data: string }) => void) | null = null;
      onerror: (() => void) | null = null;
      constructor() {
        sources.push(this);
        setTimeout(() => this.onopen?.(), 0);
      }
      close() {}
    }
    (window as unknown as Record<string, unknown>).EventSource =
      StubEventSource;
    (window as unknown as Record<string, unknown>).__emitLive = (
      frame: unknown,
    ) => {
      const es = sources[sources.length - 1] as StubEventSource;
      es.onmessage?.({ data: JSON.stringify(frame) });
    };
  });
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path =
      url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "") || "/";
    const ok = (data: unknown) =>
      route.fulfill({ json: { status: 200, data } });
    if (path === "/auth/me") return ok(authMeWire(adminUser));
    if (path === "/settings/features") return ok({});
    if (path === "/streams/tickets")
      return ok({ ticket: "e2e-ticket", expires_at: now });
    if (path === "/clusters") {
      counters.clusters += 1;
      return route.fulfill({
        json: {
          data: [cluster],
          pagination: {
            total: 1,
            limit: 100,
            offset: 0,
            has_more: false,
            next_offset: null,
          },
        },
      });
    }
    if (path === "/resources/search") {
      return ok({
        results: [
          searchRow("cluster-1", "prod-east", "api-7d9", "Running"),
          searchRow("cluster-1", "prod-east", "api-8f2", "Pending"),
          searchRow("cluster-2", "staging-west", "api-1a4", "Running"),
        ],
        errors: [
          {
            cluster_id: "cluster-3",
            cluster_name: "edge-south",
            error: "tunnel timeout",
          },
        ],
        clusters_queried: 3,
        clusters_failed: 1,
        type: "pods",
      });
    }
    return ok([]);
  });
}

test("a stubbed stream event updates a list row without a refetch", async ({
  context,
  page,
}) => {
  const counters = { clusters: 0 };
  await mockApi(page, counters);
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/clusters");

  const row = page.getByRole("row", { name: /Prod East/ });
  await expect(row).toBeVisible();
  await expect(row.getByText("25%")).toBeVisible();
  const fetchesBefore = counters.clusters;

  await page.evaluate(() =>
    (window as unknown as { __emitLive: (frame: unknown) => void }).__emitLive({
      id: 1,
      type: "cluster.metrics",
      time: new Date().toISOString(),
      data: {
        cluster_id: "cluster-1",
        cpu_percentage: 81,
        memory_percentage: 64,
        pod_count: 44,
        timestamp: new Date().toISOString(),
      },
    }),
  );

  await expect(row.getByText("81%")).toBeVisible();
  await expect(row.getByText("64%")).toBeVisible();
  // Patched in place by the live merger: no list refetch happened.
  expect(counters.clusters).toBe(fetchesBefore);
});

test("global search groups cross-cluster results with status and failures", async ({
  context,
  page,
}) => {
  await mockApi(page, { clusters: 0 });
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/search?type=pods");

  const groups = page.getByTestId("cluster-result-group");
  await expect(groups).toHaveCount(3);
  await expect(groups.nth(0)).toContainText("edge-south");
  await expect(groups.nth(0)).toContainText("not searched");
  await expect(groups.nth(1)).toContainText("prod-east");
  await expect(groups.nth(1)).toContainText("2 (1 Running, 1 Pending)");
  await expect(groups.nth(2)).toContainText("staging-west");
  await expect(page.getByText("api-1a4")).toBeVisible();

  const results = await new AxeBuilder({ page })
    .include("main")
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  expect(results.violations).toEqual([]);
});
