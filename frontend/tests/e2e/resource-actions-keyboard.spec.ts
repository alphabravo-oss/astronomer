import { expect, test, type Page } from "@playwright/test";

import { authMeWire, seedAuth } from "./helpers/auth";

const CLUSTER_ID = "cluster-keyboard";
const NAMESPACE = "platform";
const NAME = "payments";
const OBJECT_PATH = `/clusters/${CLUSTER_ID}/k8s/apis/apps/v1/namespaces/${NAMESPACE}/deployments/${NAME}`;

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
  lastLogin: "2026-08-01T00:00:00Z",
  createdAt: "2026-08-01T00:00:00Z",
};

const deployment = {
  apiVersion: "apps/v1",
  kind: "Deployment",
  metadata: {
    name: NAME,
    namespace: NAMESPACE,
    uid: "deployment-keyboard",
    creationTimestamp: "2026-08-01T00:00:00Z",
    labels: { app: NAME },
  },
  spec: {
    replicas: 2,
    paused: false,
    selector: { matchLabels: { app: NAME } },
    template: {
      metadata: { labels: { app: NAME } },
      spec: { containers: [{ name: NAME, image: "example/payments:v1" }] },
    },
  },
  status: {
    replicas: 2,
    readyReplicas: 2,
    availableReplicas: 2,
  },
};

const workloadRow = {
  name: NAME,
  namespace: NAMESPACE,
  kind: "Deployment",
  clusterId: CLUSTER_ID,
  clusterName: "Keyboard Cluster",
  status: "Running",
  ready: "2/2",
  upToDate: 2,
  available: 2,
  replicas: 2,
  desiredReplicas: 2,
  images: ["example/payments:v1"],
  labels: { app: NAME },
  annotations: {},
  createdAt: "2026-08-01T00:00:00Z",
  age: "22d",
};

interface MutationRecord {
  method: string;
  path: string;
  dryRun: boolean;
}

function apiResponse<T>(data: T) {
  return { status: 200, data };
}

async function mockApi(page: Page, mutations: MutationRecord[]) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path =
      url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "") || "/";
    const method = route.request().method();

    if (path === "/events/stream") {
      return route.fulfill({ status: 204, body: "" });
    }
    if (path === "/auth/me") {
      return route.fulfill({ json: apiResponse(authMeWire(adminUser)) });
    }
    if (path === "/settings/features") {
      return route.fulfill({ json: apiResponse({}) });
    }
    if (path === `/clusters/${CLUSTER_ID}` && method === "GET") {
      return route.fulfill({
        json: apiResponse({
          id: CLUSTER_ID,
          name: CLUSTER_ID,
          displayName: "Keyboard Cluster",
          status: "active",
          health: { status: "active", components: [] },
          labels: {},
          annotations: {},
          isLocal: false,
          createdAt: "2026-08-01T00:00:00Z",
          updatedAt: "2026-08-01T00:00:00Z",
        }),
      });
    }
    if (path === `/clusters/${CLUSTER_ID}/workloads` && method === "GET") {
      return route.fulfill({
        json: {
          data: [workloadRow],
          total: 1,
          page: 1,
          page_size: 20,
          total_pages: 1,
        },
      });
    }
    if (path === `/clusters/${CLUSTER_ID}/resources/schema`) {
      return route.fulfill({
        json: {
          resource: {
            resource_type: "deployments",
            api_base: "apis/apps/v1",
            api_group: "apps",
            api_version: "v1",
            kind: "Deployment",
            plural: "deployments",
            namespaced: true,
            verbs: ["get", "patch", "delete"],
            policy: {
              secret: false,
              privilege_escalating: false,
              destructive_delete: true,
              force_conflict_permission: "workloads:manage",
            },
            source: "builtin",
          },
          schema: {},
          schema_name: "io.k8s.api.apps.v1.Deployment",
          schema_available: true,
          definitions: {},
          definitions_truncated: false,
        },
      });
    }
    if (path.endsWith("/events")) {
      return route.fulfill({
        json: { apiVersion: "v1", kind: "EventList", items: [] },
      });
    }
    if (path.endsWith("/pods")) {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (
      path ===
        `/clusters/${CLUSTER_ID}/k8s/apis/apps/v1/namespaces/${NAMESPACE}/deployments` &&
      method === "POST"
    ) {
      mutations.push({ method, path, dryRun: false });
      return route.fulfill({
        json: {
          ...deployment,
          metadata: { ...deployment.metadata, name: "keyboard-created" },
        },
      });
    }
    if (path === OBJECT_PATH) {
      if (method !== "GET") {
        mutations.push({
          method,
          path,
          dryRun: url.searchParams.get("dryRun") === "All",
        });
      }
      return route.fulfill({ json: deployment });
    }
    if (
      path ===
        `/clusters/${CLUSTER_ID}/workloads/Deployment/${NAMESPACE}/${NAME}/scale` &&
      method === "PATCH"
    ) {
      mutations.push({ method, path, dryRun: false });
      return route.fulfill({
        json: apiResponse({ ...deployment, replicas: 3 }),
      });
    }
    if (
      path ===
        `/clusters/${CLUSTER_ID}/workloads/Deployment/${NAMESPACE}/${NAME}/restart` &&
      method === "POST"
    ) {
      mutations.push({ method, path, dryRun: false });
      return route.fulfill({ json: apiResponse(null) });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

test("keyboard-only resource create, scale, restart, YAML preview/apply, and delete works responsively", async ({
  context,
  page,
}) => {
  const mutations: MutationRecord[] = [];
  await mockApi(page, mutations);
  await seedAuth(context, page, adminUser);
  await page.goto(`/dashboard/clusters/${CLUSTER_ID}/deployments`);

  const create = page.getByRole("button", { name: "Create Deployment" });
  await create.focus();
  await page.keyboard.press("Enter");
  const createDialog = page.getByRole("dialog", { name: "Create Deployment" });
  await expect(createDialog).toBeVisible();
  const guidedTab = createDialog.getByRole("tab", { name: "guided" });
  await guidedTab.focus();
  await page.keyboard.press("ArrowRight");
  const yamlEditorTab = createDialog.getByRole("tab", { name: "yaml" });
  await expect(yamlEditorTab).toBeFocused();
  await expect(yamlEditorTab).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowLeft");
  await expect(guidedTab).toBeFocused();
  await expect(guidedTab).toHaveAttribute("aria-selected", "true");
  const createName = createDialog.getByRole("textbox", {
    name: /^Name Unique DNS-compatible/,
  });
  await createName.focus();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("keyboard-created");
  const createNamespace = createDialog.getByRole("textbox", {
    name: /^Namespace Namespace where/,
  });
  await createNamespace.focus();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type(NAMESPACE);
  const createSubmit = createDialog.getByRole("button", {
    name: "Create",
    exact: true,
  });
  await expect(createSubmit).toBeEnabled();
  await createSubmit.focus();
  await page.keyboard.press("Enter");
  await expect(createDialog).not.toBeVisible();

  const row = page.locator("tbody tr").filter({ hasText: NAME }).first();
  await row.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(
    new RegExp(
      `/dashboard/clusters/${CLUSTER_ID}/deployments/${NAMESPACE}/${NAME}$`,
    ),
  );

  await expect(page.getByRole("heading", { name: NAME })).toBeVisible();
  const viewportWidth = page.viewportSize()?.width ?? 0;
  const actionBar = page.getByRole("button", { name: "Scale", exact: true });
  const actionBox = await actionBar.boundingBox();
  expect(actionBox).not.toBeNull();
  expect(actionBox!.x + actionBox!.width).toBeLessThanOrEqual(viewportWidth);

  await actionBar.focus();
  await page.keyboard.press("Enter");
  const scaleDialog = page.getByRole("dialog", { name: "Scale Workload" });
  await expect(scaleDialog).toBeVisible();
  const replicas = scaleDialog.getByRole("spinbutton", {
    name: "Desired replicas",
  });
  await replicas.focus();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("3");
  const scaleSubmit = scaleDialog.getByRole("button", {
    name: "Scale",
    exact: true,
  });
  await scaleSubmit.focus();
  await page.keyboard.press("Enter");
  await expect(scaleDialog).not.toBeVisible();

  const restart = page.getByRole("button", { name: "Restart", exact: true });
  await restart.focus();
  await page.keyboard.press("Enter");

  const overview = page.getByRole("tab", { name: "Overview" });
  await overview.focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "YAML" })).toHaveAttribute(
    "aria-selected",
    "true",
  );

  const edit = page.getByRole("button", { name: "Edit", exact: true });
  await edit.focus();
  await page.keyboard.press("Enter");
  const dryRun = page.getByRole("button", { name: "Dry run", exact: true });
  await expect(dryRun).toBeEnabled();
  await dryRun.focus();
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("region", { name: "YAML apply preview" }),
  ).toBeVisible();
  const save = page.getByRole("button", { name: "Save", exact: true });
  await expect(save).toBeEnabled();
  await save.focus();
  await page.keyboard.press("Enter");

  const deleteButton = page.getByRole("button", {
    name: "Delete",
    exact: true,
  });
  await deleteButton.focus();
  await page.keyboard.press("Enter");
  const deleteDialog = page.getByRole("dialog", {
    name: "Delete Deployment",
  });
  await expect(deleteDialog).toBeVisible();
  const confirmation = deleteDialog.getByPlaceholder(NAME);
  await expect(confirmation).toBeFocused();
  await page.keyboard.type(NAME);
  const confirmDelete = deleteDialog.getByRole("button", {
    name: "Delete",
    exact: true,
  });
  await confirmDelete.focus();
  await page.keyboard.press("Enter");

  await expect
    .poll(() => mutations.filter((record) => record.method === "PATCH").length)
    .toBeGreaterThanOrEqual(3);
  expect(mutations.some((record) => record.path.endsWith("/scale"))).toBe(true);
  expect(mutations.some((record) => record.path.endsWith("/restart"))).toBe(
    true,
  );
  expect(
    mutations.some(
      (record) =>
        record.method === "POST" && record.path.endsWith("/deployments"),
    ),
  ).toBe(true);
  expect(mutations.some((record) => record.dryRun)).toBe(true);
  expect(
    mutations.some(
      (record) => record.path === OBJECT_PATH && record.method === "DELETE",
    ),
  ).toBe(true);
});
