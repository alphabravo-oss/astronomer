import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});
test("custom resource returns to canonical collection", async ({
  page,
}, info) => {
  const base = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
  await page.route(
    "**/k8s/apis/cert-manager.io/v1/namespaces/default/certificates/review-cert**",
    (route) =>
      route.fulfill({
        json: {
          apiVersion: "cert-manager.io/v1",
          kind: "Certificate",
          metadata: {
            name: "review-cert",
            namespace: "default",
            uid: "review-cert-uid",
          },
          spec: { secretName: "review-cert-tls" },
          status: { conditions: [] },
        },
      }),
  );
  await page.goto(
    `${base}/custom-resources/cert-manager.io/v1/certificates/default/review-cert`,
  );
  await expect(
    page.getByRole("heading", { name: "review-cert", exact: true }),
  ).toBeVisible();
  const back = page.getByRole("link", { name: "Back", exact: true });
  await expect(back).toHaveAttribute(
    "href",
    `${base}/custom-resources/cert-manager.io/v1/certificates`,
  );
  await back.click();
  await expect(page.getByText(/Unknown resource type/i)).toHaveCount(0);
  await page.screenshot({ path: info.outputPath("custom-back.png") });
});

test("Velero access failure does not advertise installation", async ({
  page,
}, info) => {
  let statusRequests = 0;
  await page.route("**/api/v1/clusters/*/velero-status**", (route) => {
    statusRequests++;
    return route.fulfill({
      status: 403,
      json: {
        error: { code: "FORBIDDEN", message: "Review fixture: access denied" },
      },
    });
  });
  await page.goto(`/dashboard/clusters/${SMOKE_CLUSTER_ID}/snapshots`);
  await expect(
    page.getByText("Velero is not installed", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page
      .getByRole("main")
      .getByText(/denied|permission/i)
      .first(),
  ).toBeVisible();
  expect(statusRequests).toBeGreaterThan(0);
  await page.screenshot({ path: info.outputPath("velero-failed-read.png") });
});

test("SMTP failed reads cannot render or save invented defaults", async ({
  page,
}) => {
  let writes = 0;
  await page.route("**/api/v1/admin/smtp**", (route) => {
    if (route.request().method() !== "GET") writes++;
    return route.fulfill({
      status: 403,
      json: { error: { message: "SMTP access denied" } },
    });
  });
  await page.goto("/dashboard/settings/smtp");
  await expect(
    page
      .getByRole("main")
      .getByText(/denied|permission/i)
      .first(),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /Save/ })).toHaveCount(0);
  expect(writes).toBe(0);
});

test("SMTP tests only saved configuration after an edited draft is saved", async ({
  page,
}) => {
  let config = {
    host: "smtp.old.test",
    port: 587,
    username: "mailer",
    password: "__redacted__",
    from_address: "ops@example.test",
    from_name: "Operations",
    auth_mechanism: "plain",
    encryption: "starttls",
    require_tls: true,
    timeout_seconds: 30,
  };
  const tests: unknown[] = [];
  const writes: unknown[] = [];
  await page.route("**/api/v1/admin/smtp**", async (route) => {
    if (
      new URL(route.request().url()).pathname
        .replace(/\/$/, "")
        .endsWith("/test")
    ) {
      tests.push(route.request().postDataJSON());
      return route.fulfill({
        json: { success: true, recipient: "ops@example.test" },
      });
    }
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON();
      writes.push(body);
      config = { ...config, ...body };
    }
    return route.fulfill({ json: { data: config } });
  });
  await page.goto("/dashboard/settings/smtp");
  await page.getByRole("button", { name: "Edit configuration" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByPlaceholder("ops@example.com").fill("ops@example.test");
  const testButton = dialog.getByRole("button", {
    name: "Test saved configuration email",
  });
  await expect(testButton).toBeEnabled();
  await dialog.getByLabel("Host", { exact: true }).fill("smtp.new.test");
  await expect(testButton).toBeDisabled();
  expect(tests).toEqual([]);
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog).toHaveCount(0);
  expect(writes).toEqual([expect.objectContaining({ host: "smtp.new.test" })]);
  await page.getByRole("button", { name: "Edit configuration" }).click();
  await dialog.getByPlaceholder("ops@example.com").fill("ops@example.test");
  await testButton.click();
  await expect.poll(() => tests).toEqual([{ recipient: "ops@example.test" }]);
});

for (const status of [403, 404]) {
  test(`Delivery target ${status} renders recoverable read state without success controls`, async ({
    page,
  }) => {
    await page.route("**/api/v1/projects/project-review/**", (route) =>
      route.fulfill({
        json: {
          data: {
            id: "project-review",
            name: "Review",
            display_name: "Review",
            cluster_id: SMOKE_CLUSTER_ID,
            namespaces: ["default"],
            created_at: "2026-09-01T00:00:00Z",
            updated_at: "2026-09-01T00:00:00Z",
          },
        },
      }),
    );
    await page.route("**/api/v1/delivery/targets/missing-target/**", (route) =>
      route.fulfill({
        status,
        json: {
          error: {
            message:
              status === 403 ? "Target access denied" : "Target not found",
          },
        },
      }),
    );
    await page.goto(
      "/dashboard/delivery/targets/missing-target?project=project-review",
    );
    await expect(
      page
        .getByRole("main")
        .getByText(status === 403 ? /denied|permission/i : /not found/i)
        .first(),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: /Delete target|Save changes/ }),
    ).toHaveCount(0);
  });
}
