import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

import { authMeWire, seedAuth } from "./helpers/auth";

// Plan 031 P6: saved views (server-persisted, URL-carried) and keyboard row
// navigation on the Pods table. Auth + API are faked via cookies + route
// interception (no backend); the table-views API is an in-memory fake.

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

const CLUSTER_ID = "cluster-01";

const apiResponse = <T>(data: T) => ({ status: 200, data });

const cluster = {
  id: CLUSTER_ID,
  name: CLUSTER_ID,
  displayName: "Cluster 01",
  description: "",
  status: "active",
  health: {
    status: "active",
    lastCheck: new Date().toISOString(),
    components: [],
  },
  provider: "aws",
  environment: "production",
  region: "us-east-1",
  distribution: "eks",
  kubernetesVersion: "1.30",
  nodeCount: 3,
  podCount: 3,
  namespaceCount: 8,
  labels: {},
  annotations: {},
  agentVersion: "e2e",
  lastHeartbeat: new Date().toISOString(),
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  isLocal: false,
};

const pods = ["web-a", "web-b", "web-c"].map((name, i) => ({
  name,
  namespace: "default",
  clusterId: CLUSTER_ID,
  phase: "Running",
  status: "Running",
  ready: "1/1",
  restarts: i,
  node: `node-${i + 1}`,
  ip: `10.1.2.${i + 3}`,
  containers: [
    {
      name: "app",
      image: "nginx:1.25",
      status: "running",
      ready: true,
      restartCount: i,
      lastState:
        i === 2
          ? {
              terminated: {
                reason: "OOMKilled",
                exitCode: 137,
                finishedAt: new Date().toISOString(),
              },
            }
          : undefined,
    },
  ],
  conditions: [],
  createdAt: new Date().toISOString(),
  age: "5m",
}));

type StoredView = {
  id: string;
  table_key: string;
  name: string;
  state: Record<string, unknown>;
  is_default: boolean;
  created_at: string;
  updated_at: string;
};

async function mockApi(page: Page, views: StoredView[]) {
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
    if (path === `/clusters/${CLUSTER_ID}` && method === "GET")
      return route.fulfill({ json: apiResponse(cluster) });
    if (path === `/clusters/${CLUSTER_ID}/pods` && method === "GET") {
      return route.fulfill({
        json: {
          data: pods,
          pagination: {
            total: pods.length,
            limit: 25,
            offset: 0,
            has_more: false,
            next_offset: null,
            next_cursor: null,
          },
        },
      });
    }
    if (path === "/auth/me/table-views" && method === "GET") {
      const key = url.searchParams.get("table_key");
      return route.fulfill({
        json: apiResponse(views.filter((v) => v.table_key === key)),
      });
    }
    if (path === "/auth/me/table-views" && method === "POST") {
      const body = route.request().postDataJSON() as {
        table_key: string;
        name: string;
        state: Record<string, unknown>;
      };
      const view: StoredView = {
        id: `view-${views.length + 1}`,
        table_key: body.table_key,
        name: body.name,
        state: body.state,
        is_default: false,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      };
      views.push(view);
      return route.fulfill({ status: 201, json: apiResponse(view) });
    }
    const patch = path.match(/^\/auth\/me\/table-views\/([^/]+)$/);
    if (patch && method === "PATCH") {
      const view = views.find((v) => v.id === patch[1]);
      const body = route.request().postDataJSON() as Partial<StoredView>;
      if (!view) return route.fulfill({ status: 404, json: { error: {} } });
      if (body.is_default !== undefined) {
        for (const v of views) v.is_default = false;
        view.is_default = body.is_default;
      }
      if (body.name) view.name = body.name;
      return route.fulfill({ json: apiResponse(view) });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

const podsUrl = `/dashboard/clusters/${CLUSTER_ID}/pods`;

test("saved view: save on Pods, reload, the default view applies", async ({
  context,
  page,
}) => {
  const views: StoredView[] = [];
  await mockApi(page, views);
  await seedAuth(context, page, adminUser);
  await page.goto(podsUrl);
  await expect(page.getByRole("heading", { name: "Pods" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: /Node/ })).toBeVisible();

  // Hide the Node column, then save that as a view and make it the default.
  await page.getByRole("button", { name: "Columns" }).click();
  await page.getByRole("checkbox", { name: "Node" }).uncheck();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("columnheader", { name: /Node/ })).toHaveCount(0);

  await page.getByRole("button", { name: /^Views/ }).click();
  await page.getByLabel("View name").fill("Without node");
  await page.getByRole("button", { name: "Save current view" }).click();
  await expect(
    page.getByRole("button", { name: "Without node", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Set Without node as default" })
    .click();
  await expect.poll(() => views[0]?.is_default).toBe(true);
  expect(views[0].state).toMatchObject({
    hidden: expect.arrayContaining(["node"]),
  });

  // Reload on a pristine browser profile for this table: only the server-side
  // default view can restore the hidden column.
  await page.evaluate(() =>
    window.localStorage.removeItem("dt:explorer:pods:visibility"),
  );
  await page.goto(podsUrl);
  await expect(page.getByRole("heading", { name: "Pods" })).toBeVisible();
  await expect(
    page.getByRole("columnheader", { name: /Status/ }),
  ).toBeVisible();
  await expect(page.getByRole("columnheader", { name: /Node/ })).toHaveCount(0);
});

test("view state is carried in the URL and survives a reload", async ({
  context,
  page,
}) => {
  await mockApi(page, []);
  await seedAuth(context, page, adminUser);
  await page.goto(podsUrl);
  await page.getByRole("textbox", { name: "Search pods..." }).fill("web-b");
  await expect
    .poll(() => new URL(page.url()).searchParams.get("tv-explorer:pods"))
    .toContain("web-b");
  await page.reload();
  await expect(
    page.getByRole("textbox", { name: "Search pods..." }),
  ).toHaveValue("web-b");
});

test("keyboard row navigation: j/k, arrows, x, / and axe clean", async ({
  context,
  page,
}) => {
  await mockApi(page, []);
  await seedAuth(context, page, adminUser);
  await page.goto(podsUrl);
  await expect(page.locator("tbody tr[data-row-index]")).toHaveCount(3);

  await page.getByRole("region", { name: "Data table" }).focus();
  await page.keyboard.press("j");
  await expect(page.locator("tbody tr[data-row-index='0']")).toBeFocused();
  await page.keyboard.press("j");
  await expect(page.locator("tbody tr[data-row-index='1']")).toBeFocused();
  await page.keyboard.press("k");
  await expect(page.locator("tbody tr[data-row-index='0']")).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(page.locator("tbody tr[data-row-index='1']")).toBeFocused();

  await page.keyboard.press("x");
  await expect(
    page.locator("tbody tr[data-row-index='1']").getByRole("checkbox"),
  ).toBeChecked();

  await page.keyboard.press("/");
  await expect(
    page.getByRole("textbox", { name: "Search pods..." }),
  ).toBeFocused();
  // Typing in the search box never triggers row shortcuts.
  await page.keyboard.type("jk");
  await expect(
    page.getByRole("textbox", { name: "Search pods..." }),
  ).toHaveValue("jk");

  const results = await new AxeBuilder({ page })
    .include("main")
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  expect(results.violations).toEqual([]);
});

test("expandable pod rows show container state and last termination", async ({
  context,
  page,
}) => {
  await mockApi(page, []);
  await seedAuth(context, page, adminUser);
  await page.goto(podsUrl);
  await page.getByRole("button", { name: "Expand row default/web-c" }).click();
  const containers = page.getByRole("table", { name: "Containers of web-c" });
  await expect(containers).toContainText("app");
  await expect(containers).toContainText("OOMKilled, exit 137");
});
