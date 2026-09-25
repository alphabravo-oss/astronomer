import { expect, test, type Page } from "@playwright/test";

import { authMeWire, seedAuth } from "./helpers/auth";

const CLUSTER_ID = "cluster-1";
const CLUSTER_ID_2 = "cluster-2";
const USER_ID = "1fa85f64-5717-4562-b3fc-2c963f66afa6";
const ROLE_ID = "2fa85f64-5717-4562-b3fc-2c963f66afa6";
const SNAPSHOT_ID = "3fa85f64-5717-4562-b3fc-2c963f66afa6";

const restoreWire = {
  id: "restore-keyboard",
  snapshot_id: SNAPSHOT_ID,
  source_cluster_id: CLUSTER_ID,
  target_cluster_id: CLUSTER_ID,
  velero_name: "restore-nightly-prod",
  phase: "New",
  spec: { restorePVs: true },
  created_at: "2026-08-23T00:00:00Z",
  errors_count: 0,
  warnings_count: 0,
  last_poll_error: "",
  start_time: null,
  completion_time: null,
};

const adminUser = {
  id: USER_ID,
  username: "admin",
  email: "admin@example.com",
  displayName: "Admin User",
  provider: "local",
  globalRoles: ["admin"],
  isSuperuser: true,
  is_superuser: true,
  roles: { global: [], cluster: [], project: [] },
  enabled: true,
  lastLogin: "2026-08-23T00:00:00Z",
  createdAt: "2026-08-01T00:00:00Z",
};

const clusterWire = {
  id: CLUSTER_ID,
  name: "prod-eks",
  display_name: "Production EKS",
  description: "Production cluster",
  status: "active",
  environment: "production",
  provider: "aws",
  distribution: "eks",
  labels: {},
  annotations: {},
  is_local: false,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
};

const clusterWire2 = {
  ...clusterWire,
  id: CLUSTER_ID_2,
  name: "staging-eks",
  display_name: "Staging EKS",
  environment: "staging",
};

const NODE_NAME = "ip-10-0-1-23";

const nodeDetailWire = {
  name: NODE_NAME,
  status: "Ready",
  roles: ["worker"],
  labels: {},
  annotations: {},
  createdAt: "2026-08-01T00:00:00Z",
  nodeInfo: {
    machineID: "m-1",
    systemUUID: "u-1",
    bootID: "b-1",
    kernelVersion: "6.1.0",
    osImage: "Ubuntu 22.04",
    containerRuntimeVersion: "containerd://1.7.0",
    kubeletVersion: "v1.29.0",
    kubeProxyVersion: "v1.29.0",
    operatingSystem: "linux",
    architecture: "amd64",
  },
  cpuCapacity: 4,
  cpuUsage: 1,
  memoryCapacity: 16_000_000_000,
  memoryUsage: 4_000_000_000,
  podCapacity: 110,
  podCount: 3,
  addresses: [],
  conditions: [],
  taints: [],
  images: [],
  pods: [],
  events: [],
  unschedulable: false,
};

const cisScanWire = {
  id: "scan-keyboard",
  cluster_id: CLUSTER_ID,
  scan_type: "eks-cis-1.5",
  status: "pending",
  passed: 0,
  failed: 0,
  warned: 0,
  skipped: 0,
  findings: [],
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
};

interface MutationRecord {
  path: string;
  body: unknown;
}

const mutationStores = new WeakMap<Page, MutationRecord[]>();

function data<T>(value: T) {
  return { data: value };
}

function page<T>(rows: T[]) {
  return {
    data: rows,
    pagination: {
      total: rows.length,
      limit: 200,
      offset: 0,
      has_more: false,
      next_offset: null,
    },
  };
}

async function mockApi(pageContext: Page, mutations: MutationRecord[]) {
  await pageContext.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path =
      url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "") || "/";
    const method = request.method();
    const record = () => {
      mutations.push({
        path,
        body: request.postData() ? request.postDataJSON() : undefined,
      });
    };

    if (path === "/events/stream") {
      return route.fulfill({ status: 204, body: "" });
    }
    if (path === "/auth/me") {
      return route.fulfill({ json: data(authMeWire(adminUser)) });
    }
    if (path === "/settings/features") {
      return route.fulfill({
        json: data({ "feature.security": true, "feature.backups": true }),
      });
    }
    if (path === "/clusters" && method === "GET") {
      return route.fulfill({ json: page([clusterWire]) });
    }
    if (path === `/clusters/${CLUSTER_ID}` && method === "GET") {
      return route.fulfill({ json: data(clusterWire) });
    }
    if (
      path === `/clusters/${CLUSTER_ID}/nodes/${NODE_NAME}` &&
      method === "GET"
    ) {
      return route.fulfill({ json: data(nodeDetailWire) });
    }
    if (path === `/clusters/${CLUSTER_ID_2}` && method === "GET") {
      return route.fulfill({ json: data(clusterWire2) });
    }
    if (path === "/projects" && method === "GET") {
      return route.fulfill({ json: page([]) });
    }
    if (path === "/users" && method === "GET") {
      return route.fulfill({
        json: page([
          {
            id: USER_ID,
            username: "admin",
            email: "admin@example.com",
            displayName: "Admin User",
            provider: "local",
            globalRoles: ["admin"],
            is_superuser: true,
            enabled: true,
            lastLogin: "2026-08-23T00:00:00Z",
            createdAt: "2026-08-01T00:00:00Z",
          },
        ]),
      });
    }
    if (path === "/rbac/principals" && method === "GET") {
      return route.fulfill({
        json: data({
          principals: [
            {
              kind: "local",
              user_id: USER_ID,
              display_name: "Admin User",
              email: "admin@example.com",
            },
          ],
          connectors: [],
        }),
      });
    }

    if (path === "/rbac/global-roles" && method === "GET") {
      return route.fulfill({ json: page([]) });
    }
    if (path === "/rbac/project-roles" && method === "GET") {
      return route.fulfill({ json: page([]) });
    }
    if (path === "/rbac/cluster-roles" && method === "GET") {
      return route.fulfill({
        json: page([
          {
            id: ROLE_ID,
            name: "cluster-viewer",
            display_name: "Cluster Viewer",
            description: "Read cluster resources",
            is_builtin: true,
            rules: [{ resource: "clusters", verbs: ["read"] }],
            created_at: "2026-08-01T00:00:00Z",
          },
        ]),
      });
    }
    if (
      [
        "/rbac/global-role-bindings",
        "/rbac/project-role-bindings",
        "/rbac/cluster-role-bindings",
      ].includes(path) &&
      method === "GET"
    ) {
      return route.fulfill({ json: page([]) });
    }
    if (path === "/rbac/cluster-role-bindings" && method === "POST") {
      record();
      return route.fulfill({
        status: 201,
        json: data({
          id: "binding-keyboard",
          user_id: USER_ID,
          role_id: ROLE_ID,
          cluster_id: CLUSTER_ID,
          namespace: "",
          created_at: "2026-08-23T00:00:00Z",
        }),
      });
    }

    if (path === "/security/profiles" && method === "GET") {
      return route.fulfill({
        json: data({
          source: "operator",
          items: [
            {
              name: "eks-cis-1.5",
              benchmarkVersion: "1.5",
            },
          ],
        }),
      });
    }
    if (path === "/security/scans" && method === "POST") {
      record();
      return route.fulfill({ status: 202, json: data(cisScanWire) });
    }
    if (path === "/security/scans/scan-keyboard" && method === "GET") {
      return route.fulfill({ json: data(cisScanWire) });
    }

    if (path === `/clusters/${CLUSTER_ID}/velero-status`) {
      return route.fulfill({
        json: data({
          installed: true,
          namespace: "velero",
          storage_ready: true,
          storage_locations: [
            {
              name: "default",
              provider: "aws",
              default: true,
              phase: "Available",
              bucket: "cluster-backups",
            },
          ],
        }),
      });
    }
    if (
      path === `/clusters/${CLUSTER_ID}/snapshot-restores` &&
      method === "GET"
    ) {
      return route.fulfill({
        json: page(
          mutations.some((row) => row.path.endsWith("/restore"))
            ? [restoreWire]
            : [],
        ),
      });
    }
    if (
      path === `/clusters/${CLUSTER_ID}/snapshot-restores/restore-keyboard` &&
      method === "GET"
    ) {
      return route.fulfill({ json: data(restoreWire) });
    }
    if (path === `/clusters/${CLUSTER_ID}/snapshots` && method === "GET") {
      return route.fulfill({
        json: data({
          items: [
            {
              id: SNAPSHOT_ID,
              velero_name: "nightly-prod",
              source: "schedule",
              schedule_name: "nightly",
              phase: "Completed",
              spec: { includedNamespaces: ["payments"] },
              start_time: "2026-08-22T23:00:00Z",
              completion_time: "2026-08-22T23:02:00Z",
              warnings_count: 0,
              errors_count: 0,
              created_at: "2026-08-22T23:00:00Z",
            },
          ],
        }),
      });
    }
    if (
      path === `/clusters/${CLUSTER_ID}/snapshot-schedules` &&
      method === "GET"
    ) {
      return route.fulfill({ json: data({ items: [] }) });
    }
    if (
      path === `/clusters/${CLUSTER_ID}/snapshots/${SNAPSHOT_ID}/restore` &&
      method === "POST"
    ) {
      record();
      return route.fulfill({
        status: 202,
        json: data(restoreWire),
      });
    }

    return route.fulfill({ json: data([]) });
  });
}

test.beforeEach(async ({ context, page }) => {
  const mutations: MutationRecord[] = [];
  await mockApi(page, mutations);
  await seedAuth(context, page, adminUser);
  mutationStores.set(page, mutations);
});

function mutationStore(pageContext: Page): MutationRecord[] {
  return mutationStores.get(pageContext) ?? [];
}

test("SSO mapping resets role scope and submits a later-page cluster role", async ({
  page,
}) => {
  const requests: number[] = [];
  await page.route("**/api/v1/rbac/global-roles/**", (route) =>
    route.fulfill({
      json: {
        data: [
          {
            id: "global-role",
            name: "Global viewer",
            display_name: "Global viewer",
            created_at: "2026-09-22T00:00:00Z",
            rules: [],
          },
        ],
        pagination: {
          total: 1,
          limit: 25,
          offset: 0,
          has_more: false,
          next_offset: null,
        },
      },
    }),
  );
  await page.route("**/api/v1/rbac/cluster-roles/**", async (route) => {
    const offset = Number(
      new URL(route.request().url()).searchParams.get("offset"),
    );
    requests.push(offset);
    return route.fulfill({
      json: {
        data: Array.from({ length: offset ? 1 : 25 }, (_, i) => ({
          id: offset ? ROLE_ID : `role-${i}`,
          name: `Cluster role ${offset + i}`,
          display_name: `Cluster role ${offset + i}`,
          created_at: "2026-09-22T00:00:00Z",
          rules: [],
        })),
        pagination: {
          limit: 25,
          offset,
          has_more: offset === 0,
          next_offset: offset === 0 ? 25 : null,
        },
      },
    });
  });
  await page.route("**/api/v1/admin/group-mappings/**", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    mutationStore(page).push({
      path: "/admin/group-mappings",
      body: route.request().postDataJSON(),
    });
    return route.fulfill({
      status: 201,
      json: data({ id: "mapping-1", ...route.request().postDataJSON() }),
    });
  });
  await page.goto("/dashboard/settings/group-mappings/new");
  await page.getByLabel("Group name").fill("operators");
  await page.getByLabel("Role", { exact: true }).press("Enter");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: /^Select / })
    .press("Enter");
  await page.getByLabel("Scope", { exact: true }).selectOption("cluster");
  await page.getByRole("button", { name: "Create mapping" }).press("Enter");
  await expect(
    page.getByText("Role is required", { exact: true }),
  ).toBeVisible();
  expect(mutationStore(page)).toEqual([]);
  await page.getByLabel("Role", { exact: true }).press("Enter");
  const picker = page.getByRole("dialog", { name: "Select cluster role" });
  await expect(picker.getByText("Showing 1-25 of at least 26")).toBeVisible();
  await picker.getByRole("button", { name: "Next page" }).press("Enter");
  await picker
    .getByRole("button", { name: "Select Cluster role 25" })
    .press("Enter");
  await chooseRemoteCluster(page, "Cluster target");
  await page.getByRole("button", { name: "Create mapping" }).press("Enter");
  await expect
    .poll(() => mutationStore(page))
    .toEqual([
      {
        path: "/admin/group-mappings",
        body: {
          group_name: "operators",
          scope: "cluster",
          role_id: ROLE_ID,
          cluster_id: CLUSTER_ID,
        },
      },
    ]);
  expect(requests).toEqual([0, 25]);
});

test("project members reach the tenth page and recover after removing its final binding", async ({
  page,
}) => {
  let removed = false;
  const offsets: number[] = [];
  await page.route("**/api/v1/projects/project-members/**", async (route) => {
    if (
      new URL(route.request().url()).pathname !==
      "/api/v1/projects/project-members/"
    )
      return route.fallback();
    return route.fulfill({
      json: data({
        id: "project-members",
        name: "Membership project",
        display_name: "Membership project",
        cluster_id: CLUSTER_ID,
        resource_quota: {},
        resource_quota_cpu_limit: "",
        resource_quota_memory_limit: "",
        resource_quota_pod_count: 0,
        namespaces: [],
        created_at: "2026-09-22T00:00:00Z",
      }),
    });
  });
  await page.route("**/api/v1/rbac/project-role-bindings/**", async (route) => {
    const request = route.request();
    if (request.method() === "DELETE") {
      expect(new URL(request.url()).pathname).toBe(
        "/api/v1/rbac/project-role-bindings/binding-225/",
      );
      removed = true;
      return route.fulfill({ status: 204 });
    }
    const url = new URL(request.url());
    expect(url.searchParams.get("project_id")).toBe("project-members");
    expect(url.searchParams.get("limit")).toBe("25");
    const offset = Number(url.searchParams.get("offset"));
    offsets.push(offset);
    const total = removed ? 225 : 226;
    return route.fulfill({
      json: {
        data: Array.from({ length: Math.min(25, total - offset) }, (_, i) => ({
          id: `binding-${offset + i}`,
          group: `team-${offset + i}`,
          role_id: ROLE_ID,
          project_id: "project-members",
          created_at: "2026-09-22T00:00:00Z",
        })),
        pagination: {
          limit: 25,
          offset,
          total,
          has_more: offset + 25 < total,
          next_offset: offset + 25 < total ? offset + 25 : null,
        },
      },
    });
  });
  await page.goto("/dashboard/projects/project-members");
  const members = page.locator("#project-members");
  for (let i = 1; i <= 9; i++) {
    await members.getByRole("button", { name: "Next page" }).click();
    await expect(
      members.getByText(`226 role bindings · Page ${i + 1}`, { exact: true }),
    ).toBeVisible();
  }
  await members.getByRole("button", { name: "Remove group: team-225" }).click();
  await page
    .getByRole("dialog", { name: "Remove member" })
    .getByRole("button", { name: "Remove", exact: true })
    .click();
  await expect(members.getByText("No members on this page.")).toBeVisible();
  await members.getByRole("button", { name: "Previous page" }).click();
  await expect(
    members.getByText("group: team-200", { exact: true }),
  ).toBeVisible();
  expect(offsets).toContain(225);
  expect(removed).toBe(true);
});

async function chooseRemoteCluster(pageContext: Page, label: string) {
  const picker = pageContext.getByRole("combobox", { name: label });
  await picker.focus();
  await pageContext.keyboard.press("Enter");
  await expect(
    pageContext.getByRole("searchbox", { name: "Search clusters" }),
  ).toBeFocused();
  await expect(
    pageContext
      .getByRole("listbox", { name: "Cluster search results" })
      .getByRole("option"),
  ).toHaveCount(1);
  await pageContext.keyboard.press("Enter");
  await expect(picker).toHaveAttribute("aria-expanded", "false");
  await expect(picker).toBeFocused();
}

test("keyboard-only RBAC binding creation submits the selected scope", async ({
  page,
}) => {
  await page.goto("/dashboard/rbac?tab=bindings");
  await expect(page.getByRole("heading", { name: "RBAC" })).toBeVisible();

  const open = page.getByRole("button", { name: "Create Binding" });
  await open.focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", { name: "Create Binding" });
  await expect(dialog).toBeVisible();

  const principalSearch = page.getByRole("searchbox", {
    name: "Search local and external identities",
  });
  await principalSearch.focus();
  await page.keyboard.type("admin");
  const principal = page.getByRole("button", { name: /Admin User/ });
  await principal.focus();
  await page.keyboard.press("Enter");
  await dialog.getByRole("button", { name: "Role", exact: true }).focus();
  await page.keyboard.press("Enter");
  const rolePicker = page.getByRole("dialog", { name: "Select cluster role" });
  await expect(rolePicker).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(rolePicker).not.toBeVisible();
  await expect(dialog).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "Role", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Enter");
  await rolePicker.getByRole("button", { name: /^Select / }).focus();
  await page.keyboard.press("Enter");
  await chooseRemoteCluster(page, "Cluster");
  const submit = dialog.getByRole("button", { name: "Create Binding" });
  await expect(submit).toBeEnabled();
  await submit.focus();
  await page.keyboard.press("Enter");

  await expect.poll(() => mutationStore(page).length).toBe(1);
  expect(mutationStore(page)[0]).toEqual({
    path: "/rbac/cluster-role-bindings",
    body: {
      user_id: USER_ID,
      role_id: ROLE_ID,
      cluster_id: CLUSTER_ID,
    },
  });
});

test("keyboard-only CIS wizard selects, reviews, and starts a scan", async ({
  page,
}) => {
  await page.goto("/dashboard/security/scans/new");
  await expect(
    page.getByRole("heading", { name: "Run CIS Scan" }),
  ).toBeVisible();

  await chooseRemoteCluster(page, "Cluster to scan");
  const next = page.getByRole("button", { name: "Next" });
  await next.focus();
  await page.keyboard.press("Enter");

  const profile = page.getByRole("button", { name: /eks-cis-1\.5/ });
  await expect(profile).toBeVisible();
  await profile.focus();
  await page.keyboard.press("Enter");
  await next.focus();
  await page.keyboard.press("Enter");

  const run = page.getByRole("button", { name: "Run Scan" });
  await run.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/dashboard\/security\/scans\/scan-keyboard/);

  expect(mutationStore(page)).toContainEqual({
    path: "/security/scans",
    body: { cluster_id: CLUSTER_ID, profile: "eks-cis-1.5" },
  });
});

test("keyboard-only snapshot restore queues the selected backup", async ({
  page,
}) => {
  await page.goto(`/dashboard/clusters/${CLUSTER_ID}/snapshots`);
  await expect(
    page.getByRole("heading", { name: "Snapshots", exact: true }),
  ).toBeVisible();

  const restore = page.getByRole("button", { name: "Restore", exact: true });
  await restore.focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", {
    name: "Restore from nightly-prod",
  });
  await expect(dialog).toBeVisible();
  const submit = dialog.getByRole("button", { name: "Restore", exact: true });
  await submit.focus();
  await page.keyboard.press("Enter");

  await expect
    .poll(() =>
      mutationStore(page).some((row) => row.path.endsWith("/restore")),
    )
    .toBe(true);
  expect(mutationStore(page)).toContainEqual({
    path: `/clusters/${CLUSTER_ID}/snapshots/${SNAPSHOT_ID}/restore`,
    body: {
      target_cluster_id: CLUSTER_ID,
      spec: {
        restorePVs: true,
      },
    },
  });
});

test("keyboard-only cluster switcher opens with Ctrl/Cmd+J and navigates on Enter", async ({
  page,
}) => {
  // Override the shared single-cluster list just for this test so ArrowDown
  // has a second cluster to land on; other tests rely on exactly one.
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path =
      url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "") || "/";
    if (path === "/clusters" && route.request().method() === "GET") {
      return route.fulfill({
        json: {
          data: [clusterWire, clusterWire2],
          pagination: {
            total: 2,
            limit: 200,
            offset: 0,
            has_more: false,
            next_offset: null,
          },
        },
      });
    }
    return route.fallback();
  });

  await page.goto("/dashboard/clusters");
  await expect(
    page.getByRole("heading", { name: "Clusters", exact: true }),
  ).toBeVisible();

  await page.keyboard.press("ControlOrMeta+j");
  const listbox = page.getByRole("listbox");
  await expect(listbox).toBeVisible();
  await expect(
    listbox.getByRole("option", { name: /Production EKS/ }),
  ).toBeVisible();
  await expect(
    listbox.getByRole("option", { name: /Staging EKS/ }),
  ).toBeVisible();

  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");

  await expect(page).toHaveURL(`/dashboard/clusters/${CLUSTER_ID_2}`);
});

test("keyboard-only node detail tab strip moves focus and selection with ArrowRight", async ({
  page,
}) => {
  await page.goto(`/dashboard/clusters/${CLUSTER_ID}/nodes/${NODE_NAME}`);
  await expect(page.getByRole("heading", { name: NODE_NAME })).toBeVisible();

  const overview = page.getByRole("tab", { name: "Overview" });
  await overview.focus();
  await page.keyboard.press("ArrowRight");
  const pods = page.getByRole("tab", { name: /^Pods/ });
  await expect(pods).toBeFocused();
  await expect(pods).toHaveAttribute("aria-selected", "true");
});
