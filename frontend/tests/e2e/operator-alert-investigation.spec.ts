import { test, expect } from "@playwright/test";
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
const event = {
  id,
  ruleId: "rule-workflow",
  ruleName: "Checkout readiness",
  clusterId,
  clusterName: "Smoke East",
  namespace: "payments",
  severity: "critical",
  status: "firing",
  message,
  firedAt: now,
  acknowledgedAt: null,
  resolvedAt: null,
};
test.beforeEach(async ({ page, context }) => {
  await workflowAuth(page, context);
  await jsonRoute(page, "/api/v1/alerting/events", pageOf([event]));
  await jsonRoute(page, `/api/v1/alerting/events/${id}`, { data: event });
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
