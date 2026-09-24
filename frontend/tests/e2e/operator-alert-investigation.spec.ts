import { test, expect } from "@playwright/test";
import type {
  AlertEvent,
  RBACEffectivePermissions,
} from "../../src/types/openapi.generated";
import { authMeWire, seedAuth } from "./helpers/auth";
import { readOnlyAuthUser } from "./helpers/auth-state";
import {
  clusterId,
  now,
  pageOf,
  workflowAuth,
  jsonRoute,
  errorBody,
} from "./helpers/operator-workflows";
const id = "alert-workflow-1";
const message = `Workload checkout has failed readiness. ${"Detailed diagnostic context with namespace and timing. ".repeat(25)} END-OF-LONG-MESSAGE`;
const event: AlertEvent = {
  id,
  ruleId: "rule-workflow",
  ruleName: "Checkout readiness",
  clusterId,
  clusterName: "Smoke East",
  namespace: "payments",
  resource: "deployment/checkout",
  labels: {},
  severity: "critical",
  status: "firing",
  message,
  firedAt: now,
  acknowledgedAt: null,
  acknowledgedBy: null,
  resolvedAt: null,
  resolvedBy: null,
};
test.beforeEach(async ({ page, context }) => {
  await workflowAuth(page, context);
  await jsonRoute(page, "/api/v1/alerting/events/summary", {
    data: {
      total: 1,
      firing: 1,
      acknowledged: 0,
      resolved: 0,
      silenced: 0,
      firing_critical: 1,
      firing_warning: 0,
      firing_info: 0,
      as_of: now,
    },
  });
  await jsonRoute(page, "/api/v1/alerting/events", pageOf([event]));
  await jsonRoute(page, `/api/v1/alerting/events/${id}`, { data: event });
});

for (const missing of ["rule", "cluster"] as const) {
  test(`alert without a ${missing} identity does not invent destination links`, async ({
    page,
  }) => {
    const incomplete: AlertEvent = {
      ...event,
      ...(missing === "rule"
        ? { ruleId: "", ruleName: "" }
        : { clusterId: null, clusterName: null }),
    };
    await jsonRoute(page, `/api/v1/alerting/events/${id}`, {
      data: incomplete,
    });
    await page.goto(`/dashboard/alerting?tab=history&event=${id}`);
    const dialog = page.getByRole("dialog", { name: "Alert investigation" });
    await expect(dialog.getByText(message, { exact: true })).toBeVisible();
    await expect(
      dialog.getByRole("link", { name: "Investigate rule" }),
    ).toHaveCount(0);
    if (missing === "rule") {
      await expect(
        dialog.getByText("Unavailable", { exact: true }),
      ).toBeVisible();
      await expect(
        dialog.getByRole("link", { name: "Cluster metrics" }),
      ).toHaveAttribute("href", `/dashboard/clusters/${clusterId}/metrics`);
    } else {
      await expect(
        dialog.getByText(event.ruleName, { exact: true }),
      ).toBeVisible();
      await expect(dialog.getByRole("link")).toHaveCount(0);
    }
    // A resource display string is insufficient to construct a resource URL.
    await expect(dialog.locator('a[href*="checkout"]')).toHaveCount(0);
    await expect(
      dialog.locator('a[href*="undefined"], a[href*="null"]'),
    ).toHaveCount(0);
  });
}

test("an alert reader sees only destinations granted by the authenticated role", async ({
  page,
  context,
}) => {
  const bindingId = "39c9a5cc-c8c4-4c45-8c59-ff0df1b8ad01";
  const roleId = "39c9a5cc-c8c4-4c45-8c59-ff0df1b8ad02";
  const rule = { resource: "alerts", verbs: ["read"] };
  const user = {
    ...readOnlyAuthUser,
    globalRoles: [],
    roles: {
      global: [
        { id: bindingId, roleId, roleName: "Alert reader", roleRules: [rule] },
      ],
      cluster: [],
      project: [],
    },
  };
  await seedAuth(context, page, user);
  let authReads = 0;
  await page.route(
    (url) => url.pathname.replace(/\/$/, "") === "/api/v1/auth/me",
    (route) => {
      authReads += 1;
      return route.fulfill({ json: { data: authMeWire(user) } });
    },
  );
  const permissions: RBACEffectivePermissions = {
    subject: { user_id: user.id, self: true },
    superuser: false,
    context: { namespace_scoped_bindings_supported: true, warnings: [] },
    bindings: [
      {
        scope: "global",
        binding_id: bindingId,
        role_id: roleId,
        role_name: "Alert reader",
        rules: [rule],
      },
    ],
    permissions: [
      {
        resource: "alerts",
        verb: "read",
        applies_to_context: true,
        sources: [
          {
            scope: "global",
            binding_id: bindingId,
            role_id: roleId,
            role_name: "Alert reader",
          },
        ],
      },
    ],
  };
  await jsonRoute(page, "/api/v1/rbac/my-permissions", { data: permissions });
  const resolved: AlertEvent = {
    ...event,
    status: "resolved",
    resolvedAt: "2026-09-24T12:10:00Z",
    resolvedBy: "user-admin",
  };
  await jsonRoute(page, "/api/v1/alerting/events", pageOf([resolved]));
  await jsonRoute(page, `/api/v1/alerting/events/${id}`, { data: resolved });
  await jsonRoute(page, "/api/v1/alerting/events/summary", {
    data: {
      total: 1,
      firing: 0,
      acknowledged: 0,
      resolved: 1,
      silenced: 0,
      firing_critical: 0,
      firing_warning: 0,
      firing_info: 0,
      as_of: now,
    },
  });
  await page.goto(`/dashboard/alerting?tab=history&event=${id}`);
  const dialog = page.getByRole("dialog", { name: "Alert investigation" });
  await expect(dialog.getByText(message, { exact: true })).toBeVisible();
  await expect.poll(() => authReads).toBeGreaterThan(0);
  // Global alerts:read is inherited by the cluster alert destination, but does
  // not imply clusters:read or monitoring:read.
  await expect(
    dialog.getByRole("link", { name: "Investigate rule" }),
  ).toHaveAttribute(
    "href",
    `/dashboard/clusters/${clusterId}/alerting?tab=rules&rule=${event.ruleId}`,
  );
  await expect(
    dialog.getByRole("link", { name: "Cluster metrics" }),
  ).toHaveCount(0);
  await expect(dialog.getByRole("link", { name: /^Cluster:/ })).toHaveCount(0);
  await expect(dialog.getByRole("link")).toHaveCount(1);
  await page.reload();
  await expect(dialog.getByText(message, { exact: true })).toBeVisible();
  await expect(dialog.getByRole("link")).toHaveCount(1);
  await expect(
    dialog.getByRole("link", { name: "Investigate rule" }),
  ).toBeVisible();
});

for (const status of ["acknowledged", "resolved"] as const) {
  test(`${status} history preserves filters, server page and off-page deep link through reload`, async ({
    page,
  }) => {
    const records: AlertEvent[] = Array.from({ length: 51 }, (_, index) => ({
      ...event,
      id: `history-${status}-${index + 1}`,
      ruleName: `History ${status} ${index + 1}`,
      message: `Historical ${status} event ${index + 1}`,
      status,
      acknowledgedAt: "2026-09-24T12:05:00Z",
      acknowledgedBy: "user-admin",
      resolvedAt: status === "resolved" ? "2026-09-24T12:10:00Z" : null,
      resolvedBy: status === "resolved" ? "user-admin" : null,
    }));
    const selected = records[0]!;
    const reads: { method: string; query: Record<string, string> }[] = [];
    await page.route(
      (url) => url.pathname.replace(/\/$/, "") === "/api/v1/alerting/events",
      (route) => {
        const query = new URL(route.request().url()).searchParams;
        reads.push({
          method: route.request().method(),
          query: Object.fromEntries(query),
        });
        const offset = Number(query.get("offset"));
        return route.fulfill({
          json: {
            data: records.slice(offset, offset + 50),
            pagination: {
              limit: 50,
              offset,
              total: 51,
              has_more: offset === 0,
              next_offset: offset === 0 ? 50 : null,
            },
          },
        });
      },
    );
    await jsonRoute(page, `/api/v1/alerting/events/${selected.id}`, {
      data: selected,
    });
    await jsonRoute(page, "/api/v1/alerting/events/summary", {
      data: {
        total: 51,
        firing: 0,
        acknowledged: status === "acknowledged" ? 51 : 0,
        resolved: status === "resolved" ? 51 : 0,
        silenced: 0,
        firing_critical: 0,
        firing_warning: 0,
        firing_info: 0,
        as_of: now,
      },
    });
    await page.goto(
      `/dashboard/alerting?tab=history&alertPage=1&alertStatus=${status}&alertSeverity=critical&event=${selected.id}`,
    );
    const dialog = page.getByRole("dialog", { name: "Alert investigation" });
    await expect(
      dialog.getByText(selected.message, { exact: true }),
    ).toBeVisible();
    await expect(
      dialog
        .locator("span")
        .filter({ hasText: new RegExp(`^${status}$`, "i") }),
    ).toBeVisible();
    await expect(
      dialog.getByText(selected.acknowledgedAt!, { exact: true }),
    ).toBeVisible();
    if (selected.resolvedAt) {
      await expect(
        dialog.getByText(selected.resolvedAt, { exact: true }),
      ).toBeVisible();
    }
    await page.reload();
    await expect(
      dialog.getByText(selected.message, { exact: true }),
    ).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(page).toHaveURL(
      (url) =>
        !url.searchParams.has("event") &&
        url.searchParams.get("alertPage") === "1" &&
        url.searchParams.get("alertStatus") === status &&
        url.searchParams.get("alertSeverity") === "critical",
    );
    await expect(
      page.getByRole("button", { name: `History ${status} 51`, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: selected.ruleName, exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Ack", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Resolve", exact: true }),
    ).toHaveCount(status === "acknowledged" ? 1 : 0);
    await expect(page.getByLabel("Filter alert events by status")).toHaveValue(
      status,
    );
    await expect(
      page.getByLabel("Filter alert events by severity"),
    ).toHaveValue("critical");
    await page
      .getByRole("button", { name: "Previous page", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: selected.ruleName, exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Next page", exact: true }).click();
    await expect(
      page.getByRole("button", { name: `History ${status} 51`, exact: true }),
    ).toBeVisible();
    await page.reload();
    await expect(
      page.getByRole("button", { name: `History ${status} 51`, exact: true }),
    ).toBeVisible();
    expect(reads.some(({ query }) => query.offset === "0")).toBe(true);
    expect(
      reads.filter(({ query }) => query.offset === "50").length,
    ).toBeGreaterThanOrEqual(3);
    for (const read of reads) {
      expect(read.method).toBe("GET");
      expect(read.query).toEqual({
        status,
        severity: "critical",
        limit: "50",
        offset: expect.stringMatching(/^(0|50)$/),
      });
    }
  });
}

test("an unavailable deep-linked alert can retry without losing history context", async ({
  page,
}) => {
  let available = false;
  const reads: string[] = [];
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") === `/api/v1/alerting/events/${id}`,
    (route) => {
      reads.push(route.request().method());
      return route.fulfill(
        available
          ? { json: { data: event } }
          : { status: 404, json: errorBody("Historical alert is unavailable") },
      );
    },
  );
  await page.goto(
    `/dashboard/alerting?tab=history&alertSeverity=critical&event=${id}`,
  );
  const dialog = page.getByRole("dialog", { name: "Alert investigation" });
  await expect(
    dialog.getByText("Alert unavailable", { exact: true }),
  ).toBeVisible();
  await expect(
    dialog.getByText("Historical alert is unavailable", { exact: true }),
  ).toBeVisible();
  await expect(dialog.getByRole("link")).toHaveCount(0);
  available = true;
  await dialog.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(dialog.getByText(message, { exact: true })).toBeVisible();
  expect(reads).toEqual(["GET", "GET"]);
  await expect(page).toHaveURL(
    (url) =>
      url.searchParams.get("event") === id &&
      url.searchParams.get("alertSeverity") === "critical",
  );
});
test("alert deep link survives reload and exposes the entire investigation", async ({
  page,
}, info) => {
  await page.goto(
    `/dashboard/alerting?tab=history&event=${id}&alertStatus=firing`,
  );
  const dialog = page.getByRole("dialog", { name: "Alert investigation" });
  await expect(dialog.getByText(message, { exact: true })).toBeVisible();
  await expect(
    dialog.getByRole("link", { name: "Cluster metrics" }),
  ).toHaveAttribute("href", `/dashboard/clusters/${clusterId}/metrics`);
  await expect(
    dialog.getByRole("link", { name: "Investigate rule" }),
  ).toHaveAttribute(
    "href",
    `/dashboard/clusters/${clusterId}/alerting?tab=rules&rule=rule-workflow`,
  );
  await page.reload();
  await expect(dialog.getByText(message, { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/event=alert-workflow-1/);
  for (const control of [
    dialog.getByRole("heading", { name: "Alert investigation", exact: true }),
    dialog.getByRole("button", { name: "Close", exact: true }),
  ]) {
    await expect(control).toBeVisible();
    expect(
      await control.evaluate((element) => {
        const bounds = element.getBoundingClientRect();
        const centerX = bounds.left + bounds.width / 2;
        const centerY = bounds.top + bounds.height / 2;
        const topmost = document.elementFromPoint(centerX, centerY);
        return (
          bounds.top >= 0 &&
          bounds.bottom <= window.innerHeight &&
          topmost !== null &&
          element.contains(topmost)
        );
      }),
    ).toBe(true);
  }
  await page.screenshot({
    path: info.outputPath("alert-deep-link.png"),
    fullPage: true,
    animations: "disabled",
  });
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(page).not.toHaveURL(/event=/);
  await expect(page).toHaveURL(/alertStatus=firing/);
});
test("acknowledgement uses one POST while pending and retains failed feedback", async ({
  page,
}, info) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const calls: { method: string; body: string | null }[] = [];
  await page.route(
    `**/api/v1/alerting/events/${id}/acknowledge/**`,
    async (route) => {
      calls.push({
        method: route.request().method(),
        body: route.request().postData(),
      });
      await held;
      await route.fulfill({
        status: 503,
        json: errorBody("Acknowledgement unavailable"),
      });
    },
  );
  await page.goto("/dashboard/alerting?tab=active");
  const ack = page.getByRole("button", { name: "Ack", exact: true });
  await ack.click();
  await expect(ack).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Resolve", exact: true }),
  ).toBeDisabled();
  expect(calls).toHaveLength(1);
  expect(calls[0]).toEqual({ method: "POST", body: null });
  release();
  await expect(
    page.getByRole("alert").filter({ hasText: "The alert update failed" }),
  ).toBeVisible();
  await expect(ack).toBeEnabled();
  await page.screenshot({
    path: info.outputPath("alert-update-failure.png"),
    fullPage: true,
    animations: "disabled",
  });
});
