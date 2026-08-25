import transport from "@/lib/api/transport";
import type {
  AnomalyBaseline,
  AlertEvent,
  AlertRule,
  AlertSilence,
  NotificationChannel,
} from "@/types";
import {
  acknowledgeAlert,
  createAlertRule,
  createAlertSilence,
  createNotificationChannel,
  deleteAlertRule,
  deleteAlertSilence,
  deleteNotificationChannel,
  disableAlertRule,
  enableAlertRule,
  expireAlertSilence,
  getAnomalyBaseline,
  getAnomalyBaselines,
  getAlertEvent,
  getAlertEvents,
  getAlertRule,
  getAlertRules,
  getAlertSilences,
  getNotificationChannel,
  getNotificationChannels,
  resolveAlert,
  testNotificationChannel,
  toAlertRuleWriteRequest,
  updateAlertRule,
  updateNotificationChannel,
} from "./alerting";

vi.mock("@/lib/api/transport", () => ({
  default: { request: vi.fn() },
}));

const request = vi.mocked(transport.request);

const RULE: AlertRule = {
  id: "10000000-0000-0000-0000-000000000001",
  name: "High CPU",
  description: "CPU is above the operating threshold",
  type: "threshold",
  severity: "warning",
  clusterId: "20000000-0000-0000-0000-000000000001",
  clusterName: "production-west",
  namespace: "",
  enabled: true,
  query: "cluster_cpu_percent",
  threshold: 85,
  duration: "5m",
  activeAlerts: 1,
  labels: { team: "platform" },
  annotations: {},
  notificationChannelIds: ["30000000-0000-0000-0000-000000000001"],
  ruleKind: "threshold",
  metric: "",
  anomalyStddev: null,
  anomalyWindowSeconds: null,
  anomalyMinSamples: null,
  anomalyDirection: "",
  createdAt: "2026-08-01T00:00:00Z",
  updatedAt: "2026-08-23T00:00:00Z",
};

const EVENT: AlertEvent = {
  id: "40000000-0000-0000-0000-000000000001",
  ruleId: RULE.id,
  ruleName: RULE.name,
  severity: "warning",
  status: "firing",
  message: "CPU is 91%",
  clusterId: RULE.clusterId,
  clusterName: RULE.clusterName,
  namespace: "",
  resource: "",
  labels: { team: "platform" },
  firedAt: "2026-08-23T12:00:00Z",
  acknowledgedAt: null,
  acknowledgedBy: null,
  resolvedAt: null,
  resolvedBy: null,
};

const CHANNEL: NotificationChannel = {
  id: "30000000-0000-0000-0000-000000000001",
  name: "Platform Slack",
  type: "slack",
  enabled: true,
  config: { webhookUrl: "[redacted]", channel: "#platform-alerts" },
  createdAt: "2026-08-01T00:00:00Z",
  updatedAt: "2026-08-23T00:00:00Z",
};

const SILENCE: AlertSilence = {
  id: "50000000-0000-0000-0000-000000000001",
  reason: "Maintenance",
  matchers: { cluster_id: RULE.clusterId ?? "", rule_id: "" },
  startsAt: "2026-08-23T12:00:00Z",
  endsAt: "2026-08-23T13:00:00Z",
  duration: "1h0m0s",
  createdBy: null,
  createdAt: "2026-08-23T12:00:00Z",
};

const BASELINE: AnomalyBaseline = {
  id: "60000000-0000-0000-0000-000000000001",
  clusterId: RULE.clusterId ?? "",
  metric: "cluster_cpu_percent",
  windowSeconds: 86400,
  sampleCount: 288,
  mean: 42,
  stddev: 8,
  min: 19,
  max: 76,
  p50: 41,
  p95: 63,
  p99: 71,
  lastValue: 44,
  lastValueAt: "2026-08-23T12:00:00Z",
  updatedAt: "2026-08-23T12:00:00Z",
  recentSampleBytes: 4096,
};

function lastRequest() {
  return request.mock.calls.at(-1)?.[0];
}

beforeEach(() => {
  vi.clearAllMocks();
  request.mockImplementation(async (config) => {
    const url = String(config.url);
    if (config.method === "DELETE") return { data: undefined } as never;
    if (url === "/api/v1/alerting/rules" && config.method === "GET") {
      return { data: { data: [RULE], pagination: {} } } as never;
    }
    if (url === "/api/v1/alerting/events/" && config.method === "GET") {
      return { data: { data: [EVENT], pagination: {} } } as never;
    }
    if (url === "/api/v1/alerting/channels/" && config.method === "GET") {
      return { data: { data: [CHANNEL], pagination: {} } } as never;
    }
    if (url === "/api/v1/alerting/silences" && config.method === "GET") {
      return { data: { data: [SILENCE], pagination: {} } } as never;
    }
    if (url === "/api/v1/anomaly-baselines" && config.method === "GET") {
      return { data: { data: [BASELINE], pagination: {} } } as never;
    }
    if (url.endsWith("/test")) {
      return {
        data: { data: { success: true, message: "Test notification sent" } },
      } as never;
    }
    if (url.includes("/events/")) return { data: { data: EVENT } } as never;
    if (url.includes("/anomaly-baselines/")) {
      return { data: { data: BASELINE } } as never;
    }
    if (url.includes("/channels")) return { data: { data: CHANNEL } } as never;
    if (url.includes("/silences")) return { data: { data: SILENCE } } as never;
    return { data: { data: RULE } } as never;
  });
});

describe("generated alerting reads", () => {
  it("uses typed pagination and filter parameters for every list", async () => {
    await expect(
      getAlertRules({ clusterId: RULE.clusterId ?? undefined, limit: 200 }),
    ).resolves.toEqual([RULE]);
    expect(lastRequest()?.params).toEqual({
      clusterId: RULE.clusterId,
      limit: 200,
    });

    await expect(
      getAlertEvents({
        status: "firing",
        severity: "warning",
        clusterId: RULE.clusterId ?? undefined,
        limit: "50",
        offset: "10",
      }),
    ).resolves.toEqual([EVENT]);
    expect(lastRequest()?.params).toEqual({
      status: "firing",
      severity: "warning",
      clusterId: RULE.clusterId,
      limit: 50,
      offset: 10,
    });

    await expect(getNotificationChannels({ limit: 100 })).resolves.toEqual([
      CHANNEL,
    ]);
    await expect(getAlertSilences({ limit: 100 })).resolves.toEqual([SILENCE]);
    await expect(
      getAnomalyBaselines({
        clusterId: RULE.clusterId ?? undefined,
        limit: 5,
        offset: 10,
      }),
    ).resolves.toEqual([BASELINE]);
    expect(lastRequest()?.params).toEqual({
      clusterId: RULE.clusterId,
      limit: 5,
      offset: 10,
    });
    expect(request.mock.calls.every(([config]) => config.baseURL === "")).toBe(
      true,
    );
  });

  it("reads exact single-resource envelopes and fails closed without data", async () => {
    await expect(getAlertRule(RULE.id)).resolves.toEqual(RULE);
    await expect(getAlertEvent(EVENT.id)).resolves.toEqual(EVENT);
    await expect(getNotificationChannel(CHANNEL.id)).resolves.toEqual(CHANNEL);
    await expect(getAnomalyBaseline(BASELINE.id)).resolves.toEqual(BASELINE);

    request.mockResolvedValueOnce({ data: {} } as never);
    await expect(getAlertRule(RULE.id)).rejects.toThrow(
      "getAlertRule returned no data payload",
    );
  });
});

describe("generated alerting mutations", () => {
  it("maps the rule edit model to the exact mixed-case Go request contract", async () => {
    expect(
      toAlertRuleWriteRequest({
        name: "Anomaly CPU",
        type: "anomaly",
        clusterId: RULE.clusterId,
        ruleKind: "anomaly",
        anomalyStddev: 3,
        anomalyWindowSeconds: 86400,
        anomalyMinSamples: 50,
        anomalyDirection: "above",
      }),
    ).toEqual({
      name: "Anomaly CPU",
      type: "anomaly",
      cluster_id: RULE.clusterId,
      rule_kind: "anomaly",
      anomaly_stddev: 3,
      anomaly_window_seconds: 86400,
      anomaly_min_samples: 50,
      anomaly_direction: "above",
    });

    await createAlertRule({
      name: "Anomaly CPU",
      type: "anomaly",
      ruleKind: "anomaly",
      metric: "cluster_cpu_percent",
      enabled: true,
    });
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "POST",
        url: "/api/v1/alerting/rules",
        data: {
          name: "Anomaly CPU",
          type: "anomaly",
          enabled: true,
          rule_kind: "anomaly",
          metric: "cluster_cpu_percent",
        },
      }),
    );

    await updateAlertRule(RULE.id, { severity: "critical", enabled: false });
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "PUT",
        url: `/api/v1/alerting/rules/${RULE.id}`,
        data: { severity: "critical", enabled: false },
      }),
    );
  });

  it("uses canonical channel and silence request names", async () => {
    await createNotificationChannel({
      name: "Platform Slack",
      type: "slack",
      enabled: true,
      config: { webhookUrl: "https://hooks.example.test/secret" },
    });
    expect(lastRequest()?.data).toEqual({
      name: "Platform Slack",
      channel_type: "slack",
      enabled: true,
      configuration: { webhookUrl: "https://hooks.example.test/secret" },
    });

    await updateNotificationChannel(CHANNEL.id, { enabled: false });
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "PUT",
        url: `/api/v1/alerting/channels/${CHANNEL.id}`,
        data: { enabled: false },
      }),
    );

    await createAlertSilence({
      reason: "Maintenance",
      duration: "1h",
      matchers: { cluster_id: RULE.clusterId ?? "" },
    });
    expect(lastRequest()?.data).toEqual({
      reason: "Maintenance",
      duration: "1h",
      matchers: { cluster_id: RULE.clusterId },
    });
  });

  it("covers action and deletion routes without handwritten URLs", async () => {
    await enableAlertRule(RULE.id);
    await disableAlertRule(RULE.id);
    await acknowledgeAlert(EVENT.id);
    await resolveAlert(EVENT.id);
    await testNotificationChannel(CHANNEL.id);
    await expireAlertSilence(SILENCE.id);
    await deleteAlertRule(RULE.id);
    await deleteNotificationChannel(CHANNEL.id);
    await deleteAlertSilence(SILENCE.id);

    expect(
      request.mock.calls.slice(-9).map(([config]) => [config.method, config.url]),
    ).toEqual([
      ["POST", `/api/v1/alerting/rules/${RULE.id}/enable`],
      ["POST", `/api/v1/alerting/rules/${RULE.id}/disable`],
      ["POST", `/api/v1/alerting/events/${EVENT.id}/acknowledge`],
      ["POST", `/api/v1/alerting/events/${EVENT.id}/resolve`],
      ["POST", `/api/v1/alerting/channels/${CHANNEL.id}/test`],
      ["POST", `/api/v1/alerting/silences/${SILENCE.id}/expire`],
      ["DELETE", `/api/v1/alerting/rules/${RULE.id}`],
      ["DELETE", `/api/v1/alerting/channels/${CHANNEL.id}`],
      ["DELETE", `/api/v1/alerting/silences/${SILENCE.id}`],
    ]);
  });
});
