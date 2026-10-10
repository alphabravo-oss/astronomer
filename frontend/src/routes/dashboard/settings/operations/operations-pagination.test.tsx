import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AxiosError, type AxiosAdapter } from "axios";
import api from "@/lib/api/transport";
import { Route } from "./index";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});
vi.mock("@/components/settings/auth-gate", () => ({
  SettingsAuthGate: ({ children }: { children: ReactNode }) => children,
}));

const Page = Route.options.component!;
beforeAll(async () => {
  await (
    Page as typeof Page & { preload?: () => Promise<unknown> }
  ).preload?.();
});
const originalAdapter = api.defaults.adapter;
const clients: QueryClient[] = [];
let requests: URL[];
let queueUnavailable: boolean;
let rejectedOutboxPage: boolean;

beforeEach(() => {
  requests = [];
  queueUnavailable = false;
  rejectedOutboxPage = false;
  const adapter: AxiosAdapter = async (config) => {
    const url = new URL(api.getUri(config), "https://example.test");
    requests.push(url);
    expect(config.signal).toBeInstanceOf(AbortSignal);
    let data: unknown;
    if (url.pathname === "/api/v1/admin/queues/") {
      if (queueUnavailable)
        throw new AxiosError(
          "Queue inspection unavailable",
          undefined,
          config,
          undefined,
          {
            config,
            data: {
              error: {
                code: "inspector_unavailable",
                message: "Queue inspection unavailable",
              },
            },
            status: 503,
            statusText: "Unavailable",
            headers: {},
          },
        );
      data = {
        data: [
          {
            name: "default",
            size: 101,
            active: 0,
            pending: 0,
            scheduled: 0,
            retry: 0,
            archived: 101,
            completed: 0,
            paused: false,
            as_of: "2026-10-10T00:00:00Z",
          },
        ],
      };
    } else if (url.pathname === "/api/v1/admin/queues/default/dlq/") {
      const page = Number(url.searchParams.get("page") ?? "1");
      const offset = (page - 1) * 100;
      const dlq = Array.from(
        { length: Math.min(100, Math.max(101 - offset, 0)) },
        (_, i) => ({
          id: `dlq-${offset + i + 1}`,
          type: `archived-task-${offset + i + 1}`,
          retried: 1,
          last_err: "fixture",
          last_failed_at: "2026-10-10T00:00:00Z",
        }),
      );
      data = {
        data: { queue: "default", dlq, count: dlq.length },
        pagination: {
          limit: 100,
          offset,
          has_more: offset + dlq.length < 101,
          next_offset: offset === 0 ? 100 : null,
        },
      };
    } else if (url.pathname === "/api/v1/admin/task-outbox/") {
      const offset = Number(url.searchParams.get("offset") ?? "0");
      if (rejectedOutboxPage && offset > 0)
        throw new AxiosError(
          "Permission revoked",
          undefined,
          config,
          undefined,
          {
            config,
            data: {
              error: { code: "forbidden", message: "Permission revoked" },
            },
            status: 403,
            statusText: "Forbidden",
            headers: {},
          },
        );
      const rows = Array.from(
        { length: Math.min(100, Math.max(101 - offset, 0)) },
        (_, i) => ({
          id: `outbox-${offset + i + 1}`,
          task_type: `durable-task-${offset + i + 1}`,
          queue_name: "default",
          status: url.searchParams.get("status") || "dead",
          attempt_count: 1,
          max_retry: 1,
          max_delivery_attempts: 1,
          timeout_seconds: 1,
          unique_seconds: 0,
          payload_size: 0,
        }),
      );
      data = {
        data: rows,
        pagination: {
          total: 101,
          limit: 100,
          offset,
          has_more: offset + rows.length < 101,
          next_offset: offset === 0 ? 100 : null,
        },
      };
    } else throw new Error(`unexpected read route ${url.pathname}`);
    return { config, data, status: 200, statusText: "OK", headers: {} };
  };
  api.defaults.adapter = adapter;
});
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  api.defaults.adapter = originalAdapter;
});
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <Page />
    </QueryClientProvider>,
  );
}
function panel(title: string) {
  return within(
    screen
      .getByRole("heading", { name: new RegExp(`^${title}`) })
      .closest("section")!,
  );
}

it.each(["Task outbox", "Dead-letter"])(
  "reaches row 101 through the actual generated transport and %s table",
  async (title) => {
    mount();
    const table = panel(title);
    await table.findByText(
      title === "Task outbox" ? "durable-task-1" : "archived-task-1",
    );
    // These are server pages of 100. Local pages of 20 over a capped response must not satisfy this.
    fireEvent.click(table.getByRole("button", { name: "Next page" }));
    await table.findByText(
      title === "Task outbox" ? "durable-task-101" : "archived-task-101",
    );
    const route =
      title === "Task outbox"
        ? "/api/v1/admin/task-outbox/"
        : "/api/v1/admin/queues/default/dlq/";
    expect(
      requests
        .filter((url) => url.pathname === route)
        .map((url) =>
          url.searchParams.get(title === "Task outbox" ? "offset" : "page"),
        ),
    ).toEqual(title === "Task outbox" ? ["0", "100"] : ["1", "2"]);
    expect(table.getByRole("button", { name: "Next page" })).toBeDisabled();
  },
);

it("keeps unavailable inspection as unknown rather than an empty healthy queue listing", async () => {
  queueUnavailable = true;
  mount();
  await screen.findByText("Queue inspection unavailable");
  expect(screen.queryByText("No queues registered")).not.toBeInTheDocument();
  expect(requests.some((url) => url.pathname.includes("/dlq/"))).toBe(false);
});

it("withholds cached rows and totals when a later outbox page loses permission", async () => {
  mount();
  const table = panel("Task outbox");
  await table.findByText("durable-task-1");
  rejectedOutboxPage = true;
  fireEvent.click(table.getByRole("button", { name: "Next page" }));
  await waitFor(() =>
    expect(
      requests.some(
        (url) =>
          url.pathname === "/api/v1/admin/task-outbox/" &&
          url.searchParams.get("offset") === "100",
      ),
    ).toBe(true),
  );
  await table.findByText("Permission required");
  expect(table.queryByText(/Showing/)).not.toBeInTheDocument();
  expect(table.queryByText("durable-task-1")).not.toBeInTheDocument();
  expect(table.getByRole("button", { name: "Previous page" })).toBeEnabled();
});
