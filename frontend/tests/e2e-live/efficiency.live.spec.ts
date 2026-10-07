import {
  expect,
  test,
  type Page,
  type Response,
  type TestInfo,
} from "@playwright/test";
import { EngineeringRecorder } from "../benchmark/engineering-recorder";
import {
  completeSearchFixture,
  type Journey,
} from "../benchmark/engineering-metrics";
import {
  loginViaForm,
  loginViaFormAs,
  waitForStreamOpen,
} from "./live.helpers";
// Opt-in sidecar only: screenshots/traces/video would retain unsanitized content.
test.use({ trace: "off", screenshot: "off", video: "off" });
test.setTimeout(240_000);
const enabled = process.env.LIVE_BROWSER_ENGINEERING === "1";
const duration = 45_000;
function fixture(name: string) {
  const value = process.env[name];
  if (!value) throw new Error("required engineering fixture is missing");
  return value;
}
test.beforeEach(({ page }) => {
  void page;
  if (!enabled)
    throw new Error(
      "Engineering measurements require LIVE_BROWSER_ENGINEERING=1",
    );
  fixture("LIVE_FIXTURE_CLUSTER_ID");
  fixture("LIVE_FIXTURE_PROJECT_ID");
  fixture("LIVE_FIXTURE_BACKUP_NAMESPACE");
  fixture("LIVE_FIXTURE_BACKUP_CONFIGMAP");
});
async function measure(
  page: Page,
  info: TestInfo,
  journey: Journey,
  action: () => Promise<void>,
  slot: "primary" | "secondary" = "primary",
) {
  let declared: unknown;
  try {
    declared = process.env.LIVE_BROWSER_ENGINEERING_PROVENANCE
      ? JSON.parse(process.env.LIVE_BROWSER_ENGINEERING_PROVENANCE)
      : undefined;
  } catch {
    throw new Error("invalid engineering provenance");
  }
  const deadline = Date.now() + duration;
  const recorder = await EngineeringRecorder.start(
    page,
    new URL(page.url()).origin,
    journey,
    duration,
    declared,
  );
  try {
    await recorder.interaction(action);
    await page.waitForTimeout(Math.max(0, deadline - Date.now()));
  } finally {
    const artifact = await recorder.finish();
    await info.attach(`engineering-${journey}-${slot}.json`, {
      contentType: "application/json",
      body: JSON.stringify({ ...artifact, page_slot: slot }),
    });
  }
}
async function data(response: Response) {
  if (response.status() !== 200 || (await response.finished()))
    throw new Error("engineering response did not complete successfully");
  const payload = await response.json();
  return payload.data;
}
async function clusterReady(page: Page) {
  const response = page.waitForResponse(
    (res) => new URL(res.url()).pathname === "/api/v1/clusters/",
  );
  await page.goto("/dashboard/clusters");
  const rows = await data(await response);
  const row = Array.isArray(rows)
    ? rows.find((row) => row.id === fixture("LIVE_FIXTURE_CLUSTER_ID"))
    : undefined;
  if (!row || typeof row.name !== "string")
    throw new Error("engineering fixture cluster absent");
  await expect(
    page.getByRole("row").filter({ hasText: row.name }).first(),
  ).toBeVisible();
}
async function searchReady(page: Page, response: Promise<Response>) {
  const result = await data(await response);
  const namespace = fixture("LIVE_FIXTURE_BACKUP_NAMESPACE");
  const name = fixture("LIVE_FIXTURE_BACKUP_CONFIGMAP");
  if (
    !completeSearchFixture(
      result,
      fixture("LIVE_FIXTURE_CLUSTER_ID"),
      namespace,
      name,
    )
  )
    throw new Error("engineering search incomplete or fixture absent");
  await expect(
    page
      .getByRole("row")
      .filter({ hasText: name })
      .filter({ hasText: namespace })
      .first(),
  ).toBeVisible();
}
for (const journey of [
  "cluster_list",
  "resource_list",
  "delivery_inventory",
  "scoped_search",
] as const) {
  test(`engineering ${journey}`, async ({ page }, info) => {
    await loginViaForm(page);
    await measure(page, info, journey, async () => {
      const cluster = encodeURIComponent(fixture("LIVE_FIXTURE_CLUSTER_ID"));
      const project = encodeURIComponent(fixture("LIVE_FIXTURE_PROJECT_ID"));
      if (journey === "cluster_list") await clusterReady(page);
      else if (journey === "resource_list") {
        const name = fixture("LIVE_FIXTURE_BACKUP_CONFIGMAP"),
          namespace = fixture("LIVE_FIXTURE_BACKUP_NAMESPACE");
        const response = page.waitForResponse((res) => {
          const url = new URL(res.url());
          return (
            url.pathname ===
              `/api/v1/clusters/${cluster}/resources/generic/configmaps` &&
            url.searchParams.get("search") === name
          );
        });
        await page.goto(
          `/dashboard/clusters/${cluster}/configmaps?project=${project}`,
        );
        await page.getByPlaceholder("Search configmaps...").fill(name);
        const rows = await data(await response);
        if (
          !Array.isArray(rows) ||
          !rows.some(
            (item) => item.name === name && item.namespace === namespace,
          )
        )
          throw new Error("engineering fixture resource absent");
        await expect(
          page
            .getByRole("row")
            .filter({ hasText: name })
            .filter({ hasText: namespace })
            .first(),
        ).toBeVisible();
      } else if (journey === "delivery_inventory") {
        const response = page.waitForResponse((res) =>
          /\/delivery\/clusters\/[^/]+\/inventory\//.test(
            new URL(res.url()).pathname,
          ),
        );
        await page.goto(
          `/dashboard/clusters/${cluster}/delivery/system-components?project=${project}`,
        );
        const result = await data(await response);
        const component = result?.controller_inventory?.system_components?.[0];
        if (!component || typeof component.name !== "string")
          throw new Error("engineering component fixture absent");
        await expect(
          page.getByRole("row").filter({ hasText: component.name }).first(),
        ).toBeVisible();
      } else {
        const response = page.waitForResponse(
          (res) => new URL(res.url()).pathname === "/api/v1/resources/search",
        );
        await page.goto(
          `/dashboard/search?type=configmaps&namespace=${encodeURIComponent(fixture("LIVE_FIXTURE_BACKUP_NAMESPACE"))}`,
        );
        await searchReady(page, response);
      }
    });
  });
}
test("engineering two tabs record their actual visibility independently", async ({
  page,
  context,
}, info) => {
  await loginViaForm(page);
  const second = await context.newPage();
  await second.goto("/dashboard/clusters");
  try {
    await Promise.all([
      measure(page, info, "two_tabs", async () => {
        await clusterReady(page);
      }),
      measure(
        second,
        info,
        "two_tabs",
        async () => {
          await clusterReady(second);
        },
        "secondary",
      ),
    ]);
  } finally {
    await second.close();
  }
});
test("engineering rapid namespace scope change ends on the requested scope", async ({
  page,
}, info) => {
  await loginViaForm(page);
  await page.goto("/dashboard/search?type=configmaps");
  await measure(page, info, "rapid_scope", async () => {
    const namespace = fixture("LIVE_FIXTURE_BACKUP_NAMESPACE");
    const response = page.waitForResponse((res) => {
      const url = new URL(res.url());
      return (
        url.pathname === "/api/v1/resources/search" &&
        url.searchParams.get("namespace") === namespace &&
        res.status() === 200
      );
    });
    await page.getByPlaceholder("namespace (optional)").fill("default");
    await page.getByPlaceholder("namespace (optional)").fill(namespace);
    await searchReady(page, response);
    await expect(page.getByPlaceholder("namespace (optional)")).toHaveValue(
      namespace,
    );
  });
});
test("engineering stream open, stream-only failure, and recovery are separate windows", async ({
  page,
}, info) => {
  await loginViaForm(page);
  await measure(page, info, "stream_open", async () => {
    const open = waitForStreamOpen(page);
    await clusterReady(page);
    await open;
  });
  const streamPattern = "**/api/v1/events/stream/**";
  await page.route(streamPattern, (route) => route.abort("failed"));
  try {
    await measure(page, info, "stream_fallback", async () => {
      let reads = 0,
        failures = 0;
      const response = (res: import("@playwright/test").Response) => {
        if (
          new URL(res.url()).pathname === "/api/v1/clusters/" &&
          res.status() === 200
        )
          void data(res)
            .then((rows) => {
              if (
                Array.isArray(rows) &&
                rows.some(
                  (row) => row.id === fixture("LIVE_FIXTURE_CLUSTER_ID"),
                )
              )
                reads++;
              else failures++;
            })
            .catch(() => {
              failures++;
            });
      };
      page.on("response", response);
      try {
        await clusterReady(page);
        await expect.poll(() => reads, { timeout: 40_000 }).toBeGreaterThan(1);
        expect(
          failures,
          "engineering fallback reads must complete with fixture data",
        ).toBe(0);
      } finally {
        page.off("response", response);
      }
    });
  } finally {
    await page.unroute(streamPattern);
  }
  await measure(page, info, "stream_recovered", async () => {
    const open = waitForStreamOpen(page);
    await clusterReady(page);
    await open;
    await expect(page.getByPlaceholder("Search clusters...")).toBeVisible();
  });
});
test("engineering denied journey retains explicit authorization failure", async ({
  page,
}, info) => {
  await loginViaFormAs(
    page,
    fixture("LIVE_RESTRICTED_EMAIL"),
    fixture("LIVE_RESTRICTED_PASSWORD"),
  );
  await measure(page, info, "denied", async () => {
    await page.goto("/dashboard/cluster-templates");
    await expect(
      page.getByText("Permission required", { exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: /create/i })).toHaveCount(0);
  });
});
