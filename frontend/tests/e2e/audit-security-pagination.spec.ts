import { expect, test } from "@playwright/test";
import { seedAuth } from "./helpers/auth";
import { adminStoreUser } from "../e2e-smoke/stub-overrides";
import { installStubs } from "../e2e-smoke/stubs";

test.beforeEach(async ({ page, context }) => {
  await installStubs(page);
  await seedAuth(context, page, adminStoreUser);
});

test("CIS history reaches beyond 200 and labels page-only summaries", async ({
  page,
}) => {
  const offsets: number[] = [];
  await page.route("**/api/v1/security/scans/?*", (route) => {
    const url = new URL(route.request().url());
    const offset = Number(url.searchParams.get("offset"));
    const limit = Number(url.searchParams.get("limit"));
    offsets.push(offset);
    return route.fulfill({
      json: {
        data: Array.from({ length: Math.min(limit, 226 - offset) }, (_, i) => ({
          id: `scan-${offset + i}`,
          cluster_id: "c-smoke-1",
          scan_type: `cis-${offset + i}`,
          status: "completed",
          passed: 2,
          failed: 1,
          warned: 0,
          skipped: 0,
          created_at: "2026-09-22T00:00:00Z",
        })),
        pagination: {
          total: 226,
          limit,
          offset,
          has_more: offset + limit < 226,
          next_offset: offset + limit < 226 ? offset + limit : null,
        },
      },
    });
  });
  await page.goto("/dashboard/security?tab=cis");
  await expect(page.getByText("Check totals on this page")).toBeVisible();
  for (let i = 1; i <= 9; i++) {
    await page.getByRole("button", { name: "Next page" }).click();
    await expect(
      page.getByText(`cis-${i * 25}`, { exact: true }),
    ).toBeVisible();
  }
  await expect(page.getByRole("button", { name: "Next page" })).toBeDisabled();
  expect(offsets).toContain(225);
});

test("Audit remote target selection resets paging and retains known-ID filters", async ({
  page,
}) => {
  const requests: {
    project: string | null;
    cluster: string | null;
    offset: number;
  }[] = [];
  await page.route("**/api/v1/audit/?*", (route) => {
    const url = new URL(route.request().url());
    const offset = Number(url.searchParams.get("offset"));
    requests.push({
      project: url.searchParams.get("project_id"),
      cluster: url.searchParams.get("cluster_id"),
      offset,
    });
    return route.fulfill({
      json: {
        data: [
          {
            id: `a-${offset}`,
            action: "cluster.update",
            timestamp: "2026-09-22T00:00:00Z",
            user: "operator",
            status: "success",
          },
        ],
        pagination: {
          limit: 50,
          offset,
          total: 101,
          has_more: offset < 100,
          next_offset: offset + 50,
        },
      },
    });
  });
  await page.route("**/api/v1/projects/?*", (route) => {
    const offset = Number(
      new URL(route.request().url()).searchParams.get("offset"),
    );
    return route.fulfill({
      json: {
        data: [
          {
            id: `project-${offset}`,
            name: `Project ${offset}`,
            display_name: `Project ${offset}`,
            resource_quota: {},
            namespaces: [],
          },
        ],
        pagination: {
          limit: 25,
          offset,
          total: 226,
          has_more: offset < 225,
          next_offset: offset + 25,
        },
      },
    });
  });
  await page.goto("/dashboard/audit");
  await page.getByRole("button", { name: "Next page" }).click();
  await expect.poll(() => requests.at(-1)?.offset).toBe(50);
  await page.getByRole("button", { name: "Filters", exact: true }).click();
  await page.getByRole("button", { name: "Find project" }).click();
  const picker = page.getByRole("dialog", { name: "Select find project" });
  for (let i = 1; i <= 9; i++) {
    await picker.getByRole("button", { name: "Next page" }).click();
    await expect(
      picker.getByRole("button", {
        name: `Select Project ${i * 25}`,
        exact: true,
      }),
    ).toBeVisible();
  }
  await picker
    .getByRole("button", { name: "Select Project 225", exact: true })
    .click();
  await expect
    .poll(() => requests.at(-1))
    .toEqual({ project: "project-225", cluster: null, offset: 0 });
  await page.getByRole("textbox", { name: "Cluster ID" }).fill("known-cluster");
  await expect
    .poll(() => requests.at(-1))
    .toEqual({ project: "project-225", cluster: "known-cluster", offset: 0 });
});

test("CVE detail continues past 100, resets severity and distinguishes a denied page", async ({
  page,
}) => {
  const report = {
    id: "report-page",
    cluster_id: "c-smoke-1",
    image_repo: "paged-image",
    image_tag: "v1",
    namespace: "default",
    workload_kind: "Deployment",
    workload_name: "app",
    critical_count: 0,
    high_count: 126,
    medium_count: 0,
    low_count: 0,
    unknown_count: 0,
    scanned_at: "2026-09-22T00:00:00Z",
  };
  const requests: { offset: number; severity: string | null }[] = [];
  await page.route(
    "**/api/v1/clusters/c-smoke-1/vulnerabilities/images/?*",
    (route) =>
      route.fulfill({
        json: {
          data: [report],
          pagination: {
            limit: 20,
            offset: 0,
            total: 1,
            has_more: false,
            next_offset: null,
          },
        },
      }),
  );
  await page.route(
    "**/api/v1/clusters/c-smoke-1/vulnerabilities/reports/report-page/?*",
    (route) => {
      const url = new URL(route.request().url());
      const offset = Number(url.searchParams.get("offset"));
      const severity = url.searchParams.get("severity");
      requests.push({ offset, severity });
      if (severity === "HIGH" && offset === 25)
        return route.fulfill({
          status: 403,
          json: { error: { code: "FORBIDDEN", message: "Denied" } },
        });
      return route.fulfill({
        json: {
          data: {
            report,
            severity_filter: severity ?? "",
            vulnerabilities: {
              data: Array.from(
                { length: Math.min(25, 126 - offset) },
                (_, i) => ({
                  id: `v-${offset + i}`,
                  report_id: report.id,
                  vulnerability_id: `CVE-2026-${1000 + offset + i}`,
                  severity: "HIGH",
                  pkg_name: "lib-example",
                  installed_version: "1",
                  fixed_version: "2",
                  primary_link: "https://example.test/cve",
                  cvss_score: 7.5,
                  title: "Finding",
                  description: "",
                }),
              ),
              pagination: {
                limit: 25,
                offset,
                has_more: offset < 125,
                next_offset: offset < 125 ? offset + 25 : null,
              },
            },
          },
        },
      });
    },
  );
  await page.goto("/dashboard/clusters/c-smoke-1/image-scans");
  const opener = page.getByRole("button", {
    name: "View CVEs for paged-image:v1",
  });
  await opener.press("Enter");
  const drawer = page.getByRole("dialog", { name: "paged-image:v1" });
  await expect(drawer).toBeVisible();
  // Visibility alone does not detect a sticky toolbar covering the header.
  await drawer.getByRole("button", { name: "Close", exact: true }).click();
  await expect(drawer).not.toBeVisible();
  await expect(opener).toBeFocused();
  await opener.press("Enter");
  await expect(drawer).toBeVisible();
  const cves = page.getByRole("region", { name: "Report CVEs" });
  await expect(
    cves.getByText("At least 26 CVEs matching filter"),
  ).toBeVisible();
  await page.screenshot({ path: test.info().outputPath("cve-drawer.png") });
  for (let i = 1; i <= 5; i++) {
    await cves.getByRole("button", { name: "Next page" }).click();
    await expect(
      cves.getByText(`CVE-2026-${1000 + i * 25}`, { exact: true }),
    ).toBeVisible();
  }
  await expect(cves.getByText("126 CVEs matching filter")).toBeVisible();
  await drawer.getByLabel("CVE severity").selectOption("HIGH");
  await expect
    .poll(() => requests.at(-1))
    .toEqual({ offset: 0, severity: "HIGH" });
  await cves.getByRole("button", { name: "Next page" }).click();
  await expect(cves.getByText(/Permission required/)).toBeVisible();
  await expect(cves.getByText("CVE-2026-1000", { exact: true })).toHaveCount(0);
  await cves.getByRole("button", { name: "Previous page" }).click();
  await expect(cves.getByText("CVE-2026-1000", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(drawer).not.toBeVisible();
  await expect(opener).toBeFocused();
});
