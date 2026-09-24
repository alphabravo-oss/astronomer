import { expect, test, type Page } from "@playwright/test";
import { authMeWire, seedAuth } from "../e2e/helpers/auth";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "./stub-overrides";
import { installStubs } from "./stubs";

const defaultUserPreferences = {
  theme: "light",
  table_density: "comfortable",
  landing_route: "/dashboard",
  time_format: "locale",
  favorites: [],
  pinned_clusters: [],
  starred_types: [] as string[],
  rows_per_page: 25,
  date_format: "locale",
};

const clusterURL = `/dashboard/clusters/${SMOKE_CLUSTER_ID}`;
const discoveryPath = `/api/v1/clusters/${SMOKE_CLUSTER_ID}/k8s/apis/apiextensions.k8s.io/v1/customresourcedefinitions`;

async function openMore(page: Page, mobile: boolean) {
  if (mobile)
    await page
      .getByRole("button", { name: "Open navigation", exact: true })
      .click();
  const more = page
    .locator("aside")
    .getByRole("button", { name: "More Resources", exact: true });
  if ((await more.getAttribute("aria-expanded")) !== "true") await more.click();
  return page.locator("aside");
}

test("star and unstar persist through a fresh page load", async ({
  page,
  context,
}, testInfo) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  let stored = { ...defaultUserPreferences, theme: "light" as const };
  const writes: string[][] = [];
  await page.route("**/api/v1/auth/me/preferences**", async (route) => {
    if (route.request().method() === "PUT") {
      stored = route.request().postDataJSON();
      writes.push(stored.starred_types ?? []);
    }
    await route.fulfill({ json: { data: stored } });
  });
  const mobile = testInfo.project.name.includes("mobile");
  await page.goto(clusterURL);
  const sidebar = await openMore(page, mobile);
  await sidebar
    .getByRole("button", { name: "Star Certificate", exact: true })
    .click();
  await expect.poll(() => writes).toEqual([["cert-manager.io/certificates"]]);
  await expect(
    sidebar
      .getByRole("button", { name: "Unstar Certificate", exact: true })
      .first(),
  ).toBeEnabled();
  await page.reload();
  await openMore(page, mobile);
  await expect(
    sidebar.getByRole("button", { name: "Starred", exact: true }),
  ).toBeVisible();
  await sidebar
    .getByRole("button", { name: "Unstar Certificate", exact: true })
    .first()
    .click();
  await expect
    .poll(() => writes)
    .toEqual([["cert-manager.io/certificates"], []]);
  await expect(
    sidebar.getByRole("button", { name: "Star Certificate", exact: true }),
  ).toBeEnabled();
  await page.reload();
  await openMore(page, mobile);
  await expect(
    sidebar.getByRole("button", { name: "Starred", exact: true }),
  ).toHaveCount(0);
  await expect(
    sidebar.getByRole("button", { name: "Star Certificate", exact: true }),
  ).toBeEnabled();
});

test("a denied preference write rolls back the star and reports the error", async ({
  page,
  context,
}, testInfo) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  await page.route("**/api/v1/auth/me/preferences**", async (route) => {
    await route.fulfill(
      route.request().method() === "PUT"
        ? {
            status: 403,
            json: { error: { message: "Preference write denied" } },
          }
        : { json: { data: { ...defaultUserPreferences, theme: "light" } } },
    );
  });
  await page.goto(clusterURL);
  const sidebar = await openMore(
    page,
    testInfo.project.name.includes("mobile"),
  );
  await sidebar
    .getByRole("button", { name: "Star Certificate", exact: true })
    .click();
  await expect(
    page.getByText(
      "Failed to save navigation preferences: Preference write denied",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    sidebar.getByRole("button", { name: "Star Certificate", exact: true }),
  ).toBeEnabled();
  await expect(
    sidebar.getByRole("button", { name: "Starred", exact: true }),
  ).toHaveCount(0);
});

test("counts load on first opening the desktop flyout or mobile drawer group", async ({
  page,
  context,
}, testInfo) => {
  const mobile = testInfo.project.name.includes("mobile");
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
  const requests: string[] = [];
  await page.route(
    `**/api/v1/clusters/${SMOKE_CLUSTER_ID}/k8s/apis/cert-manager.io/v1/certificates**`,
    async (route) => {
      requests.push(route.request().url());
      await route.fulfill({
        json: {
          kind: "PartialObjectMetadataList",
          items: [{}],
          metadata: { remainingItemCount: 6 },
        },
      });
    },
  );
  await page.goto(clusterURL);
  const sidebar = page.locator("aside");
  if (mobile)
    await page
      .getByRole("button", { name: "Open navigation", exact: true })
      .click();
  await expect(
    sidebar.getByRole("button", { name: "More Resources", exact: true }),
  ).toHaveAttribute("aria-expanded", "false");
  if (!mobile)
    await sidebar
      .getByRole("button", { name: "Collapse sidebar", exact: true })
      .click();
  expect(requests).toEqual([]);
  await sidebar
    .getByRole("button", { name: "More Resources", exact: true })
    .click();
  const flyout = mobile
    ? sidebar
    : page.getByRole("navigation", {
        name: "More Resources",
        exact: true,
      });
  await expect(flyout.getByRole("link", { name: /Certificate/ })).toHaveText(
    /Certificate\s*7/,
  );
  expect(requests).toHaveLength(1);
  await page.keyboard.press("Escape");
  if (!mobile) {
    await expect(flyout).toHaveCount(0);
    await expect(
      sidebar.getByRole("button", { name: "More Resources", exact: true }),
    ).toBeFocused();
  }
});

test("a cluster viewer without custom-resource permission never requests discovery", async ({
  page,
  context,
}, testInfo) => {
  const viewer = {
    ...adminStoreUser,
    id: "limited-viewer",
    isSuperuser: false,
    globalRoles: [],
    roles: {
      global: [],
      project: [],
      cluster: [
        {
          id: "viewer-binding",
          roleId: "viewer-role",
          clusterId: SMOKE_CLUSTER_ID,
          roleName: "viewer",
          roleRules: [{ resource: "clusters", verbs: ["read"] }],
        },
      ],
    },
  };
  await installStubs(page, [
    {
      method: "GET",
      path: "/api/v1/auth/me",
      body: { data: authMeWire(viewer) },
    },
  ]);
  await seedAuth(context, page, viewer);
  const requests: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === discoveryPath)
      requests.push(request.url());
  });
  await page.goto(clusterURL);
  if (testInfo.project.name.includes("mobile"))
    await page
      .getByRole("button", { name: "Open navigation", exact: true })
      .click();
  await expect(
    page.locator("aside").getByRole("link", { name: "Overview", exact: true }),
  ).toBeVisible();
  const sidebar = page.locator("aside");
  await sidebar
    .getByRole("button", { name: "More Resources", exact: true })
    .click();
  await expect(
    sidebar.getByRole("link", { name: "Custom Resources", exact: true }),
  ).toHaveCount(0);
  await expect(sidebar.getByRole("link", { name: /Certificate/ })).toHaveCount(
    0,
  );
  expect(requests).toEqual([]);
});
