import { expect, test } from "@playwright/test";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser } from "../e2e-smoke/stub-overrides";
import { installStubs } from "../e2e-smoke/stubs";

const userId = "00000000-0000-4000-8000-000000000001";
const clusterId = "00000000-0000-4000-8000-000000000002";
const grant = {
  id: "grant-exact",
  userId,
  clusterId,
  namespace: "app",
  apiGroup: "",
  resource: "configmaps",
  verbs: ["read"],
  createdAt: "2026-09-22T00:00:00Z",
};
test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});

for (const status of [403, 404]) {
  test(`native grants distinguish ${status} from an empty inventory`, async ({
    page,
  }) => {
    await page.route("**/api/v1/native-rbac-rules**", (route) =>
      route.fulfill({
        status,
        json: { error: { message: "Unavailable" } },
      }),
    );
    await page.goto("/dashboard/rbac?tab=native-rules");
    await expect(
      page.getByText(
        status === 403 ? "Permission required" : "Native RBAC is unavailable",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Create native grant" }),
    ).toBeDisabled();
    await expect(
      page.getByText("No native grants", { exact: true }),
    ).toHaveCount(0);
  });
}

test("native grant review sends exact scope, preserves denied confirmations and removes only the selected grant", async ({
  page,
}) => {
  let exists = false;
  const creates: unknown[] = [];
  const deletes: string[] = [];
  await page.route("**/api/v1/clusters/?*", (route) =>
    route.fulfill({
      json: {
        data: [
          {
            id: clusterId,
            name: "grant-cluster",
            display_name: "Grant Cluster",
            status: "active",
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
  await page.route(`**/api/v1/clusters/${clusterId}/`, (route) =>
    route.fulfill({
      json: {
        data: {
          id: clusterId,
          name: "grant-cluster",
          display_name: "Grant Cluster",
          status: "active",
        },
      },
    }),
  );
  await page.route("**/api/v1/native-rbac-rules**", (route) => {
    const request = route.request();
    if (request.method() === "POST") {
      creates.push(request.postDataJSON());
      if (creates.length === 1)
        return route.fulfill({
          status: 403,
          json: { error: { message: "Grant exceeds authority" } },
        });
      exists = true;
      return route.fulfill({ status: 201, json: { data: grant } });
    }
    if (request.method() === "DELETE") {
      deletes.push(new URL(request.url()).pathname);
      if (deletes.length === 1)
        return route.fulfill({
          status: 403,
          json: { error: { message: "Removal denied" } },
        });
      exists = false;
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({
      json: {
        data: exists ? [grant] : [],
        pagination: {
          total: exists ? 1 : 0,
          limit: 25,
          offset: 0,
          has_more: false,
          next_offset: null,
        },
      },
    });
  });
  await page.goto("/dashboard/rbac?tab=native-rules");
  await expect(
    page.getByText("No native grants", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /^Sort by/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Create native grant" }).click();
  const form = page.getByRole("dialog", { name: "New native resource grant" });
  await form.getByLabel("User UUID").fill(userId);
  await form.getByRole("combobox").click();
  await form.getByRole("option", { name: /Grant Cluster/ }).click();
  await expect(form.getByRole("combobox")).toBeFocused();
  await form.getByLabel(/^Namespace/).fill("app");
  await form.getByLabel("Plural resource").fill("configmaps");
  await form.getByRole("button", { name: "Review grant" }).click();
  const review = page.getByRole("dialog", {
    name: "Grant native resource access",
  });
  await expect(review).toContainText(clusterId);
  await expect(
    review.getByRole("button", { name: "Grant access" }),
  ).toBeDisabled();
  expect(creates).toHaveLength(0);
  await review.getByPlaceholder("grant access").fill("grant access");
  await review.getByRole("button", { name: "Grant access" }).click();
  await expect(
    page.getByText("Could not create grant: Grant exceeds authority", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(review).toBeVisible();
  await expect(
    page.getByText("Native resource grant created", { exact: true }),
  ).toHaveCount(0);
  await review.getByRole("button", { name: "Grant access" }).click();
  await expect(review).not.toBeVisible();
  expect(creates).toEqual([
    {
      userId,
      clusterId,
      namespace: "app",
      apiGroup: "",
      resource: "configmaps",
      verbs: ["read"],
    },
    {
      userId,
      clusterId,
      namespace: "app",
      apiGroup: "",
      resource: "configmaps",
      verbs: ["read"],
    },
  ]);
  await page.getByRole("button", { name: "Remove grant" }).click();
  const removal = page.getByRole("dialog", { name: "Remove native grant" });
  await expect(
    removal.getByRole("button", { name: "Remove grant" }),
  ).toBeDisabled();
  expect(deletes).toHaveLength(0);
  await removal.getByPlaceholder("remove grant").fill("remove grant");
  await removal.getByRole("button", { name: "Remove grant" }).click();
  await expect(
    page.getByText("Could not remove grant: Removal denied", { exact: true }),
  ).toBeVisible();
  await expect(removal).toBeVisible();
  await expect(
    page.getByText("Native resource grant removed", { exact: true }),
  ).toHaveCount(0);
  await removal.getByRole("button", { name: "Remove grant" }).click();
  await expect(removal).not.toBeVisible();
  await expect(
    page.getByText("No native grants", { exact: true }),
  ).toBeVisible();
  expect(deletes).toEqual([
    "/api/v1/native-rbac-rules/grant-exact/",
    "/api/v1/native-rbac-rules/grant-exact/",
  ]);
});
