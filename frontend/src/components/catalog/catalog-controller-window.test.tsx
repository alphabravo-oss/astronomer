import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CatalogControllerWindow, CatalogControllerWindowView } from "./catalog-controller-window";
import { getCatalogControllerWindow } from "@/lib/api/catalog-controller";
import { useCurrentUser } from "@/lib/hooks/auth";

vi.mock("@/lib/hooks/auth", () => ({ useCurrentUser: vi.fn() }));
vi.mock("@/lib/api/catalog-controller", () => ({ getCatalogControllerWindow: vi.fn() }));
const window = { scope: "authorized-clusters" as const, windowLimit: 1000 as const, returned: 2, total: 1001, complete: false, notice: "Recent authorized window" };
afterEach(() => { vi.clearAllMocks(); });

it("labels authorized scope and separately observed totals as an incomplete window", () => {
  render(<CatalogControllerWindowView window={window} unavailable={false} />);
  expect(screen.getByText(/2 recent operations from authorized clusters/)).toBeInTheDocument();
  expect(screen.getByText(/Separately observed authorized operation count: 1001/)).toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("not fleet totals");
});
it("keeps missing totals unknown instead of zero", () => {
  render(<CatalogControllerWindowView window={{ ...window, total: null }} unavailable={false} />);
  expect(screen.getByText("Authorized operation total unknown.")).toBeInTheDocument();
});
it("hides retained successful values when authorization is unavailable", () => {
  render(<CatalogControllerWindowView window={window} unavailable />);
  expect(screen.getByRole("status")).toHaveTextContent("summary unavailable");
  expect(screen.queryByText(/1001/)).not.toBeInTheDocument();
});
it("refetches A to B to A roles and hides old data during reauthorization", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const calls: { signal?: AbortSignal; resolve: (value: typeof window) => void }[] = [];
  vi.mocked(getCatalogControllerWindow).mockImplementation((signal) => new Promise((resolve, reject) => {
    calls.push({ signal, resolve });
    signal?.addEventListener("abort", () => reject(new DOMException("Cancelled", "AbortError")), { once: true });
  }));
  const identity = (role: string) => ({ isSuccess: true, data: { id: "owned-user", enabled: true, isSuperuser: false, roles: { global: [], project: [], cluster: [{ id: role }] } } });
  vi.mocked(useCurrentUser).mockReturnValue(identity("A") as never);
  const node = <QueryClientProvider client={client}><CatalogControllerWindow /></QueryClientProvider>;
  const mounted = render(node);
  try {
    await waitFor(() => expect(calls).toHaveLength(1));
    calls[0].resolve(window);
    await screen.findByText(/Separately observed authorized operation count: 1001/);
    vi.mocked(useCurrentUser).mockReturnValue(identity("B") as never);
    mounted.rerender(<QueryClientProvider client={client}><CatalogControllerWindow /></QueryClientProvider>);
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(screen.queryByText(/1001/)).not.toBeInTheDocument();
    vi.mocked(useCurrentUser).mockReturnValue(identity("A") as never);
    mounted.rerender(<QueryClientProvider client={client}><CatalogControllerWindow /></QueryClientProvider>);
    await waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[1].signal?.aborted).toBe(true);
    expect(screen.queryByText(/1001/)).not.toBeInTheDocument();
    calls[2].resolve({ ...window, total: 4 });
    await screen.findByText(/Separately observed authorized operation count: 4/);
  } finally {
    mounted.unmount();
    await client.cancelQueries();
    client.clear();
  }
});
