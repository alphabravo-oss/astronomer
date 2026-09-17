import { expect, test, type Page } from "@playwright/test";

import { authMeWire, seedAuth } from "./helpers/auth";

// Confirms the DataTable rewrite (now backed by @tanstack/react-table) end-to-end
// against the real Clusters page: server-owned search/filter/pagination and the
// B2 column-visibility persistence across reload. Auth + API are faked via
// cookies + route interception (no backend), mirroring dashboard-smoke.spec.ts.

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

function apiResponse<T>(data: T) {
  return { status: 200, data };
}
function paginated<T>(data: T[], limit: number, offset: number) {
  return {
    data: data.slice(offset, offset + limit),
    pagination: {
      total: data.length,
      limit,
      offset,
      has_more: offset + limit < data.length,
      next_offset: offset + limit < data.length ? offset + limit : null,
    },
  };
}

// 55 clusters exceed the estate page size of 50 so server pagination engages.
const clusters = Array.from({ length: 55 }, (_, i) => {
  const n = String(i + 1).padStart(2, "0");
  return {
    id: `cluster-${n}`,
    name: `cluster-${n}`,
    display_name: `Cluster ${n}`,
    description: "",
    status: i % 2 === 0 ? "active" : "inactive",
    health: {
      status: "active",
      lastCheck: new Date().toISOString(),
      components: [],
    },
    provider: ["aws", "gcp", "azure"][i % 3],
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
    last_heartbeat: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    is_local: false,
  };
});

async function mockApi(page: Page, clusterRequests: URL[]) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path =
      url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "") || "/";
    const method = route.request().method();

    if (path === "/events/stream")
      return route.fulfill({ status: 204, body: "" });
    if (path === "/auth/me")
      return route.fulfill({ json: apiResponse(authMeWire(adminUser)) });
    if (path === "/settings/features")
      return route.fulfill({ json: apiResponse({}) });
    if (path === "/clusters" && method === "GET") {
      clusterRequests.push(url);
      const provider = url.searchParams.get("provider");
      const search = url.searchParams.get("search")?.toLowerCase();
      const matching = clusters.filter(
        (cluster) =>
          (!provider || cluster.provider === provider) &&
          (!search ||
            cluster.name.toLowerCase().includes(search) ||
            cluster.display_name.toLowerCase().includes(search)),
      );
      const limit = Number(url.searchParams.get("limit") ?? "50");
      const offset = Number(url.searchParams.get("offset") ?? "0");
      return route.fulfill({
        json: paginated(matching, limit, offset),
      });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

const firstBodyRow = (page: Page) => page.locator("tbody tr").first();

test("DataTable: paginates and searches clusters through the server API", async ({
  context,
  page,
}) => {
  const clusterRequests: URL[] = [];
  await mockApi(page, clusterRequests);
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/clusters");

  await expect(page.getByRole("heading", { name: "Clusters" })).toBeVisible();

  await expect(page.getByText("Showing 1-50 of 55")).toBeVisible();
  await expect(firstBodyRow(page)).toContainText("Cluster 01");

  // The server owns the stable estate order; navigating requests the next
  // bounded page instead of sorting or slicing the visible page locally.
  await page.getByRole("button", { name: "Page 2", exact: true }).click();
  await expect(page.getByText("Showing 51-55 of 55")).toBeVisible();
  await expect(firstBodyRow(page)).toContainText("Cluster 51");
  await expect
    .poll(() =>
      clusterRequests.some(
        (url) =>
          url.searchParams.get("limit") === "50" &&
          url.searchParams.get("offset") === "50",
      ),
    )
    .toBe(true);

  await page
    .getByRole("textbox", { name: "Search clusters..." })
    .fill("Cluster 55");
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await expect(firstBodyRow(page)).toContainText("Cluster 55");
  await expect
    .poll(() =>
      clusterRequests.some(
        (url) =>
          url.searchParams.get("search") === "Cluster 55" &&
          url.searchParams.get("offset") === "0",
      ),
    )
    .toBe(true);
});

test("DataTable: server-side Provider filter narrows the estate", async ({
  context,
  page,
}) => {
  const clusterRequests: URL[] = [];
  await mockApi(page, clusterRequests);
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/clusters");
  await expect(page.getByText("Showing 1-50 of 55")).toBeVisible();

  // 55 clusters cycle aws/gcp/azure → 19 are aws. The toolbar filter must be
  // sent to the API; a page-local faceted filter would lie about estate truth.
  await page
    .getByRole("combobox", { name: "Filter clusters by provider" })
    .selectOption("aws");

  await expect(page.locator("tbody tr")).toHaveCount(19);
  await expect(page.getByText("Showing 1-50 of 55")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Provider", exact: true }),
  ).toHaveCount(0);
  await expect
    .poll(() =>
      clusterRequests.some(
        (url) =>
          url.searchParams.get("provider") === "aws" &&
          url.searchParams.get("offset") === "0",
      ),
    )
    .toBe(true);
});

test("DataTable: column-visibility choices persist across reload (B2)", async ({
  context,
  page,
}) => {
  await mockApi(page, []);
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/clusters");

  await expect(
    page.getByRole("columnheader", { name: /provider/i }),
  ).toBeVisible();

  // Hide the Provider column via the Columns dropdown.
  await page.getByRole("button", { name: /columns/i }).click();
  await page.getByRole("checkbox", { name: /provider/i }).click();
  await expect(
    page.getByRole("columnheader", { name: /provider/i }),
  ).toHaveCount(0);

  // Reload — the hidden column must stay hidden (persisted to localStorage).
  await page.reload();
  await expect(page.getByRole("heading", { name: "Clusters" })).toBeVisible();
  await expect(
    page.getByRole("columnheader", { name: /provider/i }),
  ).toHaveCount(0);

  // And the persisted entry is present under the namespaced key.
  const stored = await page.evaluate(() =>
    window.localStorage.getItem("dt:clusters:visibility"),
  );
  expect(stored).toContain("provider");
});
