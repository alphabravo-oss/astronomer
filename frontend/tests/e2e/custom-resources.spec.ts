import { expect, test, type Page } from "@playwright/test";

import { authMeWire, seedAuth } from "./helpers/auth";

// GATE C custom-resource explorer: CRD list -> CR list (virtualized) -> CR
// detail (generic ResourceDetail Overview + YAML). Auth + API are faked via
// cookies + route interception (no backend), mirroring resource-drilldown.spec.

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
const GROUP = "example.com";
const VERSION = "v1";
const PLURAL = "widgets";
const KIND = "Widget";
const CR_NS = "team-a";
const CR_NAME = "widget-000";

function apiResponse<T>(data: T) {
  return { status: 200, data };
}

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
  podCount: 42,
  namespaceCount: 8,
  cpuCapacity: 24,
  cpuUsage: 6,
  cpuPercentage: 25,
  memoryCapacity: 96,
  memoryUsage: 32,
  memoryPercentage: 33,
  labels: {},
  annotations: {},
  agentVersion: "e2e",
  lastHeartbeat: new Date().toISOString(),
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  isLocal: false,
};

// A single CRD in the apiextensions list.
const crdList = {
  apiVersion: "apiextensions.k8s.io/v1",
  kind: "CustomResourceDefinitionList",
  items: [
    {
      metadata: {
        name: `${PLURAL}.${GROUP}`,
        creationTimestamp: "2024-01-01T00:00:00Z",
      },
      spec: {
        group: GROUP,
        scope: "Namespaced",
        names: { kind: KIND, plural: PLURAL },
        versions: [{ name: VERSION, served: true, storage: true }],
      },
    },
  ],
};

// Enough CR instances to exercise virtualization (windows the row model).
const crList = {
  apiVersion: `${GROUP}/${VERSION}`,
  kind: "WidgetList",
  items: Array.from({ length: 200 }, (_, i) => ({
    metadata: {
      name: `widget-${String(i).padStart(3, "0")}`,
      namespace: CR_NS,
      creationTimestamp: "2024-01-01T00:00:00Z",
    },
  })),
};

// The single CR object returned by the k8s proxy GET (raw upstream JSON).
const crObject = {
  apiVersion: `${GROUP}/${VERSION}`,
  kind: KIND,
  metadata: {
    name: CR_NAME,
    namespace: CR_NS,
    uid: "widget-uid-0",
    creationTimestamp: "2024-01-01T00:00:00Z",
    labels: { team: "platform" },
  },
  spec: { size: "large" },
  status: {},
};

async function mockApi(page: Page) {
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
    if (path === `/clusters/${CLUSTER_ID}/namespaces`) {
      return route.fulfill({
        json: {
          data: [
            {
              name: CR_NS,
              cluster_id: CLUSTER_ID,
              status: "Active",
              labels: {},
              annotations: {},
              created_at: "2024-01-01T00:00:00Z",
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
    }
    if (path === "/rbac/my-permissions") {
      return route.fulfill({
        json: apiResponse({
          subject: { user_id: adminUser.id, self: true },
          superuser: true,
          context: {
            cluster_id: CLUSTER_ID,
            namespace_scoped_bindings_supported: true,
            warnings: [],
          },
          bindings: [],
          permissions: [
            {
              resource: "*",
              verb: "*",
              applies_to_context: true,
              inherited: false,
              sources: [{ scope: "global" }],
            },
          ],
        }),
      });
    }
    if (path === `/clusters/${CLUSTER_ID}` && method === "GET") {
      return route.fulfill({ json: apiResponse(cluster) });
    }
    if (path === `/clusters/${CLUSTER_ID}/resources/discovery`) {
      return route.fulfill({
        json: apiResponse({
          cluster_id: CLUSTER_ID,
          resources: [],
          crds: [
            {
              name: `${PLURAL}.${GROUP}`,
              group: GROUP,
              scope: "Namespaced",
              kind: KIND,
              plural: PLURAL,
              versions: [{ name: VERSION, storage: true }],
            },
          ],
          crd_continue: "",
          partial: false,
          errors: {},
        }),
      });
    }
    // CRD list (E1): single proxy GET to the apiextensions endpoint.
    if (
      path ===
      `/clusters/${CLUSTER_ID}/k8s/apis/apiextensions.k8s.io/v1/customresourcedefinitions`
    ) {
      return route.fulfill({ json: crdList });
    }
    // Single CR (detail) — match before the cluster-wide list route.
    if (
      path ===
      `/clusters/${CLUSTER_ID}/k8s/apis/${GROUP}/${VERSION}/namespaces/${CR_NS}/${PLURAL}/${CR_NAME}`
    ) {
      return route.fulfill({ json: crObject });
    }
    // CR instance list (E2): cluster-wide dynamic path.
    if (
      path === `/clusters/${CLUSTER_ID}/k8s/apis/${GROUP}/${VERSION}/${PLURAL}`
    ) {
      const offset = Number(url.searchParams.get("continue") || "0");
      const limit = Number(url.searchParams.get("limit"));
      // The sidebar separately requests one item for its bounded count.
      expect([1, 50]).toContain(limit);
      return route.fulfill({
        json: {
          ...crList,
          items: crList.items.slice(offset, offset + limit),
          metadata: {
            continue:
              offset + limit < crList.items.length
                ? String(offset + limit)
                : "",
            remainingItemCount: Math.max(
              0,
              crList.items.length - offset - limit,
            ),
          },
        },
      });
    }
    // CR events feed (Events tab fieldSelector).
    if (
      path === `/clusters/${CLUSTER_ID}/k8s/api/v1/namespaces/${CR_NS}/events`
    ) {
      return route.fulfill({
        json: { apiVersion: "v1", kind: "EventList", items: [] },
      });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test("custom resources: CRD list -> CR list -> CR detail (Overview + YAML)", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.goto(`/dashboard/clusters/${CLUSTER_ID}/custom-resources`);

  // E1: CRD list renders the widget CRD row.
  await expect(
    page.getByRole("heading", { name: "Custom Resources" }),
  ).toBeVisible();
  const crdLink = page
    .getByRole("region", { name: "Data table" })
    .getByRole("link", { name: KIND, exact: true });
  await expect(crdLink).toBeVisible();

  // Drill into the CR instance list (E2, virtualized).
  await crdLink.click();
  await expect(page).toHaveURL(
    new RegExp(
      `/dashboard/clusters/${CLUSTER_ID}/custom-resources/${GROUP}/${VERSION}/${PLURAL}$`,
    ),
  );
  await expect(page.getByRole("heading", { name: PLURAL })).toBeVisible();

  // First CR row is visible (virtualized grid mounts the top window).
  const crLink = page.getByRole("link", { name: CR_NAME });
  await expect(crLink).toBeVisible();

  // Drill into the CR detail (reuses generic ResourceDetail).
  await crLink.click();
  await expect(page).toHaveURL(
    new RegExp(
      `/custom-resources/${GROUP}/${VERSION}/${PLURAL}/${CR_NS}/${CR_NAME}$`,
    ),
  );

  // Overview: header + Metadata/Labels sections render for the CR.
  await expect(page.getByRole("heading", { name: CR_NAME })).toBeVisible();
  await expect(page.getByText(`Kind: ${KIND}`)).toBeVisible();
  await expect(page.getByText("Metadata")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Labels", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("platform")).toBeVisible();

  // YAML tab renders the panel (View/Edit toggle). Scope to the tab nav — the
  // header also has a "Download YAML" action button named YAML.
  await page.getByRole("tab", { name: "YAML" }).click();
  await expect(page.getByRole("button", { name: "Edit" })).toBeVisible();
});

test("custom resources: server continuation and live YAML actions preserve list navigation", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  const listUrl = `/dashboard/clusters/${CLUSTER_ID}/custom-resources/${GROUP}/${VERSION}/${PLURAL}`;
  await page.goto(listUrl);
  await expect(page.getByRole("link", { name: CR_NAME })).toBeVisible();
  await page.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "widget-050", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: CR_NAME, exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Previous", exact: true }).click();
  await expect(page.getByRole("link", { name: CR_NAME })).toBeVisible();
  const action = page.getByLabel("Open actions menu").first();
  await action.scrollIntoViewIfNeeded();
  await action.click();
  const download = page.waitForEvent("download");
  await page
    .getByRole("menuitem", { name: "Download YAML", exact: true })
    .click();
  expect((await download).suggestedFilename()).toBe(`${CR_NS}-${CR_NAME}.yaml`);
  await expect(page).toHaveURL(new RegExp(`${listUrl}$`));
  await action.click();
  await page.getByRole("menuitem", { name: "Clone", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: `Clone ${CR_NAME}` }),
  ).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`${listUrl}$`));
});
