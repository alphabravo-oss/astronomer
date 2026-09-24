import { test, expect } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { seedAuth, authMeWire } from "./helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
test("project operator cannot request a denied namespace from an explicit deep link", async ({
  page,
  context,
}) => {
  await installStubs(page);
  const user = {
    ...adminStoreUser,
    isSuperuser: false,
    globalRoles: [],
    roles: {
      global: [
        {
          id: "reader",
          roleRules: [{ resource: "clusters", verbs: ["read", "list"] }],
        },
      ],
      cluster: [],
      project: [
        {
          id: "project-binding",
          projectId: "project-a",
          roleRules: [{ resource: "workloads", verbs: ["list", "read"] }],
        },
      ],
    },
  };
  await seedAuth(context, page, user);
  await page.route("**/api/v1/auth/me/", (route) =>
    route.fulfill({ json: { data: authMeWire(user) } }),
  );
  await page.route("**/api/v1/rbac/my-permissions**", (route) =>
    route.fulfill({
      json: {
        data: {
          subject: { user_id: user.id, self: true },
          superuser: false,
          context: { cluster_id: SMOKE_CLUSTER_ID },
          bindings: [
            {
              scope: "project",
              project_id: "project-a",
              namespace: "allowed",
              rules: [{ resource: "workloads", verbs: ["list", "read"] }],
            },
          ],
          permissions: [
            {
              resource: "workloads",
              verb: "read",
              sources: [
                {
                  scope: "project",
                  project_id: "project-a",
                  namespace: "allowed",
                },
              ],
            },
          ],
        },
      },
    }),
  );
  await page.route("**/api/v1/clusters/*/namespaces/**", (route) =>
    route.fulfill({
      json: {
        data: [
          {
            name: "allowed",
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
    }),
  );
  const requests: string[] = [];
  await page.route(
    /\/api\/v1\/clusters\/[^/]+\/workloads\/?(?:\?.*)?$/,
    (route) => {
      requests.push(route.request().url());
      return route.fulfill({
        json: {
          data: [
            {
              id: "forbidden-data",
              name: "forbidden-app",
              namespace: "denied",
            },
          ],
          pagination: {
            limit: 50,
            offset: 0,
            total: 1,
            has_more: false,
            next_offset: null,
          },
        },
      });
    },
  );
  await page.goto(
    `/dashboard/clusters/${SMOKE_CLUSTER_ID}/deployments?namespaces=denied`,
  );
  await expect(
    page.getByText("No namespaces selected", { exact: false }).first(),
  ).toBeVisible();
  expect(requests).toEqual([]);
  await expect(page.getByText("forbidden-app", { exact: true })).toHaveCount(0);
  await expect(
    page.locator("header").getByRole("button", { name: /Namespace scope/ }),
  ).toBeEnabled();
});
