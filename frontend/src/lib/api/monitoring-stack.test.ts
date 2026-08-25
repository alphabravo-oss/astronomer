import transport from "@/lib/api/transport";
import {
  getClusterStackStatus,
  getMonitoringOperation,
  getMonitoringSizer,
  getSharedAlertmanagerStatus,
  getSharedGrafanaStatus,
  getSharedLokiStatus,
  getSharedThanosStatus,
  installClusterStack,
  installSharedAlertmanager,
  installSharedGrafana,
  installSharedLoki,
  installSharedThanos,
  isActiveOperationStatus,
  isRetryableOperationStatus,
  isTerminalOperationStatus,
  listMonitoringOperations,
  operationTargetOf,
  parseReplaceRequiredError,
  previewClusterStack,
  previewSharedAlertmanager,
  previewSharedGrafana,
  previewSharedLoki,
  previewSharedThanos,
  replaceClusterStack,
  replaceSharedAlertmanager,
  replaceSharedGrafana,
  replaceSharedLoki,
  replaceSharedThanos,
  retryMonitoringOperation,
  runStackLifecycle,
  uninstallClusterStack,
  uninstallSharedAlertmanager,
  uninstallSharedGrafana,
  uninstallSharedLoki,
  uninstallSharedThanos,
  upgradeClusterStack,
  upgradeSharedAlertmanager,
  upgradeSharedGrafana,
  upgradeSharedLoki,
  upgradeSharedThanos,
  type MonitoringOperation,
  type SharedThanosRequest,
} from "./monitoring-stack";

vi.mock("@/lib/api/transport", () => ({
  default: { request: vi.fn() },
}));

const request = vi.mocked(transport.request);

const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const OP: MonitoringOperation = {
  id: "10000000-0000-0000-0000-000000000001",
  targetType: "cluster_stack",
  targetKey: "cluster-1",
  operationType: "install",
  status: "pending",
  attemptCount: 0,
  errorMessage: "",
  createdAt: "2026-07-29T10:00:00Z",
  updatedAt: "2026-07-29T10:00:00Z",
};

const PREVIEW = {
  clusterId: "cluster-1",
  chart: {
    repoUrl: "https://charts.example.test",
    chartName: "kube-prometheus-stack",
  },
  values: { prometheus: { external_labels: { cluster_id: "cluster-1" } } },
  desiredSpecHash: "spec-sha256",
  requiresReplace: false,
  replaceReasons: null,
};

const SIZER = {
  managementClusterId: "cluster-1",
  isLocal: true,
  kubernetesVersion: "v1.35.0",
  nodes: {},
  requestsInUse: {},
  podListTruncated: false,
  leftover: {},
  reserve: {},
  usable: {},
  storageClass: {},
  objectStorage: {
    configured: true,
    storageConfigId: "storage-1",
    computedLokiPrefix: "loki",
  },
  connectedClusters: 1,
  thanos: {},
  estimates: {},
  skipDiskCheck: false,
  verdicts: {
    grafana: { result: "pass", reasons: [], warnings: [] },
    loki: { result: "pass", mode: "singleBinary", reasons: [], warnings: [] },
    thanosReceive: { result: "fail", reasons: ["not_offered"], warnings: [] },
  },
  caps: {},
};

const THANOS_BODY: SharedThanosRequest = {
  managementClusterId: "20000000-0000-0000-0000-000000000001",
  storageConfigId: "30000000-0000-0000-0000-000000000001",
  queryReplicas: 3,
};

beforeEach(() => {
  vi.clearAllMocks();
  request.mockImplementation(async (config) => {
    const url = String(config.url);
    if (url.endsWith("/preview")) return { data: { data: PREVIEW } } as never;
    if (url.endsWith("/sizer")) return { data: { data: SIZER } } as never;
    if (url.endsWith("/operations")) {
      return {
        data: {
          data: [OP],
          pagination: { limit: 5, offset: 0, returned: 1, has_more: false },
        },
      } as never;
    }
    if (url.endsWith("/status")) {
      return { data: { data: { status: "healthy" } } } as never;
    }
    return { data: { data: OP } } as never;
  });
});

function lastRequest() {
  return request.mock.calls.at(-1)?.[0];
}

describe("generated per-cluster monitoring lifecycle", () => {
  it("uses the exact OpenAPI path, method, body, and typed envelope for every verb", async () => {
    await expect(getClusterStackStatus("cluster-1")).resolves.toEqual({
      status: "healthy",
    });
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/clusters/cluster-1/monitoring/stack/status",
      }),
    );

    await expect(
      previewClusterStack("cluster-1", { namespace: "observability" }),
    ).resolves.toEqual(PREVIEW);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "POST",
        url: "/api/v1/clusters/cluster-1/monitoring/stack/preview",
        data: { namespace: "observability" },
      }),
    );

    await installClusterStack("cluster-1", { retention: "30d" });
    await upgradeClusterStack("cluster-1", { retention: "60d" });
    await replaceClusterStack("cluster-1", { namespace: "metrics" });
    await uninstallClusterStack("cluster-1");

    expect(
      request.mock.calls
        .slice(-4)
        .map(([config]) => [config.method, config.url]),
    ).toEqual([
      ["POST", "/api/v1/clusters/cluster-1/monitoring/stack/install"],
      ["PUT", "/api/v1/clusters/cluster-1/monitoring/stack/upgrade"],
      ["POST", "/api/v1/clusters/cluster-1/monitoring/stack/replace"],
      ["DELETE", "/api/v1/clusters/cluster-1/monitoring/stack/uninstall"],
    ]);
    for (const [config] of request.mock.calls.slice(-4)) {
      expect(config.headers).toEqual(
        expect.objectContaining({
          "Idempotency-Key": expect.stringMatching(UUID_V4),
        }),
      );
    }
  });

  it("rejects a success envelope without data instead of fabricating success", async () => {
    request.mockResolvedValueOnce({ data: {} } as never);
    await expect(getClusterStackStatus("cluster-1")).rejects.toThrow(
      "getClusterStackStatus returned no data payload",
    );
  });
});

describe("generated shared-stack monitoring lifecycle", () => {
  it("covers Thanos and preserves the handler's camelCase request keys", async () => {
    await getSharedThanosStatus();
    await previewSharedThanos(THANOS_BODY);
    await installSharedThanos(THANOS_BODY);
    await upgradeSharedThanos(THANOS_BODY);
    await replaceSharedThanos(THANOS_BODY);
    await uninstallSharedThanos(THANOS_BODY.managementClusterId);

    expect(request.mock.calls.map(([config]) => config.url)).toEqual([
      "/api/v1/settings/monitoring/thanos/status",
      "/api/v1/settings/monitoring/thanos/preview",
      "/api/v1/settings/monitoring/thanos/install",
      "/api/v1/settings/monitoring/thanos/upgrade",
      "/api/v1/settings/monitoring/thanos/replace",
      "/api/v1/settings/monitoring/thanos/uninstall",
    ]);
    expect(request.mock.calls[2]?.[0]).toEqual(
      expect.objectContaining({
        data: THANOS_BODY,
        headers: expect.objectContaining({
          "Idempotency-Key": expect.stringMatching(UUID_V4),
        }),
      }),
    );
    expect(lastRequest()?.params).toEqual({
      clusterId: THANOS_BODY.managementClusterId,
    });
  });

  it("covers Alertmanager, Grafana, and Loki status/preview/lifecycle/uninstall contracts", async () => {
    const alertmanager = { managementClusterId: "cluster-1", replicas: 2 };
    const grafana = {
      managementClusterId: "cluster-1",
      ingressHost: "grafana.example.test",
    };
    const loki = {
      managementClusterId: "cluster-1",
      storageConfigId: "storage-1",
      ingestHostname: "loki.example.test",
    };

    await getSharedAlertmanagerStatus();
    await previewSharedAlertmanager(alertmanager);
    await installSharedAlertmanager(alertmanager);
    await upgradeSharedAlertmanager(alertmanager);
    await replaceSharedAlertmanager(alertmanager);
    await uninstallSharedAlertmanager("cluster-1");

    await getSharedGrafanaStatus();
    await previewSharedGrafana(grafana);
    await installSharedGrafana(grafana);
    await upgradeSharedGrafana(grafana);
    await replaceSharedGrafana(grafana);
    await uninstallSharedGrafana("cluster-1");

    await getSharedLokiStatus();
    await previewSharedLoki(loki);
    await installSharedLoki(loki);
    await upgradeSharedLoki(loki);
    await replaceSharedLoki(loki);
    await uninstallSharedLoki("cluster-1");

    expect(request).toHaveBeenCalledTimes(18);
    expect(
      request.mock.calls.filter(([config]) => config.method === "DELETE"),
    ).toHaveLength(3);
    expect(
      request.mock.calls.filter(([config]) => config.url?.endsWith("/preview")),
    ).toHaveLength(3);
    expect(lastRequest()?.params).toEqual({ clusterId: "cluster-1" });
  });
});

describe("generated monitoring operations and sizer", () => {
  it("sends the exact camelCase operation filters and returns the authorized page data", async () => {
    await expect(
      listMonitoringOperations({
        targetType: "shared_thanos",
        targetKey: "shared",
        status: "running",
        limit: 5,
        offset: 10,
      }),
    ).resolves.toEqual([OP]);
    expect(lastRequest()?.params).toEqual({
      targetType: "shared_thanos",
      targetKey: "shared",
      status: "running",
      limit: 5,
      offset: 10,
    });
  });

  it("uses typed detail/retry paths and returns the complete capacity result", async () => {
    await expect(getMonitoringOperation(OP.id)).resolves.toEqual(OP);
    expect(lastRequest()?.url).toBe(
      `/api/v1/settings/monitoring/operations/${OP.id}`,
    );

    await expect(retryMonitoringOperation(OP.id)).resolves.toEqual(OP);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "POST",
        url: `/api/v1/settings/monitoring/operations/${OP.id}/retry`,
      }),
    );

    await expect(getMonitoringSizer()).resolves.toEqual(SIZER);
  });
});

describe("target dispatch and state policy", () => {
  it("maps every target to the durable queue identity", () => {
    expect(operationTargetOf({ kind: "cluster", clusterId: "c1" })).toEqual({
      targetType: "cluster_stack",
      targetKey: "c1",
    });
    expect(operationTargetOf({ kind: "thanos" })).toEqual({
      targetType: "shared_thanos",
      targetKey: "shared",
    });
    expect(operationTargetOf({ kind: "alertmanager" })).toEqual({
      targetType: "shared_alertmanager",
      targetKey: "shared",
    });
    expect(operationTargetOf({ kind: "grafana" })).toEqual({
      targetType: "shared_grafana",
      targetKey: "shared",
    });
    expect(operationTargetOf({ kind: "loki" })).toEqual({
      targetType: "shared_loki",
      targetKey: "shared",
    });
  });

  it("routes the target-generic lifecycle through the generated operation", async () => {
    await runStackLifecycle(
      { kind: "cluster", clusterId: "c1" },
      "uninstall",
      {},
    );
    expect(lastRequest()?.url).toBe(
      "/api/v1/clusters/c1/monitoring/stack/uninstall",
    );

    await runStackLifecycle({ kind: "thanos" }, "upgrade", THANOS_BODY);
    expect(lastRequest()?.url).toBe(
      "/api/v1/settings/monitoring/thanos/upgrade",
    );

    await runStackLifecycle({ kind: "loki" }, "install", {
      managementClusterId: "cluster-1",
      storageConfigId: "storage-1",
      ingestHostname: "loki.example.test",
    });
    expect(lastRequest()?.url).toBe("/api/v1/settings/monitoring/loki/install");
  });

  it("parses only the monitoring replace-required 409 envelope", () => {
    expect(
      parseReplaceRequiredError({
        response: {
          status: 409,
          data: {
            data: {
              error: "replace_required",
              message: "Storage movement requires replacement",
              requiresReplace: true,
              replaceReasons: ["storage configuration change"],
            },
          },
        },
      }),
    ).toEqual({
      message: "Storage movement requires replacement",
      replaceReasons: ["storage configuration change"],
    });
    expect(parseReplaceRequiredError(new Error("boom"))).toBeNull();
  });

  it("classifies the complete operation state alphabet", () => {
    expect(["pending", "running"].every(isActiveOperationStatus)).toBe(true);
    expect(
      ["completed", "failed", "superseded"].every(isTerminalOperationStatus),
    ).toBe(true);
    expect(["failed", "superseded"].every(isRetryableOperationStatus)).toBe(
      true,
    );
    expect(isRetryableOperationStatus("completed")).toBe(false);
  });
});
