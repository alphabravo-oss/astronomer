import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;

test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  await page.route("**/namespaces/**", (route) => {
    if (new URL(route.request().url()).pathname.endsWith("/namespaces/"))
      return route.fulfill({
        json: {
          data: [
            {
              name: "default",
              clusterId: SMOKE_CLUSTER_ID,
              createdAt: "2026-01-01T00:00:00Z",
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
    return route.fallback();
  });
  await page.route(
    "**/k8s/apis/apps/v1/namespaces/default/deployments/review-web**",
    (route) =>
      route.fulfill({
        json: {
          apiVersion: "apps/v1",
          kind: "Deployment",
          metadata: { name: "review-web", namespace: "default" },
          spec: {
            replicas: 1,
            selector: { matchLabels: { app: "review" } },
            template: {
              spec: {
                containers: [
                  { name: "app", image: "app:v1" },
                  { name: "worker", image: "worker:v1" },
                ],
              },
            },
          },
          status: { availableReplicas: 0 },
        },
      }),
  );
  await page.route(
    "**/workloads/deployments/default/review-web/pods/**",
    (route) =>
      route.fulfill({
        json: {
          data: [
            {
              name: "review-pod",
              namespace: "default",
              clusterId: SMOKE_CLUSTER_ID,
              phase: "Running",
              status: "CrashLoopBackOff",
              ready: "1/2",
              restarts: 7,
              containers: [
                { name: "app", image: "app:v1", ready: false, restartCount: 7 },
                {
                  name: "worker",
                  image: "worker:v1",
                  ready: true,
                  restartCount: 0,
                },
              ],
              createdAt: "2026-01-01T00:00:00Z",
            },
          ],
        },
      }),
  );
  await page.route(
    "**/k8s/api/v1/namespaces/default/pods/review-pod**",
    (route) =>
      route.fulfill({
        json: {
          apiVersion: "v1",
          kind: "Pod",
          metadata: { name: "review-pod", namespace: "default" },
          spec: {
            containers: [
              { name: "app", image: "app:v1" },
              { name: "worker", image: "worker:v1" },
            ],
          },
          status: {
            phase: "Running",
            containerStatuses: [
              {
                name: "app",
                ready: false,
                restartCount: 7,
                state: { waiting: { reason: "CrashLoopBackOff" } },
              },
            ],
          },
        },
      }),
  );
});

test("workload to pod retains failing status, URL logs state, and browser Back context", async ({
  page,
}) => {
  await page.goto(
    `${base}/deployments/default/review-web?tab=workload-pods&namespaces=default`,
  );
  await expect(
    page.getByText("Crash Loop Back Off", { exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "review-pod", exact: true }).click();
  const back = page.getByRole("link", {
    name: "Back to workload",
    exact: true,
  });
  await expect(back).toBeVisible();
  const origin = new URL((await back.getAttribute("href"))!, page.url());
  expect(origin.pathname).toBe(`${base}/deployments/default/review-web`);
  expect(origin.searchParams.get("namespaces")).toBe("default");
  expect(origin.searchParams.get("tab")).toBe("workload-pods");
  await page.getByRole("tab", { name: "Events", exact: true }).click();
  await expect(page).toHaveURL(/tab=events/);
  await page.reload();
  await expect(
    page.getByRole("tab", { name: "Events", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: "Logs", exact: true }).click();
  await expect(page).toHaveURL(/tab=logs/);
  await page.reload();
  await expect(
    page.getByRole("tab", { name: "Logs", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await page.goBack();
  await expect(page).toHaveURL(
    /deployments\/default\/review-web.*tab=workload-pods/,
  );
  await expect(
    page.getByRole("link", { name: "review-pod", exact: true }),
  ).toBeVisible();
});

test("multi-container log selection, tail and filter survive direct links and refresh", async ({
  page,
}) => {
  const reads: URL[] = [];
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") ===
      `/api/v1/workloads/pods/${SMOKE_CLUSTER_ID}/default/review-pod/logs`,
    (route) => {
      const url = new URL(route.request().url());
      reads.push(url);
      const container = url.searchParams.get("container");
      return route.fulfill({
        json: {
          data: ["needle", "error", "discard"].map((message) => ({
            timestamp: "2026-09-01T00:00:00Z",
            message: `${message} from ${container}`,
            container,
          })),
        },
      });
    },
  );
  await page.goto(
    `${base}/pods/default/review-pod?tab=logs&container=worker&tail=1000&logFilter=needle`,
  );
  const container = page.getByRole("combobox", {
    name: "Container",
    exact: true,
  });
  const tail = page.getByRole("combobox", {
    name: "Log tail lines",
    exact: true,
  });
  const filter = page.getByRole("textbox", {
    name: "Filter log lines",
    exact: true,
  });
  const logs = page.getByRole("log", {
    name: "Logs for default/review-pod",
    exact: true,
  });
  await expect(container).toHaveValue("worker");
  await expect(tail).toHaveValue("1000");
  await expect(filter).toHaveValue("needle");
  await expect(logs).toContainText("needle from worker");
  await expect(logs).not.toContainText("discard");
  await expect(
    page.getByRole("link", { name: "Back to workload", exact: true }),
  ).toHaveCount(0);
  await container.selectOption("app");
  await expect(page).toHaveURL(
    (url) => url.searchParams.get("container") === "app",
  );
  await tail.selectOption("100");
  await expect(page).toHaveURL((url) => url.searchParams.get("tail") === "100");
  await filter.fill("error");
  await expect(page).toHaveURL(
    (url) => url.searchParams.get("logFilter") === "error",
  );
  await page.reload();
  await expect(container).toHaveValue("app");
  await expect(tail).toHaveValue("100");
  await expect(filter).toHaveValue("error");
  await expect(logs).toContainText("error from app");
  expect(
    reads.some(
      (url) =>
        url.searchParams.get("container") === "worker" &&
        url.searchParams.get("tailLines") === "1000",
    ),
  ).toBe(true);
  expect(reads.at(-1)?.searchParams.get("container")).toBe("app");
  expect(reads.at(-1)?.searchParams.get("tailLines")).toBe("100");
});

test("invalid and Exec URL tabs fall back without opening a terminal session", async ({
  page,
}) => {
  const executions: string[] = [];
  page.on("request", (request) => {
    if (/\/(exec|shell)(\/|\?)/.test(request.url()))
      executions.push(request.url());
  });
  page.on("websocket", (socket) => {
    if (/\/(exec|shell)(\/|\?)/.test(socket.url()))
      executions.push(socket.url());
  });
  for (const tab of ["not-a-tab", "exec"]) {
    await page.goto(`${base}/pods/default/review-pod?tab=${tab}`);
    await expect(
      page.getByRole("tab", { name: "Overview", exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(
      page.getByRole("tab", { name: "Exec", exact: true }),
    ).toHaveAttribute("aria-selected", "false");
    await expect(
      page.getByRole("link", { name: "Back to workload", exact: true }),
    ).toHaveCount(0);
    expect(executions).toEqual([]);
  }
});

for (const status of [403, 404]) {
  test(`unavailable originating workload ${status} preserves a recoverable pod investigation`, async ({
    page,
  }) => {
    await page.route(
      "**/k8s/apis/apps/v1/namespaces/default/deployments/review-web**",
      (route) =>
        route.fulfill({
          status,
          json: {
            error: {
              message:
                status === 403
                  ? "Parent workload access denied"
                  : "Parent workload not found",
            },
          },
        }),
    );
    const origin = `${base}/deployments/default/review-web?tab=workload-pods&namespaces=default`;
    await page.goto(
      `${base}/pods/default/review-pod?tab=events&namespaces=default&origin=${encodeURIComponent(origin)}`,
    );
    await expect(
      page.getByRole("tab", { name: "Events", exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await page
      .getByRole("link", { name: "Back to workload", exact: true })
      .click();
    await expect(
      page
        .getByRole("main")
        .getByText(status === 403 ? /denied|permission/i : /not found/i)
        .first(),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Back", exact: true }),
    ).toHaveAttribute("href", `${base}/deployments`);
    await page.goBack();
    await expect(
      page.getByRole("tab", { name: "Events", exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(
      page.getByRole("heading", { name: "review-pod", exact: true }),
    ).toBeVisible();
  });
}
