/**
 * Generated alerting transport boundary.
 *
 * Alerting responses are deliberately camelCase on the wire; request bodies
 * use the Go handlers' snake_case JSON tags, apart from the historical
 * notificationChannelIds compatibility field. Generated operations preserve
 * that casing exactly; the shared transport never rewrites response keys.
 */
import {
  deleteAlertingChannelsById,
  deleteAlertingRulesById,
  deleteAlertingSilencesById,
  getAnomalyBaselines as getAnomalyBaselinesOperation,
  getAnomalyBaselinesById as getAnomalyBaselineOperation,
  getAlertingChannels,
  getAlertingChannelsById,
  getAlertingEvents,
  getAlertingEventsById,
  getAlertingRules,
  getAlertingRulesById,
  getAlertingSilences,
  postAlertingChannels,
  postAlertingChannelsByIdTest,
  postAlertingEventsByIdAcknowledge,
  postAlertingEventsByIdResolve,
  postAlertingRules,
  postAlertingRulesByIdDisable,
  postAlertingRulesByIdEnable,
  postAlertingSilences,
  postAlertingSilencesByIdExpire,
  putAlertingChannelsById,
  putAlertingRulesById,
} from "@/lib/api/generated/client";
import type {
  AnomalyBaseline,
  AlertEvent,
  AlertRule,
  AlertSilence,
  NotificationChannel,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Contracts = OpenAPIComponents["schemas"];

export type AlertRuleWrite = Partial<AlertRule> & {
  cluster_id?: string | null;
  rule_type?: string;
  configuration?: Record<string, unknown>;
  cooldown_minutes?: number;
  rule_kind?: Contracts["AlertRuleRequest"]["rule_kind"];
  anomaly_stddev?: number | null;
  anomaly_window_seconds?: number | null;
  anomaly_min_samples?: number | null;
  anomaly_direction?: Contracts["AlertRuleRequest"]["anomaly_direction"];
};

export type NotificationChannelWrite = Partial<NotificationChannel> & {
  channel_type?: string;
  configuration?: Record<string, unknown>;
};

export type AlertSilenceWrite = Partial<AlertSilence> & {
  rule_id?: string | null;
  cluster_id?: string | null;
};

export interface AlertEventQuery {
  status?: "firing" | "acknowledged" | "resolved" | "silenced";
  severity?: "critical" | "warning" | "info";
  clusterId?: string;
  limit?: number | string;
  offset?: number | string;
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function present<T extends Record<string, unknown>>(value: T): T {
  return Object.fromEntries(
    Object.entries(value).filter(([, field]) => field !== undefined),
  ) as T;
}

function requiredText(value: string | undefined, field: string): string {
  const normalized = value?.trim();
  if (!normalized) throw new Error(`${field} is required`);
  return normalized;
}

function optionalInteger(value: number | string | undefined): number | undefined {
  if (value === undefined || value === "") return undefined;
  const parsed = typeof value === "number" ? value : Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : undefined;
}

/** Maps the UI's camelCase edit model to CreateAlertRuleRequest exactly once. */
export function toAlertRuleWriteRequest(
  data: AlertRuleWrite,
): Contracts["AlertRuleRequest"] {
  return present({
    name: data.name,
    description: data.description,
    cluster_id: data.cluster_id ?? data.clusterId,
    rule_type: data.rule_type,
    type: data.type,
    configuration: data.configuration,
    query: data.query,
    threshold: data.threshold,
    duration: data.duration,
    labels: data.labels,
    annotations: data.annotations,
    notificationChannelIds: data.notificationChannelIds,
    severity: data.severity,
    enabled: data.enabled,
    cooldown_minutes: data.cooldown_minutes,
    rule_kind: data.rule_kind ?? data.ruleKind,
    metric: data.metric,
    anomaly_stddev: data.anomaly_stddev ?? data.anomalyStddev,
    anomaly_window_seconds:
      data.anomaly_window_seconds ?? data.anomalyWindowSeconds,
    anomaly_min_samples: data.anomaly_min_samples ?? data.anomalyMinSamples,
    anomaly_direction: data.anomaly_direction ??
      (data.anomalyDirection || undefined),
  });
}

/** Uses the canonical channel_type/configuration pair accepted by the handler. */
export function toNotificationChannelWriteRequest(
  data: NotificationChannelWrite,
): Contracts["AlertChannelRequest"] {
  return present({
    name: data.name,
    channel_type: data.channel_type ?? data.type,
    configuration: data.configuration ?? data.config,
    enabled: data.enabled,
  });
}

export function toAlertSilenceWriteRequest(
  data: AlertSilenceWrite,
): Contracts["AlertSilenceRequest"] {
  return present({
    rule_id: data.rule_id,
    cluster_id: data.cluster_id,
    reason: requiredText(data.reason, "Alert silence reason"),
    starts_at: data.startsAt,
    ends_at: data.endsAt,
    duration: data.duration,
    matchers: data.matchers,
  });
}

export async function getAlertRules(params?: {
  clusterId?: string;
  limit?: number;
  offset?: number;
}): Promise<AlertRule[]> {
  const page = await getAlertingRules({ query: params });
  return page.data;
}

export async function getAlertRule(id: string): Promise<AlertRule> {
  return requireData(
    await getAlertingRulesById({ path: { id } }),
    "getAlertRule",
  );
}

export async function createAlertRule(
  data: AlertRuleWrite,
): Promise<AlertRule> {
  const body = toAlertRuleWriteRequest({
    ...data,
    name: requiredText(data.name, "Alert rule name"),
  });
  return requireData(await postAlertingRules({ body }), "createAlertRule");
}

export async function updateAlertRule(
  id: string,
  data: AlertRuleWrite,
): Promise<AlertRule> {
  return requireData(
    await putAlertingRulesById({
      path: { id },
      body: toAlertRuleWriteRequest(data),
    }),
    "updateAlertRule",
  );
}

export async function deleteAlertRule(id: string): Promise<void> {
  await deleteAlertingRulesById({ path: { id } });
}

export async function enableAlertRule(id: string): Promise<AlertRule> {
  return requireData(
    await postAlertingRulesByIdEnable({ path: { id } }),
    "enableAlertRule",
  );
}

export async function disableAlertRule(id: string): Promise<AlertRule> {
  return requireData(
    await postAlertingRulesByIdDisable({ path: { id } }),
    "disableAlertRule",
  );
}

export async function getAlertEvents(
  params?: AlertEventQuery,
): Promise<AlertEvent[]> {
  const page = await getAlertingEvents({
    query: present({
      status: params?.status,
      severity: params?.severity,
      clusterId: params?.clusterId,
      limit: optionalInteger(params?.limit),
      offset: optionalInteger(params?.offset),
    }),
  });
  return page.data;
}

export async function getAlertEvent(id: string): Promise<AlertEvent> {
  return requireData(
    await getAlertingEventsById({ path: { id } }),
    "getAlertEvent",
  );
}

export async function acknowledgeAlert(id: string): Promise<AlertEvent> {
  return requireData(
    await postAlertingEventsByIdAcknowledge({ path: { id } }),
    "acknowledgeAlert",
  );
}

export async function resolveAlert(id: string): Promise<AlertEvent> {
  return requireData(
    await postAlertingEventsByIdResolve({ path: { id } }),
    "resolveAlert",
  );
}

export async function getNotificationChannels(params?: {
  limit?: number;
  offset?: number;
}): Promise<NotificationChannel[]> {
  const page = await getAlertingChannels({ query: params });
  return page.data;
}

export async function getNotificationChannel(
  id: string,
): Promise<NotificationChannel> {
  return requireData(
    await getAlertingChannelsById({ path: { id } }),
    "getNotificationChannel",
  );
}

export async function createNotificationChannel(
  data: NotificationChannelWrite,
): Promise<NotificationChannel> {
  const body = toNotificationChannelWriteRequest({
    ...data,
    name: requiredText(data.name, "Notification channel name"),
  });
  return requireData(
    await postAlertingChannels({ body }),
    "createNotificationChannel",
  );
}

export async function updateNotificationChannel(
  id: string,
  data: NotificationChannelWrite,
): Promise<NotificationChannel> {
  return requireData(
    await putAlertingChannelsById({
      path: { id },
      body: toNotificationChannelWriteRequest(data),
    }),
    "updateNotificationChannel",
  );
}

export async function deleteNotificationChannel(id: string): Promise<void> {
  await deleteAlertingChannelsById({ path: { id } });
}

export async function testNotificationChannel(
  id: string,
): Promise<Contracts["NotificationChannelTestResult"]> {
  return requireData(
    await postAlertingChannelsByIdTest({ path: { id } }),
    "testNotificationChannel",
  );
}

export async function getAlertSilences(params?: {
  limit?: number;
  offset?: number;
}): Promise<AlertSilence[]> {
  const page = await getAlertingSilences({ query: params });
  return page.data;
}

export async function createAlertSilence(
  data: AlertSilenceWrite,
): Promise<AlertSilence> {
  return requireData(
    await postAlertingSilences({ body: toAlertSilenceWriteRequest(data) }),
    "createAlertSilence",
  );
}

export async function deleteAlertSilence(id: string): Promise<void> {
  await deleteAlertingSilencesById({ path: { id } });
}

export async function expireAlertSilence(id: string): Promise<AlertSilence> {
  return requireData(
    await postAlertingSilencesByIdExpire({ path: { id } }),
    "expireAlertSilence",
  );
}

export async function getAnomalyBaselines(params?: {
  clusterId?: string;
  limit?: number;
  offset?: number;
}): Promise<AnomalyBaseline[]> {
  const page = await getAnomalyBaselinesOperation({ query: params });
  return page.data;
}

export async function getAnomalyBaseline(
  id: string,
): Promise<AnomalyBaseline> {
  return requireData(
    await getAnomalyBaselineOperation({ path: { id } }),
    "getAnomalyBaseline",
  );
}
