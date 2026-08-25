import { beforeEach, describe, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/lib/api/transport", () => ({ default: { request } }));

import {
  createDatasource,
  createWidget,
  deleteDatasource,
  deleteWidget,
  getWidget,
  listDatasources,
  listWidgets,
  renderForCluster,
  renderForProject,
  renderGlobal,
  testDatasource,
  updateDatasource,
  updateWidget,
  type DatasourceWriteBody,
  type WidgetWriteBody,
} from "./dashboards";

const WIDGET_WIRE = {
  id: "10000000-0000-0000-0000-000000000001",
  name: "API latency",
  description: "p99 latency",
  widget_type: "prom_stat",
  spec: { datasource: "default", query: "up", unit: "s" },
  scope: "global",
  scope_ids: [],
  grid: { x: 0, y: 0, w: 4, h: 2 },
  refresh_seconds: 60,
  enabled: true,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
} as const;

const DATASOURCE_WIRE = {
  id: "20000000-0000-0000-0000-000000000001",
  name: "default",
  url: "https://prometheus.example.test",
  has_auth: true,
  tls_skip_verify: false,
  enabled: true,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
} as const;

const RENDERED_WIRE = {
  id: WIDGET_WIRE.id,
  name: WIDGET_WIRE.name,
  widget_type: "prom_stat",
  spec_resolved: WIDGET_WIRE.spec,
  grid: WIDGET_WIRE.grid,
  refresh_seconds: 60,
  data: { stat_value: 0.42, stat_unit: "s", stat_format: ".2f", stat_ok: true },
} as const;

const WIDGET_BODY: WidgetWriteBody = {
  name: WIDGET_WIRE.name,
  description: WIDGET_WIRE.description,
  widget_type: "prom_stat",
  spec: WIDGET_WIRE.spec,
  scope: "global",
  scope_ids: [],
  grid: WIDGET_WIRE.grid,
  refresh_seconds: 60,
  enabled: true,
};

const DATASOURCE_BODY: DatasourceWriteBody = {
  name: DATASOURCE_WIRE.name,
  url: DATASOURCE_WIRE.url,
  bearer_token: "secret",
  tls_skip_verify: false,
  enabled: true,
};

function lastRequest() {
  return request.mock.calls.at(-1)?.[0];
}

beforeEach(() => {
  request.mockReset();
  request.mockImplementation(async (config) => {
    const url = String(config.url);
    if (config.method === "DELETE") return { data: undefined };
    if (url === "/api/v1/admin/dashboard-widgets" && config.method === "GET") {
      return { data: { data: [WIDGET_WIRE], pagination: {} } };
    }
    if (url.includes("/admin/dashboard-widgets")) {
      return { data: { data: WIDGET_WIRE } };
    }
    if (
      url === "/api/v1/admin/prometheus-datasources" &&
      config.method === "GET"
    ) {
      return { data: { data: [DATASOURCE_WIRE], pagination: {} } };
    }
    if (url.endsWith("/test")) {
      return { data: { data: { ok: true, message: "Datasource reachable" } } };
    }
    if (url.includes("/admin/prometheus-datasources")) {
      return { data: { data: DATASOURCE_WIRE } };
    }
    if (url.includes("/dashboards/")) {
      return { data: { data: [RENDERED_WIRE] } };
    }
    throw new Error(`Unexpected request: ${url}`);
  });
});

describe("generated dashboard API boundary", () => {
  it("maps paginated widget reads and fails closed on missing single data", async () => {
    await expect(listWidgets()).resolves.toEqual([
      expect.objectContaining({
        widgetType: "prom_stat",
        scopeIds: [],
        refreshSeconds: 60,
      }),
    ]);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/admin/dashboard-widgets",
        params: { limit: 200 },
      }),
    );

    request.mockResolvedValueOnce({ data: {} });
    await expect(getWidget(WIDGET_WIRE.id)).rejects.toThrow(
      "getWidget returned no data payload",
    );
  });

  it("uses exact widget mutation paths and snake_case bodies", async () => {
    await expect(createWidget(WIDGET_BODY)).resolves.toMatchObject({
      id: WIDGET_WIRE.id,
    });
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "POST",
        url: "/api/v1/admin/dashboard-widgets",
        data: WIDGET_BODY,
      }),
    );
    await updateWidget(WIDGET_WIRE.id, WIDGET_BODY);
    await deleteWidget(WIDGET_WIRE.id);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "DELETE",
        url: `/api/v1/admin/dashboard-widgets/${WIDGET_WIRE.id}`,
      }),
    );
  });

  it("maps datasource CRUD without exposing or recasing stored credentials", async () => {
    await expect(listDatasources()).resolves.toEqual([
      expect.objectContaining({ hasAuth: true, tlsSkipVerify: false }),
    ]);
    await createDatasource(DATASOURCE_BODY);
    expect(lastRequest()?.data).toEqual(DATASOURCE_BODY);
    await updateDatasource(DATASOURCE_WIRE.id, DATASOURCE_BODY);
    await expect(testDatasource(DATASOURCE_WIRE.id)).resolves.toEqual({
      ok: true,
      message: "Datasource reachable",
    });
    await deleteDatasource(DATASOURCE_WIRE.id);
  });

  it("maps every rendered scope to the camelCase view model once", async () => {
    await expect(renderGlobal()).resolves.toEqual([
      expect.objectContaining({
        widgetType: "prom_stat",
        specResolved: WIDGET_WIRE.spec,
        data: expect.objectContaining({ statValue: 0.42, statOk: true }),
      }),
    ]);
    await renderForCluster("cluster-1");
    expect(lastRequest()?.url).toBe("/api/v1/dashboards/clusters/cluster-1");
    await renderForProject("project-1");
    expect(lastRequest()?.url).toBe("/api/v1/dashboards/projects/project-1");
  });
});
