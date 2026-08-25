/**
 * Dashboard widgets API client — migration 058.
 *
 * The widget surface has two halves:
 *
 *   - Admin CRUD over /api/v1/admin/dashboard-widgets/ +
 *     /api/v1/admin/prometheus-datasources/ — superuser-gated server-
 *     side. The admin UI lives at /dashboard/settings/widgets/.
 *
 *   - Public render under /api/v1/dashboards/{global,clusters/{id},
 *     projects/{id}}/ — returns a per-scope list of RenderedWidget
 *     objects with server-rendered SVGs / stat values inlined into
 *     data.{sparkline_svg,stat_value}. The render endpoints are
 *     RBAC-gated on the parent resource read verb (clusters:read for
 *     a cluster page).
 *
 * Generated operations preserve the exact snake_case wire contract. This
 * module is the one explicit wire-to-view-model mapping boundary.
 */

import {
  deleteAdminDashboardWidgetsById,
  deleteAdminPrometheusDatasourcesById,
  getAdminDashboardWidgets,
  getAdminDashboardWidgetsById,
  getAdminPrometheusDatasources,
  getDashboardsClustersById,
  getDashboardsGlobal,
  getDashboardsProjectsById,
  postAdminDashboardWidgets,
  postAdminPrometheusDatasources,
  postAdminPrometheusDatasourcesByIdTest,
  putAdminDashboardWidgetsById,
  putAdminPrometheusDatasourcesById,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

type Contracts = OpenAPIComponents["schemas"];
type WidgetWire = Contracts["DashboardWidget"];
type RenderedWidgetWire = Contracts["RenderedDashboardWidget"];
type DatasourceWire = Contracts["PrometheusDatasource"];

// ============================================================
// Types
// ============================================================

export type WidgetType = WidgetWire["widget_type"];
export type WidgetScope = WidgetWire["scope"];

export type WidgetGrid = Contracts["DashboardWidgetGrid"];

export interface WidgetSpec extends Record<string, unknown> {
  // grafana_panel
  base_url?: string;
  dashboard_uid?: string;
  panel_id?: number | string;
  vars?: Record<string, string>;
  // prom_sparkline + prom_stat
  datasource?: string;
  query?: string;
  duration?: string;
  step?: string;
  unit?: string;
  format?: string;
  // url_iframe
  url?: string;
  height_px?: number;
}

export type Widget = Omit<CamelizeKeys<WidgetWire>, "spec"> & {
  spec: WidgetSpec;
};

export type WidgetWriteBody = Omit<
  Contracts["DashboardWidgetRequest"],
  "spec" | "grid" | "refresh_seconds"
> & {
  spec: WidgetSpec;
  grid: WidgetGrid;
  refresh_seconds: number;
};

export type RenderedWidgetData = CamelizeKeys<
  Contracts["RenderedDashboardWidgetData"]
>;

export type RenderedWidget = Omit<
  CamelizeKeys<RenderedWidgetWire>,
  "specResolved" | "data"
> & {
  specResolved: WidgetSpec;
  data?: RenderedWidgetData;
};

export type DashboardDatasource = CamelizeKeys<DatasourceWire>;

export type DatasourceWriteBody = Contracts["PrometheusDatasourceRequest"];

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

export function mapWidget(wire: WidgetWire): Widget {
  return {
    id: wire.id,
    name: wire.name,
    description: wire.description,
    widgetType: wire.widget_type,
    spec: wire.spec as WidgetSpec,
    scope: wire.scope,
    scopeIds: wire.scope_ids,
    grid: wire.grid,
    refreshSeconds: wire.refresh_seconds,
    enabled: wire.enabled,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function mapDatasource(wire: DatasourceWire): DashboardDatasource {
  return {
    id: wire.id,
    name: wire.name,
    url: wire.url,
    hasAuth: wire.has_auth,
    tlsSkipVerify: wire.tls_skip_verify,
    enabled: wire.enabled,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

export function mapRenderedWidget(wire: RenderedWidgetWire): RenderedWidget {
  const data = wire.data
    ? {
        sparklineSvg: wire.data.sparkline_svg,
        statValue: wire.data.stat_value,
        statUnit: wire.data.stat_unit,
        statFormat: wire.data.stat_format,
        statOk: wire.data.stat_ok,
        error: wire.data.error,
      }
    : undefined;
  return {
    id: wire.id,
    name: wire.name,
    widgetType: wire.widget_type,
    specResolved: wire.spec_resolved as WidgetSpec,
    grid: wire.grid,
    refreshSeconds: wire.refresh_seconds,
    data,
  };
}

// ============================================================
// Admin endpoints
// ============================================================

export async function listWidgets(): Promise<Widget[]> {
  const page = await getAdminDashboardWidgets({ query: { limit: 200 } });
  return page.data.map(mapWidget);
}

export async function getWidget(id: string): Promise<Widget> {
  return mapWidget(
    requireData(
      await getAdminDashboardWidgetsById({ path: { id } }),
      "getWidget",
    ),
  );
}

export async function createWidget(body: WidgetWriteBody): Promise<Widget> {
  return mapWidget(
    requireData(await postAdminDashboardWidgets({ body }), "createWidget"),
  );
}

export async function updateWidget(
  id: string,
  body: WidgetWriteBody,
): Promise<Widget> {
  return mapWidget(
    requireData(
      await putAdminDashboardWidgetsById({ path: { id }, body }),
      "updateWidget",
    ),
  );
}

export async function deleteWidget(id: string): Promise<void> {
  await deleteAdminDashboardWidgetsById({ path: { id } });
}

export async function listDatasources(): Promise<DashboardDatasource[]> {
  const page = await getAdminPrometheusDatasources({ query: { limit: 200 } });
  return page.data.map(mapDatasource);
}

export async function createDatasource(
  body: DatasourceWriteBody,
): Promise<DashboardDatasource> {
  return mapDatasource(
    requireData(
      await postAdminPrometheusDatasources({ body }),
      "createDatasource",
    ),
  );
}

export async function updateDatasource(
  id: string,
  body: DatasourceWriteBody,
): Promise<DashboardDatasource> {
  return mapDatasource(
    requireData(
      await putAdminPrometheusDatasourcesById({ path: { id }, body }),
      "updateDatasource",
    ),
  );
}

export async function deleteDatasource(id: string): Promise<void> {
  await deleteAdminPrometheusDatasourcesById({ path: { id } });
}

export type DatasourceTestResult =
  Contracts["PrometheusDatasourceTestResult"];

export async function testDatasource(
  id: string,
): Promise<DatasourceTestResult> {
  return requireData(
    await postAdminPrometheusDatasourcesByIdTest({ path: { id } }),
    "testDatasource",
  );
}

// ============================================================
// Render endpoints
// ============================================================

export async function renderGlobal(): Promise<RenderedWidget[]> {
  const response = await getDashboardsGlobal();
  return response.data.map(mapRenderedWidget);
}

export async function renderForCluster(
  clusterId: string,
): Promise<RenderedWidget[]> {
  const response = await getDashboardsClustersById({ path: { id: clusterId } });
  return response.data.map(mapRenderedWidget);
}

export async function renderForProject(
  projectId: string,
): Promise<RenderedWidget[]> {
  const response = await getDashboardsProjectsById({ path: { id: projectId } });
  return response.data.map(mapRenderedWidget);
}
