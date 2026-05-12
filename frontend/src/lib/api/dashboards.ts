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
 * Conventions mirror `lib/api/settings.ts`:
 *   - Reads come back through the axios response interceptor (camelCase).
 *   - Writes send snake_case keys matching the Go handler's json:"..." tags.
 *   - The standard {data, …} envelope.
 */

import api from '@/lib/api';
import type { APIResponse } from '@/types';

// ============================================================
// Types
// ============================================================

export type WidgetType = 'grafana_panel' | 'prom_sparkline' | 'prom_stat' | 'url_iframe';
export type WidgetScope = 'global' | 'cluster' | 'project';

export interface WidgetGrid {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface WidgetSpec {
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

export interface Widget {
  id: string;
  name: string;
  description: string;
  widgetType: WidgetType;
  spec: WidgetSpec;
  scope: WidgetScope;
  scopeIds: string[];
  grid: WidgetGrid;
  refreshSeconds: number;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface WidgetWriteBody {
  name: string;
  description?: string;
  widget_type: WidgetType;
  spec: WidgetSpec;
  scope: WidgetScope;
  scope_ids?: string[];
  grid: WidgetGrid;
  refresh_seconds: number;
  enabled?: boolean;
}

export interface RenderedWidgetData {
  sparkline_svg?: string;
  sparklineSvg?: string;
  stat_value?: number;
  statValue?: number;
  stat_ok?: boolean;
  statOk?: boolean;
  stat_unit?: string;
  statUnit?: string;
  stat_format?: string;
  statFormat?: string;
  error?: string;
}

export interface RenderedWidget {
  id: string;
  name: string;
  widgetType: WidgetType;
  specResolved: WidgetSpec;
  grid: WidgetGrid;
  refreshSeconds: number;
  data?: RenderedWidgetData;
}

export interface PrometheusDatasource {
  id: string;
  name: string;
  url: string;
  hasAuth: boolean;
  tlsSkipVerify: boolean;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface DatasourceWriteBody {
  name: string;
  url: string;
  basic_auth_user?: string;
  basic_auth_pass?: string;
  bearer_token?: string;
  tls_skip_verify?: boolean;
  enabled?: boolean;
}

// ============================================================
// Admin endpoints
// ============================================================

export async function listWidgets(): Promise<Widget[]> {
  const res = await api.get<APIResponse<Widget[]>>('/api/v1/admin/dashboard-widgets/');
  return res.data.data ?? [];
}

export async function getWidget(id: string): Promise<Widget> {
  const res = await api.get<APIResponse<Widget>>(`/api/v1/admin/dashboard-widgets/${id}/`);
  return res.data.data;
}

export async function createWidget(body: WidgetWriteBody): Promise<Widget> {
  const res = await api.post<APIResponse<Widget>>('/api/v1/admin/dashboard-widgets/', body);
  return res.data.data;
}

export async function updateWidget(id: string, body: WidgetWriteBody): Promise<Widget> {
  const res = await api.put<APIResponse<Widget>>(`/api/v1/admin/dashboard-widgets/${id}/`, body);
  return res.data.data;
}

export async function deleteWidget(id: string): Promise<void> {
  await api.delete(`/api/v1/admin/dashboard-widgets/${id}/`);
}

export async function listDatasources(): Promise<PrometheusDatasource[]> {
  const res = await api.get<APIResponse<PrometheusDatasource[]>>('/api/v1/admin/prometheus-datasources/');
  return res.data.data ?? [];
}

export async function createDatasource(body: DatasourceWriteBody): Promise<PrometheusDatasource> {
  const res = await api.post<APIResponse<PrometheusDatasource>>('/api/v1/admin/prometheus-datasources/', body);
  return res.data.data;
}

export async function updateDatasource(id: string, body: DatasourceWriteBody): Promise<PrometheusDatasource> {
  const res = await api.put<APIResponse<PrometheusDatasource>>(`/api/v1/admin/prometheus-datasources/${id}/`, body);
  return res.data.data;
}

export async function deleteDatasource(id: string): Promise<void> {
  await api.delete(`/api/v1/admin/prometheus-datasources/${id}/`);
}

export interface DatasourceTestResult {
  ok: boolean;
  message: string;
}

export async function testDatasource(id: string): Promise<DatasourceTestResult> {
  const res = await api.post<APIResponse<DatasourceTestResult>>(`/api/v1/admin/prometheus-datasources/${id}/test/`);
  return res.data.data;
}

// ============================================================
// Render endpoints
// ============================================================

export async function renderGlobal(): Promise<RenderedWidget[]> {
  const res = await api.get<APIResponse<RenderedWidget[]>>('/api/v1/dashboards/global/');
  return res.data.data ?? [];
}

export async function renderForCluster(clusterId: string): Promise<RenderedWidget[]> {
  const res = await api.get<APIResponse<RenderedWidget[]>>(`/api/v1/dashboards/clusters/${clusterId}/`);
  return res.data.data ?? [];
}

export async function renderForProject(projectId: string): Promise<RenderedWidget[]> {
  const res = await api.get<APIResponse<RenderedWidget[]>>(`/api/v1/dashboards/projects/${projectId}/`);
  return res.data.data ?? [];
}
