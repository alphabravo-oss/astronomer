import { test, expect } from "@playwright/test";
import {
  clusterId,
  now,
  pageOf,
  workflowAuth,
  jsonRoute,
  errorBody,
} from "./helpers/operator-workflows";
const id = "pipeline-workflow-1";
const outputId = "output-workflow-1";
const opaqueFilters = {
  custom_stage: {
    transforms: [
      { processor: "vendor-transform", options: { nested: [1, "unchanged"] } },
    ],
  },
};
const initial = {
  id,
  name: "Checkout logs",
  cluster_id: clusterId,
  namespaces: ["payments"],
  output_ids: [outputId],
  output_names: ["Archive logs"],
  filters: opaqueFilters,
  labels: { team: "payments", "vendor.example/opaque": "keep-me" },
  enabled: true,
  created_at: now,
  updated_at: now,
};
test.beforeEach(async ({ page, context }) => {
  await workflowAuth(page, context);
  await jsonRoute(
    page,
    `/api/v1/clusters/${clusterId}/namespaces`,
    pageOf([{ name: "payments", clusterId, status: "Active", createdAt: now }]),
  );
  await jsonRoute(
    page,
    "/api/v1/logging/outputs",
    pageOf([
      {
        id: outputId,
        name: "Archive logs",
        output_type: "s3",
        cluster_id: clusterId,
        configuration: { bucket: "fixture-archive" },
        is_system: false,
        capabilities: {
          ship: true,
          test: true,
          query: false,
          tail: false,
          aggregate: false,
          link_out: false,
          retention_visibility: false,
          query_mode: "shipping_only",
        },
        enabled: true,
        status: "ready",
        created_at: now,
        updated_at: now,
      },
    ]),
  );
});
test("pipeline edits keep its ID, opaque filters and labels through PUT", async ({
  page,
}, info) => {
  const mutations: { method: string; path: string }[] = [];
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname.replace(/\/$/, "");
    if (
      path.startsWith("/api/v1/logging/pipelines") &&
      request.method() !== "GET"
    ) {
      mutations.push({ method: request.method(), path });
    }
  });
  let record = { ...initial };
  const writes: { method: string; body: Record<string, unknown> }[] = [];
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") === `/api/v1/logging/pipelines/${id}`,
    (route) => {
      if (route.request().method() === "GET")
        return route.fulfill({ json: { data: record } });
      const body = route.request().postDataJSON();
      writes.push({ method: route.request().method(), body });
      record = { ...record, ...body };
      return route.fulfill({
        status: 202,
        json: {
          data: {
            pipeline: record,
            operation: { id: "pipeline-operation", status: "pending" },
          },
        },
      });
    },
  );
  await page.goto(`/dashboard/logging/pipelines/${id}`);
  await expect(
    page.getByRole("heading", { name: initial.name, exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Edit pipeline", exact: true }).click();
  await page
    .getByRole("textbox", { name: "Name", exact: true })
    .fill("Checkout logs revised");
  await expect(
    page.getByText(
      "This pipeline uses a custom filter configuration, preserved unchanged.",
    ),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Save pipeline", exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({
    method: "PUT",
    body: {
      name: "Checkout logs revised",
      cluster_id: clusterId,
      namespaces: ["payments"],
      output_ids: [outputId],
      filters: opaqueFilters,
      labels: initial.labels,
      enabled: true,
    },
  });
  expect(mutations).toEqual([
    { method: "PUT", path: `/api/v1/logging/pipelines/${id}` },
  ]);
  await expect(page).toHaveURL(new RegExp(`/logging/pipelines/${id}$`));
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Checkout logs revised", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Archive logs", { exact: true })).toBeVisible();
  await page.screenshot({
    path: info.outputPath("pipeline-roundtrip.png"),
    fullPage: true,
    animations: "disabled",
  });
});
test("failed saves retain draft and denied destination prevents destructive replacement", async ({
  page,
}, info) => {
  await page.route(
    (url) =>
      url.pathname.replace(/\/$/, "") === `/api/v1/logging/pipelines/${id}`,
    (route) =>
      route.request().method() === "GET"
        ? route.fulfill({ json: { data: initial } })
        : route.fulfill({
            status: 503,
            json: errorBody("Pipeline save fixture failure"),
          }),
  );
  await page.goto(`/dashboard/logging/pipelines/${id}/edit`);
  await page
    .getByRole("textbox", { name: "Name", exact: true })
    .fill("Retained draft");
  await page
    .getByRole("button", { name: "Save pipeline", exact: true })
    .click();
  await expect(
    page
      .getByRole("alert")
      .filter({ hasText: "Saving failed. Your edits are retained." }),
  ).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue("Retained draft");
  await page.route(
    (url) => url.pathname.replace(/\/$/, "") === "/api/v1/logging/outputs",
    (route) =>
      route.fulfill({
        status: 403,
        json: errorBody("Destination access denied"),
      }),
  );
  page.on("dialog", (dialog) => dialog.accept());
  await page.reload();
  await expect(
    page.getByRole("alert").filter({ hasText: outputId }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Save pipeline", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: info.outputPath("pipeline-denied-destination.png"),
    fullPage: true,
    animations: "disabled",
  });
});
