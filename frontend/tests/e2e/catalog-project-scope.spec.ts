import { expect, test } from "@playwright/test";
import { installStubs } from "../e2e-smoke/stubs";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
import { seedAuth } from "./helpers/auth";

const projects = Array.from({ length: 226 }, (_, i) => ({
  id: `project-${i + 1}`,
  name: `project-${i + 1}`,
  display_name: `Project ${i + 1}`,
  cluster_id: SMOKE_CLUSTER_ID,
  resource_quota: {},
  namespaces: [],
  created_at: "2026-09-22T00:00:00Z",
  updated_at: "2026-09-22T00:00:00Z",
}));
for (const status of [403, 404, 503]) {
  test(`Catalog deep-link ${status} keeps the project picker available`, async ({
    page,
    context,
  }) => {
    await installStubs(page);
    await seedAuth(context, page, adminStoreUser);
    await page.route("**/api/v1/projects/project-225/", (route) =>
      route.fulfill({
        status,
        json: { error: { message: "Project unavailable" } },
      }),
    );
    await page.goto("/dashboard/catalog?project=project-225");
    await expect(
      page.getByText(
        status === 403
          ? "Permission required"
          : status === 404
            ? "Project not found"
            : "Failed to load catalog project",
        { exact: true },
      ),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Catalog project", exact: true })
      .click();
    await expect(
      page.getByRole("dialog", { name: "Select catalog project" }),
    ).toBeVisible();
    await expect(page).toHaveURL(/project=project-225/);
    expect(await page.pageErrors()).toEqual([]);
  });
}
for (const clusterScoped of [false, true]) {
  test(`${clusterScoped ? "Apps" : "Catalog"} project picker reaches beyond 200 and preserves its deep link`, async ({
    page,
    context,
  }) => {
    await installStubs(page);
    await seedAuth(context, page, adminStoreUser);
    const listPath = clusterScoped
      ? `/clusters/${SMOKE_CLUSTER_ID}/projects`
      : "/projects";
    const calls: URL[] = [];
    const catalogScopes: string[] = [];
    await page.route("**/api/v1/**", async (route) => {
      const url = new URL(route.request().url());
      const path = url.pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "");
      if (path === listPath) {
        calls.push(url);
        const limit = Number(url.searchParams.get("limit"));
        const offset = Number(url.searchParams.get("offset"));
        const term = url.searchParams.get("search") || "";
        const matches = projects.filter((p) => p.name.includes(term));
        const hasMore = offset + limit < matches.length;
        return route.fulfill({
          json: {
            data: matches.slice(offset, offset + limit),
            pagination: {
              limit,
              offset,
              total: matches.length,
              has_more: hasMore,
              next_offset: hasMore ? offset + limit : null,
            },
          },
        });
      }
      if (path.startsWith("/projects/project-")) {
        return route.fulfill({
          json: { data: projects.find((p) => path === `/projects/${p.id}`) },
        });
      }
      if (path === "/catalog/charts") {
        catalogScopes.push(url.searchParams.get("project_id") || "");
        return route.fulfill({
          json: {
            data: [],
            pagination: {
              limit: 60,
              offset: 0,
              total: 0,
              has_more: false,
              next_offset: null,
            },
          },
        });
      }
      return route.fallback();
    });
    const path = clusterScoped
      ? `/dashboard/clusters/${SMOKE_CLUSTER_ID}/apps?section=browse`
      : "/dashboard/catalog";
    await page.goto(path);
    await page
      .getByRole("button", { name: "Catalog project", exact: true })
      .click();
    const dialog = page.getByRole("dialog", { name: "Select catalog project" });
    await expect(
      dialog.getByRole("button", { name: "Select Project 1", exact: true }),
    ).toBeVisible();
    await dialog.getByRole("button", { name: "Next page" }).click();
    await expect(
      dialog.getByRole("button", { name: "Select Project 26", exact: true }),
    ).toBeVisible();
    await dialog.getByPlaceholder("Search...").fill("project-225");
    await dialog
      .getByRole("button", { name: "Select Project 225", exact: true })
      .click();
    await expect(page).toHaveURL(/project=project-225/);
    await expect.poll(() => catalogScopes.includes("project-225")).toBe(true);
    await page.reload();
    await expect(
      page.getByRole("button", { name: "Catalog project", exact: true }),
    ).toHaveText("Project 225");
    expect(
      calls.every((url) =>
        ["2", "25"].includes(url.searchParams.get("limit") || ""),
      ),
    ).toBe(true);
    expect(calls.some((url) => url.searchParams.get("offset") === "25")).toBe(
      true,
    );
    expect(
      calls.some(
        (url) =>
          url.searchParams.get("search") === "project-225" &&
          url.searchParams.get("offset") === "0",
      ),
    ).toBe(true);
    expect(await page.pageErrors()).toEqual([]);
  });
}
