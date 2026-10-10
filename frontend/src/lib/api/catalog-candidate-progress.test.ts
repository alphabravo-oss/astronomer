import api from "@/lib/api/transport";
import { AxiosError, type AxiosAdapter } from "axios";
import { collectHelmCharts } from "./catalog-collect";

const originalAdapter = api.defaults.adapter;
afterEach(() => { api.defaults.adapter = originalAdapter; });

it.each(["success", "repeated-token", "missing-token", "revoked", "canceled"])(
  "follows actual generated transport through an empty authorized window: %s", async (mode) => {
    const owner = new AbortController();
    let calls = 0;
    const adapter: AxiosAdapter = async (config) => {
      calls++;
      expect(config.url).toContain("/catalog/charts");
      expect(config.params.cursor).toBe(calls === 1 ? "" : "advance-one");
      expect(config.params.tag).toBe("tag-a");
      expect(config.signal?.aborted).toBe(false);
      if (calls === 2 && mode === "revoked") throw new AxiosError("revoked", "ERR_BAD_REQUEST", config, undefined, { data: {}, status: 403, statusText: "Forbidden", headers: {}, config });
      if (calls === 2 && mode === "canceled") { owner.abort(); throw new AxiosError("canceled", "ERR_CANCELED", config); }
      const next = calls === 1 || mode === "repeated-token" ? "advance-one" : mode === "missing-token" ? null : null;
      const more = calls === 1 || mode === "repeated-token" || mode === "missing-token";
      return { status: 200, statusText: "OK", headers: {}, config, data: { data: [], pagination: { limit: 20, offset: 0, has_more: more, next_offset: null, next_cursor: next } } };
    };
    api.defaults.adapter = adapter;
    const result = collectHelmCharts({ tag: "tag-a", search: "sparse" }, owner.signal);
    if (mode === "success") await expect(result).resolves.toEqual([]);
    else await expect(result).rejects.toThrow();
    expect(calls).toBe(2);
  },
);
