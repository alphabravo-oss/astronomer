import { createServer, type Server } from "node:http";
import { QueryClient } from "@tanstack/react-query";
import axios from "axios";
import { afterEach, beforeEach, expect, it } from "vitest";
import api from "./transport";
import { queryKeys } from "@/lib/query-keys";
import { getToolOperation, installTool } from "./tools";
import { k8sGet } from "./kubernetes-proxy";

let server: Server;
let baseURL: string | undefined;
let adapter: typeof api.defaults.adapter;
let observe: (path: string, closed: Promise<void>) => void;
let committed = 0;
let interceptor: number;
beforeEach(async () => {
  committed = 0;
  baseURL = api.defaults.baseURL;
  adapter = api.defaults.adapter;
  server = createServer((req, res) => {
    if (req.method === "POST") {
      committed++;
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ data: { id: "operation-1" } }));
      return;
    }
    const closed = new Promise<void>((resolve) => res.on("close", resolve));
    observe(req.url ?? "", closed);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string")
    throw new Error("Missing fixture address");
  api.defaults.baseURL = `http://127.0.0.1:${address.port}`;
  api.defaults.adapter = "http";
  const origin = api.defaults.baseURL;
  interceptor = api.interceptors.request.use((config) => {
    config.baseURL = origin;
    return config;
  });
});
afterEach(async () => {
  api.interceptors.request.eject(interceptor);
  api.defaults.baseURL = baseURL;
  api.defaults.adapter = adapter;
  server.closeAllConnections();
  await new Promise<void>((resolve) => server.close(() => resolve()));
});
it.each(["generated", "kubernetes"])(
  "cancels an actual %s read socket through Query ownership",
  async (kind) => {
    let arrived!: () => void;
    const request = new Promise<void>((resolve) => {
      arrived = resolve;
    });
    let disconnected!: Promise<void>;
    observe = (_path, closed) => {
      disconnected = closed;
      arrived();
    };
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const pending = client
      .fetchQuery({
        queryKey: queryKeys.tools.operation(kind),
        queryFn: ({ signal }) =>
          kind === "generated"
            ? getToolOperation("operation-1", signal)
            : k8sGet("cluster-1", "api/v1/pods", signal),
      })
      .catch((error: unknown) => error);
    await request;
    await client.cancelQueries({ queryKey: queryKeys.tools.operation(kind) });
    await disconnected;
    await pending;
    client.clear();
  },
);
it("closing observation does not cancel a committed tool installation", async () => {
  await installTool("logging", { cluster_id: "cluster-1", preset: "default" });
  const controller = new AbortController();
  let arrived!: () => void;
  const request = new Promise<void>((resolve) => {
    arrived = resolve;
  });
  let disconnected!: Promise<void>;
  observe = (_path, closed) => {
    disconnected = closed;
    arrived();
  };
  const pending = getToolOperation("operation-1", controller.signal).catch(
    (error: unknown) => error,
  );
  await request;
  controller.abort();
  expect(axios.isCancel(await pending)).toBe(true);
  await disconnected;
  expect(committed).toBe(1);
});
