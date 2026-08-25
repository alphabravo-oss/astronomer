import { expect, test, type Page } from "@playwright/test";

import { authMeWire, seedAuth } from "./helpers/auth";

const CLUSTER_ID = "cluster-1";
const USER_ID = "1fa85f64-5717-4562-b3fc-2c963f66afa6";
const ROLE_ID = "2fa85f64-5717-4562-b3fc-2c963f66afa6";
const SNAPSHOT_ID = "3fa85f64-5717-4562-b3fc-2c963f66afa6";

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
    count: rows.length,
    total: rows.length,
    next: null,
    previous: null,
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
          storageReady: true,
          storageLocations: [
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
    if (path === `/clusters/${CLUSTER_ID}/snapshots` && method === "GET") {
      return route.fulfill({
        json: data([
          {
            id: SNAPSHOT_ID,
            name: "nightly-prod",
            source: "schedule",
            scheduleName: "nightly",
            phase: "Completed",
            spec: { includedNamespaces: ["payments"] },
            startTimestamp: "2026-08-22T23:00:00Z",
            completionTimestamp: "2026-08-22T23:02:00Z",
            warnings: 0,
            errors: 0,
            createdAt: "2026-08-22T23:00:00Z",
          },
        ]),
      });
    }
    if (
      path === `/clusters/${CLUSTER_ID}/snapshot-schedules` &&
      method === "GET"
    ) {
      return route.fulfill({ json: data([]) });
    }
    if (
      path === `/clusters/${CLUSTER_ID}/snapshots/${SNAPSHOT_ID}/restore` &&
      method === "POST"
    ) {
      record();
      return route.fulfill({
        status: 202,
        json: data({
          id: "restore-keyboard",
          name: "restore-nightly-prod",
          snapshotId: SNAPSHOT_ID,
          targetClusterId: CLUSTER_ID,
          phase: "New",
        }),
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

async function chooseNextOption(pageContext: Page, label: string) {
  const select = pageContext.getByLabel(label, { exact: true });
  await select.focus();
  await pageContext.keyboard.press("ArrowDown");
  await pageContext.keyboard.press("Enter");
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

  await chooseNextOption(page, "User");
  await chooseNextOption(page, "Role");
  await chooseNextOption(page, "Cluster");
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

  const cluster = page.getByRole("button", { name: /Production EKS/ });
  await cluster.focus();
  await page.keyboard.press("Enter");
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
