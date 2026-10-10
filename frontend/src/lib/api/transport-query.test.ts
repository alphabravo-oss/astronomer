import api from "./transport";
import axios, { type AxiosAdapter } from "axios";
import { getHelmCharts } from "./catalog";
it("serializes OpenAPI exploded arrays as repeated keys rather than bracketed names", () => {
  const uri = api.getUri({
    url: "/clusters/c/workloads/",
    params: { namespaces: ["team-a", "team-b"], namespace: undefined },
  });
  const query = new URL(uri, "https://example.test").searchParams;
  expect(query.getAll("namespaces")).toEqual(["team-a", "team-b"]);
  expect(query.has("namespaces[]")).toBe(false);
  expect(query.has("namespace")).toBe(false);
});

it.each(["", "opaque-past-ten-thousand_+/=%&"])(
  "preserves cursor %j through the generated operation and Axios serialization",
  async (cursor) => {
    const previous = api.defaults.adapter;
    const signal = new AbortController().signal;
    const adapter: AxiosAdapter = async (config) => {
      const uri = new URL(api.getUri(config), "https://example.test");
      expect(uri.pathname).toBe("/api/v1/catalog/charts/");
      expect(uri.searchParams.getAll("cursor")).toEqual([cursor]);
      expect(uri.searchParams.has("offset")).toBe(false);
      expect(uri.searchParams.get("project_id")).toBe("project-a");
      expect(uri.searchParams.get("search")).toBe("literal");
      expect(config.signal).toBe(signal);
      return {
        config,
        data: {
          data: [],
          pagination: {
            limit: 25,
            offset: 0,
            has_more: false,
            next_offset: null,
          },
        },
        status: 200,
        statusText: "OK",
        headers: {},
      };
    };
    api.defaults.adapter = vi.fn(adapter);
    try {
      await getHelmCharts(
        { cursor, projectId: "project-a", search: " literal " },
        signal,
      );
      expect(api.defaults.adapter).toHaveBeenCalledTimes(1);
      const cancelled = new AbortController();
      cancelled.abort();
      const error = await getHelmCharts({ cursor }, cancelled.signal).catch(
        (cause) => cause,
      );
      expect(axios.isCancel(error)).toBe(true);
      expect(api.defaults.adapter).toHaveBeenCalledTimes(1);
    } finally {
      api.defaults.adapter = previous;
    }
  },
);
