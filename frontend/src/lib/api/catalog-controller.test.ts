import { AxiosError, type AxiosAdapter } from "axios";
import api from "@/lib/api/transport";
import { getCatalogControllerWindow, parseCatalogControllerWindow } from "./catalog-controller";

const originalAdapter = api.defaults.adapter;
afterEach(() => { api.defaults.adapter = originalAdapter; });
const window = { scope: "authorized-clusters", windowLimit: 1000, returned: 2, total: 1001, complete: false, notice: "Recent authorized window" };

it("reads the typed window through the actual generated authenticated transport", async () => {
  const signal = new AbortController().signal;
  api.defaults.adapter = (async (config) => {
    expect(config.method).toBe("get");
    expect(config.url).toBe("/api/v1/catalog/controller/status/");
    expect(config.signal).toBe(signal);
    expect(config.withCredentials).toBe(true);
    return { data: { operationSummary: window }, status: 200, statusText: "OK", headers: {}, config };
  }) satisfies AxiosAdapter;
  await expect(getCatalogControllerWindow(signal)).resolves.toEqual(window);
});

it("propagates actual authorization failure without manufacturing a zero window", async () => {
  api.defaults.adapter = (async (config) => {
    throw new AxiosError("Forbidden", "ERR_BAD_REQUEST", config, undefined, { data: {}, status: 403, statusText: "Forbidden", headers: {}, config });
  }) satisfies AxiosAdapter;
  await expect(getCatalogControllerWindow()).rejects.toMatchObject({ response: { status: 403 } });
});

it.each([
  ["null total", { ...window, total: null }],
  ["concurrently smaller count", { ...window, total: 1 }],
  ["equal independently observed count", { ...window, total: 2 }],
])("preserves %s with explicit incomplete semantics", (_name, expected) => {
  expect(parseCatalogControllerWindow({ operationSummary: expected })).toEqual(expected);
});

it("keeps a legacy omitted field unknown", () => {
  expect(parseCatalogControllerWindow({ operations: { pending: 0 } })).toBeNull();
});

it.each([
  ["null window", null],
  ["unsafe count", { ...window, total: Number.MAX_SAFE_INTEGER + 1 }],
  ["negative count", { ...window, total: -1 }],
  ["missing count", { ...window, total: undefined }],
  ["overflow window", { ...window, returned: 1001 }],
  ["unknown scope", { ...window, scope: "namespace" }],
  ["invented completeness", { ...window, complete: true }],
  ["oversized notice", { ...window, notice: "x".repeat(513) }],
])("rejects %s rather than presenting a total", (_name, value) => {
  expect(() => parseCatalogControllerWindow({ operationSummary: value })).toThrow("malformed");
});
