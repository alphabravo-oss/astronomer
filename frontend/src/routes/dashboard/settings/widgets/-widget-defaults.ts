import { toastApiError, toastSuccess } from "@/lib/toast";
import type { Widget, WidgetScope, WidgetType } from "@/lib/api/dashboards";

// Local cache keys for the admin widget/datasource lists. Assigned to
// identifiers (not inlined into `queryKey:`) so they satisfy the lint rule
// that reserves inline queryKey arrays for src/lib/query-keys.ts.
export const WIDGETS_KEY = ["admin", "dashboard-widgets"] as const;
export const DATASOURCES_KEY = ["admin", "prometheus-datasources"] as const;

export const DEFAULT_SPEC_BY_TYPE: Record<WidgetType, string> = {
  grafana_panel: JSON.stringify(
    {
      base_url: "https://grafana.example.com",
      dashboard_uid: "xyz",
      panel_id: 1,
      vars: { cluster: "$cluster_uid" },
    },
    null,
    2,
  ),
  prom_sparkline: JSON.stringify(
    {
      datasource: "default",
      query: "sum(rate(container_cpu_usage_seconds_total[5m]))",
      duration: "1h",
      step: "60s",
    },
    null,
    2,
  ),
  prom_stat: JSON.stringify(
    {
      datasource: "default",
      query:
        "histogram_quantile(0.99, sum(rate(apiserver_request_duration_seconds_bucket[5m])) by (le))",
      unit: "s",
      format: ".3f",
    },
    null,
    2,
  ),
  url_iframe: JSON.stringify(
    {
      url: "https://billing.example.com/clusters/{{cluster_uid}}",
      height_px: 280,
    },
    null,
    2,
  ),
};

// Shared success/failure reporting so every widgets-admin mutation surfaces
// a toast (P023.6) instead of failing silently.
export const onMutationOk = (invalidate: () => void, message: string) => () => {
  invalidate();
  toastSuccess(message);
};
export const onMutationFail = (action: string) => (err: unknown) =>
  toastApiError(action, err);

export const widgetDefaults = () => ({
  name: "",
  description: "",
  widgetType: "prom_sparkline" as WidgetType,
  scope: "global" as WidgetScope,
  scopeIds: [] as string[],
  grid: { x: 0, y: 0, w: 4, h: 2 },
  refreshSeconds: 60,
  enabled: true,
  specText: DEFAULT_SPEC_BY_TYPE.prom_sparkline,
});

export type WidgetFormValues = ReturnType<typeof widgetDefaults>;

export const widgetToFormValues = (w: Widget): WidgetFormValues => ({
  name: w.name,
  description: w.description ?? "",
  widgetType: w.widgetType,
  scope: w.scope,
  scopeIds: w.scopeIds ?? [],
  grid: w.grid ?? { x: 0, y: 0, w: 4, h: 2 },
  refreshSeconds: w.refreshSeconds ?? 60,
  enabled: w.enabled ?? true,
  specText: JSON.stringify(w.spec, null, 2),
});
