import * as generated from "@/lib/api/generated/client";
import { requireMonitoringData as requireData } from "./monitoring-data";
import type { MonitoringStackPreview } from "@/types/openapi.generated";
import type {
  ClusterStackRequest,
  SharedThanosRequest,
  SharedAlertmanagerRequest,
  SharedGrafanaRequest,
  SharedLokiRequest,
  MonitoringStackTarget,
  MonitoringStackRequestBody,
} from "./monitoring-stack";

export async function previewClusterStack(
  clusterId: string,
  body: ClusterStackRequest = {},
  signal?: AbortSignal,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postClustersByIdMonitoringStackPreview({
      signal,
      path: { id: clusterId },
      body,
    }),
    "previewClusterStack",
  );
}

export async function previewSharedThanos(
  body: SharedThanosRequest,
  signal?: AbortSignal,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringThanosPreview({ signal, body }),
    "previewSharedThanos",
  );
}

export async function previewSharedAlertmanager(
  body: SharedAlertmanagerRequest,
  signal?: AbortSignal,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringAlertmanagerPreview({ signal, body }),
    "previewSharedAlertmanager",
  );
}

export async function previewSharedGrafana(
  body: SharedGrafanaRequest,
  signal?: AbortSignal,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringGrafanaPreview({ signal, body }),
    "previewSharedGrafana",
  );
}

export async function previewSharedLoki(
  body: SharedLokiRequest,
  signal?: AbortSignal,
): Promise<MonitoringStackPreview> {
  return requireData(
    await generated.postSettingsMonitoringLokiPreview({ signal, body }),
    "previewSharedLoki",
  );
}

export async function previewStack(
  target: MonitoringStackTarget,
  body: MonitoringStackRequestBody,
  signal?: AbortSignal,
): Promise<MonitoringStackPreview> {
  switch (target.kind) {
    case "cluster":
      return previewClusterStack(
        target.clusterId,
        body as ClusterStackRequest,
        signal,
      );
    case "thanos":
      return previewSharedThanos(body as SharedThanosRequest, signal);
    case "alertmanager":
      return previewSharedAlertmanager(
        body as SharedAlertmanagerRequest,
        signal,
      );
    case "grafana":
      return previewSharedGrafana(body as SharedGrafanaRequest, signal);
    case "loki":
      return previewSharedLoki(body as SharedLokiRequest, signal);
  }
}
