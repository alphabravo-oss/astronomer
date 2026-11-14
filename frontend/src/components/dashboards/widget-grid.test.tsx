import { act, cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WidgetGrid, type WidgetFetcher } from "./widget-grid";
import type { RenderedWidget } from "@/lib/api/dashboards";

const widget = (name: string, seconds = 5): RenderedWidget => ({
  id: name,
  name,
  widgetType: "prom_stat",
  specResolved: {},
  grid: { x: 0, y: 0, w: 4, h: 2 },
  refreshSeconds: seconds,
});
let client: QueryClient;
beforeEach(() => {
  vi.useFakeTimers();
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.useRealTimers();
});
const tick = async (ms = 1) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
};
const grid = (fetcher: WidgetFetcher, scope = "a") => (
  <QueryClientProvider client={client}>
    <WidgetGrid scope={["cluster", scope]} fetcher={fetcher} />
  </QueryClientProvider>
);

it("shares one grid refresh and ignores parent fetcher identity changes", async () => {
  const fetcher = vi.fn().mockResolvedValue([widget("one"), widget("two")]);
  const view = render(grid(fetcher));
  await tick();
  expect(fetcher).toHaveBeenCalledTimes(1);
  const next = vi.fn().mockResolvedValue([widget("one"), widget("two")]);
  view.rerender(grid(next));
  await tick();
  expect(next).not.toHaveBeenCalled();
  await tick(5000);
  expect(next).toHaveBeenCalledTimes(1);
});
it("recovers from an initial error and from an empty scope", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValue([widget("recovered")]);
  const view = render(grid(fetcher));
  await tick();
  expect(screen.getByText("Widgets: offline")).toBeInTheDocument();
  await tick(60001);
  expect(screen.getByText("recovered")).toBeInTheDocument();
  view.rerender(grid(vi.fn().mockResolvedValue([]), "empty"));
  await tick();
  expect(screen.queryByText("recovered")).not.toBeInTheDocument();
  view.rerender(grid(vi.fn().mockResolvedValue([widget("new scope")]), "b"));
  await tick();
  expect(screen.getByText("new scope")).toBeInTheDocument();
});
it("aborts old scopes and unmounts, and never accepts a late old-scope response", async () => {
  let resolve!: (value: RenderedWidget[]) => void;
  let oldSignal: AbortSignal | undefined;
  const old: WidgetFetcher = (signal) => {
    oldSignal = signal;
    return new Promise((done) => {
      resolve = done;
    });
  };
  let newSignal: AbortSignal | undefined;
  const next: WidgetFetcher = async (signal) => {
    newSignal = signal;
    return [widget("current")];
  };
  const view = render(grid(old));
  await tick();
  view.rerender(grid(next, "b"));
  await tick();
  expect(oldSignal?.aborted).toBe(true);
  await act(async () => resolve([widget("stale")]));
  await tick();
  expect(screen.queryByText("stale")).not.toBeInTheDocument();
  expect(screen.getByText("current")).toBeInTheDocument();
  // Make an active request at unmount, rather than asserting a finished one aborts.
  view.rerender(
    grid((signal) => {
      newSignal = signal;
      return new Promise(() => {});
    }, "c"),
  );
  await tick();
  view.unmount();
  expect(newSignal?.aborted).toBe(true);
});
it("refuses unsafe iframe URLs", async () => {
  const fetcher = vi.fn().mockResolvedValue([
    {
      ...widget("unsafe"),
      widgetType: "url_iframe",
      specResolved: { url: "javascript:alert(1)" },
    },
  ]);
  const { container } = render(grid(fetcher));
  await tick();
  expect(screen.getByText("Missing or unsafe HTTPS URL")).toBeInTheDocument();
  expect(container.querySelector("iframe")).toBeNull();
});
