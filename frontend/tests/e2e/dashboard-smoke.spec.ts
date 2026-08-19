import { expect, type Page, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

import { CSRF_COOKIE, SESSION_COOKIE, seedAuth } from "./helpers/auth";

type SmokeUser = {
  id: string;
  username: string;
  email: string;
  displayName: string;
  provider: string;
  globalRoles: string[];
  isSuperuser: boolean;
  roles: {
    global: Array<Record<string, unknown>>;
    cluster: Array<Record<string, unknown>>;
    project: Array<Record<string, unknown>>;
  };
  enabled: boolean;
  lastLogin: string;
  createdAt: string;
};

const adminUser: SmokeUser = {
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

const readOnlyUser = {
  ...adminUser,
  id: "user-readonly",
  username: "reader",
  email: "reader@example.com",
  displayName: "Read Only",
  globalRoles: ["viewer"],
  isSuperuser: false,
  roles: {
    global: [
      {
        roleRules: [{ resources: ["clusters"], verbs: ["list", "read"] }],
      },
    ],
    cluster: [],
    project: [],
  },
} satisfies SmokeUser;

const cluster = {
  id: "cluster-1",
  name: "prod-east",
  displayName: "Prod East",
  description: "Production cluster",
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

const project = {
  id: "project-1",
  name: "platform",
  displayName: "Platform",
  description: "Platform delivery project",
  namespaces: [],
  members: [],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

const catalogChart = {
  id: "chart-prometheus",
  repositoryId: "repo-prometheus",
  repositoryName: "Prometheus Community",
  name: "kube-prometheus-stack",
  displayName: "Kube Prometheus Stack",
  description: "Prometheus, Grafana, and alerting for Kubernetes.",
  category: "monitoring",
  keywords: ["monitoring", "prometheus"],
  homeUrl: "https://prometheus.io",
  iconUrl: "",
  sources: [],
  maintainers: [],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

const catalogVersion = {
  id: "version-prometheus-1",
  chartId: catalogChart.id,
  version: "61.0.0",
  appVersion: "0.75.0",
  defaultValues: "grafana:\n  enabled: true\n",
  valuesSchema: null,
  readme: "Install kube-prometheus-stack.",
  createdAt: new Date().toISOString(),
};

function apiResponse<T>(data: T) {
  return { status: 200, data };
}

function paginated<T>(data: T[]) {
  return { data, total: data.length, page: 1, pageSize: 100, totalPages: 1 };
}

async function mockApi(page: Page, user = adminUser) {
  let generalSettings = {
    platformName: "Astronomer",
    agentHeartbeatInterval: 30,
    defaultSessionTimeout: 60,
    enableAuditLogging: true,
    metricsCollection: true,
  };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path =
      url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "") || "/";
    const method = request.method();

    if (path === "/events/stream") {
      return route.fulfill({ status: 204, body: "" });
    }
    if (path === "/auth/sso/providers" && method === "GET") {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === "/auth/login" && method === "POST") {
      // Set the session/CSRF pair through the context, not a Set-Cookie
      // header: route.fulfill sends the header dict as single entries, and a
      // comma-joined Set-Cookie only stores the first cookie — the JS-readable
      // CSRF cookie the /dashboard guard (hasSessionHint) checks never landed.
      await page.context().addCookies([
        {
          name: SESSION_COOKIE,
          value: "e2e-session",
          domain: "127.0.0.1",
          path: "/",
        },
        {
          name: CSRF_COOKIE,
          value: "e2e-csrf",
          domain: "127.0.0.1",
          path: "/",
        },
      ]);
      return route.fulfill({
        json: apiResponse({
          token: "unused-cookie-auth-token",
          refresh: "unused-refresh",
          user: adminUser,
        }),
      });
    }
    if (path === "/auth/logout" && method === "POST") {
      // Mirror the backend's clearBrowserSessionCookies: both cookies drop in
      // lockstep so the guard sees the session as gone.
      await page.context().clearCookies({ name: SESSION_COOKIE });
      await page.context().clearCookies({ name: CSRF_COOKIE });
      return route.fulfill({ json: apiResponse({ ok: true }) });
    }
    if (path === "/auth/me") {
      return route.fulfill({ json: apiResponse(user) });
    }
    if (path === "/settings/features") {
      return route.fulfill({
        json: apiResponse({
          "feature.catalog": true,
          "feature.projects": true,
          "feature.monitoring": true,
          "feature.security": true,
          "feature.backups": true,
          "feature.charlie": true,
        }),
      });
    }
    if (path === "/clusters" && method === "GET") {
      return route.fulfill({ json: paginated([cluster]) });
    }
    if (path === "/clusters" && method === "POST") {
      return route.fulfill({
        json: apiResponse({
          ...cluster,
          id: "cluster-new",
          name: "e2e-cluster",
          displayName: "E2E Cluster",
        }),
      });
    }
    if (
      path === "/clusters/cluster-new/registration/options" &&
      method === "PUT"
    ) {
      return route.fulfill({
        json: apiResponse({
          phase: "created",
          installBaseline: false,
          steps: [],
        }),
      });
    }
    if (path === "/clusters/cluster-1") {
      return route.fulfill({ json: apiResponse(cluster) });
    }
    if (path.startsWith("/clusters/cluster-1/")) {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === "/projects" && method === "GET") {
      return route.fulfill({ json: paginated([project]) });
    }
    if (
      method === "GET" &&
      [
        "/delivery/sources",
        "/delivery/bundles",
        "/delivery/targets",
        "/delivery/rollouts",
        "/delivery/deployments",
      ].includes(path)
    ) {
      return route.fulfill({
        json: {
          data: [],
          count: 0,
          next: null,
          previous: null,
          totalKnown: true,
        },
      });
    }
    if (path === "/delivery/fleet" && method === "GET") {
      return route.fulfill({
        json: apiResponse({
          summary: {
            adoptedClusters: 2,
            fluxReady: 2,
            incompatible: 0,
            disconnected: 0,
            stale: 0,
            assignments: 4,
            drifted: 0,
            failed: 0,
            degraded: 0,
            activeRollouts: 0,
          },
          clusters: [],
          attention: [],
          distributions: {
            compatibility: [{ key: "compatible", count: 2 }],
            privilege: [{ key: "admin", count: 2 }],
            assignmentPhases: [{ key: "ready", count: 4 }],
          },
        }),
      });
    }
    if (path === "/delivery/system/compatibility" && method === "GET") {
      return route.fulfill({
        json: apiResponse({
          contract: {
            fluxVersion: "v2.6.4",
            kubernetesMinimum: "1.30",
            kubernetesMaximum: "1.34",
            agentProtocol: "delivery.v2",
            requiredCapabilities: ["delivery.v2"],
          },
          currentRelease: { version: "1.0.0" },
          currentRollout: null,
          observedInventory: [],
        }),
      });
    }
    if (
      path === "/activity" ||
      path === "/alerting/events" ||
      path === "/tools"
    ) {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === "/charlie/threads" && method === "GET") {
      return route.fulfill({
        json: apiResponse({
          threads: [
            {
              id: "session-private-user",
              title: "Inspect alert health",
              state: "active",
              updated_at: new Date().toISOString(),
            },
          ],
        }),
      });
    }
    if (
      path === "/charlie/threads/session-private-user/history" &&
      method === "GET"
    ) {
      return route.fulfill({
        json: apiResponse({
          messages: [
            {
              itemId: "message-charlie",
              kind: "assistant_message",
              redactedContent: "The selected alert is under investigation.",
            },
          ],
        }),
      });
    }
    if (path === "/charlie/sessions" && method === "GET") {
      return route.fulfill({
        json: apiResponse({
          mode: "approval",
          sessions: [
            {
              id: "session-shared-incident",
              clientSessionId: "client-session-shared-incident",
              intent: "Investigate agent connection health",
              resourceScopeSummary: "agent_connection_record:connection-1",
              state: "active",
              visibility: "incident",
              centralRevision: 5,
              source: "event",
              createdAt: new Date().toISOString(),
              updatedAt: new Date().toISOString(),
            },
            {
              id: "session-private-user",
              clientSessionId: "client-session-private-user",
              intent: "Inspect alert health",
              resourceScopeSummary: "alert:active",
              state: "active",
              visibility: "private",
              centralRevision: 3,
              source: "user",
              createdAt: new Date().toISOString(),
              updatedAt: new Date().toISOString(),
            },
          ],
        }),
      });
    }
    if (
      path === "/charlie/sessions/session-private-user/history" &&
      method === "GET"
    ) {
      return route.fulfill({
        json: apiResponse({
          messages: [
            {
              itemId: "message-charlie",
              kind: "assistant_message",
              redactedContent: "The selected alert is under investigation.",
            },
          ],
        }),
      });
    }
    if (path === "/charlie/findings" && method === "GET") {
      return route.fulfill({
        json: apiResponse({
          items: [
            {
              id: "f-1",
              title: "Bounded finding",
              severity: "high",
              state: "open",
              source: "alert",
              repeatCount: 2,
              sessionId: "session-shared-incident",
              createdAt: new Date(Date.now() - 60_000).toISOString(),
              updatedAt: new Date().toISOString(),
              affectedResource: {
                type: "agent_connection_record",
                id: "connection-1",
                requiredVerb: "read",
              },
              summary: "Review the selected installation.",
            },
          ],
        }),
      });
    }
    if (path === "/charlie/findings/f-1") {
      return route.fulfill({
        json: apiResponse({
          finding: {
            id: "f-1",
            title: "Bounded finding",
            severity: "high",
            state: "open",
            affectedResource: {
              type: "agent_connection_record",
              id: "connection-1",
              requiredVerb: "read",
            },
            summary: "Review the selected installation.",
            confidence: 0.9,
            operatorChecks: [],
            evidence: [],
          },
        }),
      });
    }
    if (path === "/catalog/repositories") {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === "/catalog/charts") {
      return route.fulfill({ json: apiResponse([catalogChart]) });
    }
    if (path === `/catalog/charts/${catalogChart.id}/versions`) {
      return route.fulfill({ json: apiResponse([catalogVersion]) });
    }
    if (path === "/catalog/installed" && method === "GET") {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === "/catalog/installed" && method === "POST") {
      return route.fulfill({
        json: apiResponse({
          id: "installed-prometheus",
          releaseName: "kube-prometheus-stack",
          chartName: catalogChart.name,
          chartVersionLabel: catalogVersion.version,
          clusterId: cluster.id,
          clusterName: cluster.displayName,
          namespace: "monitoring",
          status: "pending",
          revision: 1,
          installedBy: user.username,
          createdAt: new Date().toISOString(),
        }),
      });
    }
    if (path === "/settings/general") {
      if (method === "PUT") {
        generalSettings = await request.postDataJSON();
        return route.fulfill({ json: apiResponse(generalSettings) });
      }
      return route.fulfill({ json: apiResponse(generalSettings) });
    }
    if (path === "/settings/sso" || path === "/settings/tokens") {
      return route.fulfill({ json: apiResponse([]) });
    }
    if (path === "/audit" || path === "/settings/audit-logs") {
      return route.fulfill({ json: paginated([]) });
    }
    if (path === "/admin/backup-drill") {
      return route.fulfill({
        json: apiResponse({
          latest: null,
          latest_success: null,
          latest_success_age_seconds: null,
        }),
      });
    }
    if (path === "/admin/backup-drill/history") {
      return route.fulfill({ json: paginated([]) });
    }
    if (path === "/admin/management-backup") {
      return route.fulfill({
        json: apiResponse({
          enabled: false,
          reason: "Add an S3 destination to start nightly dumps of Astronomer's database.",
          destinations: [],
          encryption_key_backup: { wrapping_configured: false },
        }),
      });
    }
    return route.fulfill({ json: apiResponse([]) });
  });
}

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test("redirects unauthenticated dashboard users and supports login/logout", async ({
  page,
}) => {
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/auth\/login/);
  await expect(
    page.getByRole("heading", { name: /sign in to astronomer/i }),
  ).toBeVisible();

  await page.getByPlaceholder("you@example.com").fill("admin@example.com");
  await page.getByPlaceholder("Enter your password").fill("password");
  await page.getByRole("button", { name: /sign in/i }).click();

  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(
    page.getByRole("heading", { name: /platform overview/i }),
  ).toBeVisible();

  await page.getByRole("button", { name: /user menu/i }).click();
  await page.getByRole("button", { name: /sign out/i }).click();
  await expect(page).toHaveURL(/\/auth\/login/);
});

test("cluster registration wizard creates a cluster and advances to connect step", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/clusters/register");
  await expect(
    page.getByRole("heading", { name: /register an existing cluster/i }),
  ).toBeVisible();

  await page.getByPlaceholder("my-cluster").fill("e2e-cluster");
  await page.getByPlaceholder("My Production Cluster").fill("E2E Cluster");
  await page
    .getByRole("button", { name: /next: get install command/i })
    .click();

  await expect(page).toHaveURL(
    /\/dashboard\/clusters\/register\/cluster-new\/connect/,
  );
});

test("read-only cluster detail hides admin-only settings navigation", async ({
  context,
  page,
}) => {
  await mockApi(page, readOnlyUser);
  await seedAuth(context, page, readOnlyUser);
  await page.goto("/dashboard/clusters/cluster-1");

  await expect(page.getByRole("heading", { name: /prod east/i })).toBeVisible();
  await expect(page.getByRole("link", { name: /^Auth$/ })).toHaveCount(0);
});

test("delivery overview renders the Flux-native system for authenticated users", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/delivery");

  await expect(
    page.getByRole("heading", { name: /delivery overview/i }),
  ).toBeVisible();
  await expect(page.getByText("1.0.0")).toBeVisible();
  await expect(page.getByRole("link", { name: /sources/i })).toHaveAttribute(
    "href",
    /project=project-1/,
  );
});

test("catalog install modal remains usable on responsive viewports", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/catalog");

  await expect(page.getByRole("heading", { name: /^Catalog$/ })).toBeVisible();
  await page.getByRole("button", { name: /kube prometheus stack/i }).click();
  await expect(
    page.getByRole("heading", { name: /kube prometheus stack/i }),
  ).toBeVisible();
  await page.getByRole("button", { name: /^Install$/ }).click();

  await expect(
    page.getByRole("heading", { name: /install kube prometheus stack/i }),
  ).toBeVisible();
  await page.getByLabel("Target Cluster").selectOption(cluster.id);
  await page.getByLabel("Release Name").fill("platform-monitoring");
  await page.getByLabel("Namespace").fill("monitoring");
  await expect(
    page.getByRole("button", { name: /install chart/i }),
  ).toBeEnabled();
  await page.getByRole("button", { name: /install chart/i }).click();
});

test("settings general form remains usable on responsive viewports", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/settings/general");

  await expect(page.getByRole("heading", { name: /^Settings$/ })).toBeVisible();
  await page.getByRole("button", { name: /^General$/ }).click();
  await page.getByRole("button", { name: /^Edit$/ }).click();
  await page.getByLabel("Platform Name").fill("Astronomer Control Plane");
  await page.getByLabel("Agent Heartbeat Interval").selectOption("60");
  await page.getByLabel("Default Session Timeout").selectOption("480");
  await page.getByRole("button", { name: /save settings/i }).click();
  await expect(
    page.getByText("Astronomer Control Plane", { exact: true }),
  ).toBeVisible();
});

test("Charlie launcher exposes only bounded route context across product surfaces", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  const cases = [
    ["/dashboard/clusters/cluster-1", "Cluster agent connection"],
    ["/dashboard/clusters/cluster-1/tools", "Cluster agent connection"],
    ["/dashboard/alerting", "Alerts"],
    ["/dashboard/agents", "Cluster agents"],
    ["/dashboard/backups", "Astronomer backup"],
    ["/dashboard/settings/backup", "Astronomer backup"],
    ["/dashboard/delivery", "Continuous delivery"],
  ] as const;

  for (const [path, contextLabel] of cases) {
    await page.goto(path);
    await page.getByRole("button", { name: "Open Charlie assistant" }).click();
    await expect(page.getByRole("dialog", { name: "Charlie" })).toBeVisible();
    await expect(
      page.getByRole("button", { name: `Remove ${contextLabel}` }),
    ).toBeVisible();
    await expect(
      page.getByText(
        /Charlie retrieves authorized diagnostics through audited read tools/i,
      ),
    ).toBeVisible();
    await page.getByRole("button", { name: "Close", exact: true }).click();
  }
});

test("Charlie hub and administration deep links survive refresh and history", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.goto(
    "/dashboard/charlie?tab=findings&filter=open&context=cluster-1&finding=f-1",
  );
  await expect(page.getByRole("tab", { name: "findings" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await page.reload();
  await expect(page).toHaveURL(
    /tab=findings.*filter=open.*context=cluster-1.*finding=f-1/,
  );
  await page.getByRole("tab", { name: "approvals" }).click();
  await expect(page).toHaveURL(
    /tab=approvals.*filter=open.*context=cluster-1.*finding=f-1/,
  );
  await page.goBack();
  await expect(page.getByRole("tab", { name: "findings" })).toHaveAttribute(
    "aria-selected",
    "true",
  );

  await page.goto("/dashboard/settings/charlie?tab=connection");
  for (const tab of [
    "connection",
    "agent",
    "mode",
    "automation",
    "access",
    "diagnostics",
  ]) {
    await page.getByRole("tab", { name: tab }).click();
    await expect(page).toHaveURL(new RegExp(`tab=${tab}`));
    await expect(page.getByRole("tab", { name: tab })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  }
  await page.reload();
  await expect(page.getByRole("tab", { name: "diagnostics" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await page.goBack();
  await expect(page.getByRole("tab", { name: "access" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
});

test("Charlie hub separates private conversations from authorized shared incidents", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.goto("/dashboard/charlie?tab=conversations");
  await expect(page.getByText("Inspect alert health")).toBeVisible();
  await expect(page.getByText("Private chat")).toBeVisible();
  await expect(
    page.getByText("Investigate agent connection health"),
  ).toHaveCount(0);

  await page.getByRole("tab", { name: "investigations" }).click();
  await expect(
    page.getByText("Investigate agent connection health"),
  ).toBeVisible();
  await expect(page.getByText("Shared incident")).toBeVisible();
  await expect(page.getByText("Inspect alert health")).toHaveCount(0);
  await page.getByText("Investigate agent connection health").click();
  await expect(
    page.getByText(
      /you can currently read every affected Astronomer resource/i,
    ),
  ).toBeVisible();
});

test("Charlie drawer and hub are mobile-safe and pass serious axe checks", async ({
  context,
  page,
}) => {
  await seedAuth(context, page, adminUser);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/dashboard/alerting");
  await page.getByRole("button", { name: "Open Charlie assistant" }).click();
  const drawer = page.getByRole("dialog", { name: "Charlie" });
  await expect(drawer).toBeVisible();
  await expect(
    page.getByLabel("Current Charlie mode: Approval mode"),
  ).toBeVisible();
  await expect(
    drawer.getByText(/Every exact write is proposed for human review/),
  ).toBeVisible();
  await expect(drawer.getByText("Alerts")).toBeVisible();
  const viewportWidth = page.viewportSize()?.width ?? 0;
  const drawerBox = await drawer.boundingBox();
  expect(drawerBox).not.toBeNull();
  expect(drawerBox!.width).toBeLessThanOrEqual(viewportWidth);
  expect(
    await drawer.evaluate(
      (element) => element.scrollWidth <= element.clientWidth + 1,
    ),
  ).toBe(true);
  const composer = page.getByLabel("Message Charlie");
  await composer.focus();
  await page.keyboard.press("Shift+Tab");
  await expect(page.locator('[role="dialog"] :focus')).toHaveCount(1);
  // Send is disabled until the composer has content. Tab from the hub link
  // must stay inside the dialog (suggested-command chips sit after it).
  const lastFocusable = drawer.getByRole("link", { name: "Open Charlie hub" });
  await lastFocusable.focus();
  await page.keyboard.press("Tab");
  await expect(page.locator('[role="dialog"] :focus')).toHaveCount(1);
  await expect
    .poll(() =>
      page
        .getByRole("button", { name: "Send" })
        .evaluate((element) => getComputedStyle(element).transitionProperty),
    )
    .toBe("none");
  const drawerA11y = await new AxeBuilder({ page })
    .include('[role="dialog"]')
    .analyze();
  expect(
    drawerA11y.violations.filter(
      (item) => item.impact === "critical" || item.impact === "serious",
    ),
  ).toEqual([]);

  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(drawer).toBeHidden();
  await page.goto(
    "/dashboard/charlie?tab=findings&status=open&severity=high&source=alert",
  );
  await expect(page.getByRole("dialog", { name: "Charlie" })).toHaveCount(0);
  await expect(page.getByText("Bounded finding")).toBeVisible();
  await expect(page.getByText("2 occurrences")).toBeVisible();
  await expect
    .poll(() =>
      page
        .locator("main > div")
        .evaluate((element) => getComputedStyle(element).opacity),
    )
    .toBe("1");
  const hubA11y = await new AxeBuilder({ page }).include("main").analyze();
  expect(
    hubA11y.violations.filter(
      (item) => item.impact === "critical" || item.impact === "serious",
    ),
  ).toEqual([]);
});
