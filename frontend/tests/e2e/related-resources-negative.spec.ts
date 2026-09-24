import { expect, test } from "@playwright/test";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser } from "../e2e-smoke/stub-overrides";
import { installStubs } from "../e2e-smoke/stubs";

test("denied relationship continuation preserves Previous and never masquerades as empty", async ({
  page,
  context,
}) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  const requests: string[] = [];
  await page.route(
    "**/api/v1/clusters/c-smoke-1/k8s/api/v1/namespaces/app/services/web**",
    (route) =>
      route.fulfill({
        json: {
          apiVersion: "v1",
          kind: "Service",
          metadata: { name: "web", namespace: "app", uid: "service-web" },
          spec: { selector: { app: "web" }, ports: [{ port: 80 }] },
        },
      }),
  );
  await page.route(
    "**/api/v1/clusters/c-smoke-1/k8s/api/v1/namespaces/app/pods?*",
    (route) => {
      const url = new URL(route.request().url());
      expect(url.searchParams.get("limit")).toBe("50");
      const token = url.searchParams.get("continue") ?? "";
      requests.push(token);
      if (token)
        return route.fulfill({
          status: 403,
          json: { error: { message: "Relationship page denied" } },
        });
      return route.fulfill({
        json: {
          items: [
            {
              metadata: {
                name: "selected-pod",
                namespace: "app",
                uid: "pod-web",
                labels: { app: "web" },
              },
            },
            {
              metadata: {
                name: "unrelated-pod",
                namespace: "app",
                labels: { app: "other" },
              },
            },
          ],
          metadata: { continue: "next+/=" },
        },
      });
    },
  );
  await page.goto("/dashboard/clusters/c-smoke-1/services/app/web");
  await page.getByRole("tab", { name: "Related" }).click();
  await expect(
    page.getByRole("link", { name: "selected-pod" }),
  ).toHaveAttribute(
    "href",
    "/dashboard/clusters/c-smoke-1/pods/app/selected-pod",
  );
  await expect(page.getByRole("link", { name: "unrelated-pod" })).toHaveCount(
    0,
  );
  await page.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(
    page.getByText("Permission required", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "selected-pod" })).toHaveCount(0);
  await expect(
    page.getByText("No matching relationships on this page."),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Next page", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Previous", exact: true }).click();
  await expect(page.getByRole("link", { name: "selected-pod" })).toBeVisible();
  expect(requests).toContain("next+/=");
  expect(requests.at(-1)).toBe("");
});
