import { beforeEach, describe, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/lib/api/transport", () => ({ default: { request } }));

import {
  executeOpenAPIOperationWithResponse,
  getClustersByClusterIdProxyServiceByNamespaceByServicePortProxy,
  getClustersById,
  postClusters,
} from "./client";

describe("generated OpenAPI client", () => {
  beforeEach(() => {
    request.mockReset();
    request.mockResolvedValue({ data: { data: { id: "cluster-1" } } });
  });

  it("owns URL expansion, query/header parameters, cancellation, and raw wire mode", async () => {
    const controller = new AbortController();
    await getClustersById({
      path: { id: "cluster/with space" },
      signal: controller.signal,
      headers: { "X-Request-ID": "request-1" },
    });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        baseURL: "",
        method: "GET",
        url: "/api/v1/clusters/cluster%2Fwith%20space/",
        signal: controller.signal,
        headers: { "X-Request-ID": "request-1" },
      }),
    );
  });

  it("sends the OpenAPI request body without casing transforms", async () => {
    const body = { name: "west-prod", display_name: "West production" };
    await postClusters({ body });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        baseURL: "",
        method: "POST",
        url: "/api/v1/clusters/",
        data: body,
      }),
    );
  });

  it("treats format: binary proxy schemas as blobs regardless of media wildcard", async () => {
    await getClustersByClusterIdProxyServiceByNamespaceByServicePortProxy({
      path: {
        cluster_id: "cluster-1",
        namespace: "monitoring",
        service_port: "grafana:3000",
      },
    });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/clusters/cluster-1/proxy/service/monitoring/grafana%3A3000/*",
        responseType: "blob",
      }),
    );
  });

  it("preserves response headers and status for conditional API clients", async () => {
    request.mockResolvedValueOnce({
      data: { data: { id: "target-1" } },
      headers: { etag: '"7"' },
      status: 200,
    });

    const response = await executeOpenAPIOperationWithResponse(
      "getDeliveryTargetsById",
      {
        path: { id: "target-1" },
        query: { project_id: "project-1" },
      },
    );

    expect(response).toEqual({
      data: { data: { id: "target-1" } },
      headers: { etag: '"7"' },
      status: 200,
    });
  });
});
