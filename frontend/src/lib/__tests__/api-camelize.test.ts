/** End-to-end proof that the shared transport never rewrites response keys. */
import type {
  AxiosAdapter,
  AxiosResponse,
  InternalAxiosRequestConfig,
} from "axios";
import api from "@/lib/api";

const realAdapter = api.defaults.adapter;

function respondWith(body: unknown) {
  const adapter: AxiosAdapter = async (config: InternalAxiosRequestConfig) =>
    ({
      data: structuredClone(body),
      status: 200,
      statusText: "OK",
      headers: {},
      config,
    }) as AxiosResponse;
  api.defaults.adapter = adapter;
}

afterEach(() => {
  api.defaults.adapter = realAdapter;
});

describe("API response wire casing", () => {
  it("preserves ordinary management API responses exactly", async () => {
    const wire = {
      data: [{ operation_id: "op-1", created_at: "2026-08-24T00:00:00Z" }],
      pagination: { total: 1, has_more: false, next_offset: 0 },
    };
    respondWith(wire);

    const response = await api.get("/settings/monitoring/operations/");
    expect(response.data).toEqual(wire);
    expect(response.data.pagination.hasMore).toBeUndefined();
  });

  it("preserves opaque monitoring preview values", async () => {
    const wire = {
      data: {
        values: {
          config: {
            global: { resolve_timeout: "5m" },
            route: { group_by: ["cluster_id"], repeat_interval: "3h" },
          },
        },
      },
    };
    respondWith(wire);

    const response = await api.post(
      "/settings/monitoring/alertmanager/preview/",
      {},
    );
    expect(response.data).toEqual(wire);
  });

  it("preserves raw Kubernetes object keys", async () => {
    const wire = {
      apiVersion: "v1",
      metadata: { creationTimestamp: "2026-08-24T00:00:00Z" },
      status: { container_statuses: [{ restart_count: 2 }] },
    };
    respondWith(wire);

    const response = await api.get("/clusters/c1/k8s/api/v1/pods/p1");
    expect(response.data).toEqual(wire);
  });
});
