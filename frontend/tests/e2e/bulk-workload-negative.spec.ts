import { expect, test } from "@playwright/test";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser } from "../e2e-smoke/stub-overrides";
import { installStubs } from "../e2e-smoke/stubs";

for (const mode of ["partial", "unconfirmed", "cancelled"] as const) {
  test(`bulk restart handles ${mode} results without hiding or duplicating writes`, async ({
    page,
    context,
  }) => {
    await installStubs(page);
    await seedAuth(context, page, adminStoreUser);
    const names = ["alpha", "bravo", "charlie"];
    const writes: { path: string; key?: string }[] = [];
    let polls = 0;
    await page.route("**/api/v1/clusters/c-smoke-1/workloads/?*", (route) =>
      route.fulfill({
        json: {
          data: names.map((name) => ({
            name,
            namespace: "app",
            kind: "Deployment",
            clusterId: "c-smoke-1",
            clusterName: "Smoke East",
            status: "Running",
            ready: "1/1",
            replicas: 1,
            desiredReplicas: 1,
            images: ["example/app:v1"],
            createdAt: "2026-09-22T00:00:00Z",
            labels: {},
            annotations: {},
          })),
          pagination: {
            total: 3,
            limit: 20,
            offset: 0,
            has_more: false,
            next_offset: null,
          },
        },
      }),
    );
    await page.route(
      "**/api/v1/clusters/c-smoke-1/workloads/Deployment/app/*/restart/",
      (route) => {
        const request = route.request();
        writes.push({
          path: new URL(request.url()).pathname,
          key: request.headers()["idempotency-key"],
        });
        expect(request.method()).toBe("POST");
        return route.fulfill({
          status: 202,
          json: {
            data: {
              id: `op-${writes.length}`,
              status:
                mode === "partial"
                  ? writes.length === 1
                    ? "partial"
                    : "completed"
                  : "queued",
              errorMessage:
                writes.length === 1 && mode === "partial"
                  ? "One replica was not restarted"
                  : undefined,
            },
          },
        });
      },
    );
    await page.route("**/api/v1/workloads/operations/op-1/", (route) => {
      polls++;
      return mode === "unconfirmed"
        ? route.fulfill({
            status: 403,
            json: { error: { message: "Operation status denied" } },
          })
        : route.fulfill({ json: { data: { id: "op-1", status: "running" } } });
    });
    await page.goto("/dashboard/clusters/c-smoke-1/deployments");
    for (const name of names)
      await page
        .getByRole("checkbox", { name: `Select row app/${name}`, exact: true })
        .check();
    await page.getByRole("button", { name: "Restart selected" }).click();
    const confirm = page.getByRole("dialog", { name: "Restart 3 workloads" });
    await expect(
      confirm.getByRole("button", { name: "Run operations" }),
    ).toBeDisabled();
    expect(writes).toHaveLength(0);
    await confirm.getByPlaceholder("restart 3").fill("restart 3");
    await confirm.getByRole("button", { name: "Run operations" }).click();
    if (mode === "cancelled") {
      await expect.poll(() => polls).toBeGreaterThan(0);
      await page
        .getByRole("button", { name: "Stop remaining operations" })
        .click();
    }
    const results = page.getByRole("dialog", {
      name: "Workload operation results",
    });
    await expect(results).toBeVisible();
    const first = results
      .getByRole("listitem")
      .filter({ hasText: "app/alpha" });
    await expect(first).toContainText(
      mode === "partial" ? "failed" : "unconfirmed",
    );
    await expect(first).toContainText("Operation: op-1");
    if (mode === "partial")
      await expect(first).toContainText("One replica was not restarted");
    for (const name of names.slice(1))
      await expect(
        results.getByRole("listitem").filter({ hasText: `app/${name}` }),
      ).toContainText(mode === "partial" ? "succeeded" : "not-started");
    const sent = mode === "partial" ? names : names.slice(0, 1);
    expect(writes.map((write) => write.path)).toEqual(
      sent.map(
        (name) =>
          `/api/v1/clusters/c-smoke-1/workloads/Deployment/app/${name}/restart/`,
      ),
    );
    for (const write of writes) expect(write.key).toMatch(/^workload-restart:/);
    expect(new Set(writes.map((write) => write.key)).size).toBe(writes.length);
    await results.getByRole("button", { name: "Done" }).click();
    await expect(results).not.toBeVisible();
    expect(writes).toHaveLength(sent.length);
  });
}
