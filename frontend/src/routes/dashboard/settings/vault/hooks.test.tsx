import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useVaultConnections } from "./-hooks";
const { list, remove, testConnection, create } = vi.hoisted(() => ({
  list: vi.fn(),
  remove: vi.fn(),
  testConnection: vi.fn(),
  create: vi.fn(),
}));
vi.mock("@/lib/api/vault", () => ({
  listVaultConnections: list,
  deleteVaultConnection: remove,
  testVaultConnection: testConnection,
  createVaultConnection: create,
}));
beforeEach(() => {
  vi.clearAllMocks();
  list.mockResolvedValue([]);
  remove.mockResolvedValue({});
  testConnection.mockResolvedValue({});
});
it.each(["delete", "test"])(
  "keeps %s pending until its observed cache refresh completes",
  async (action) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useVaultConnections(vi.fn()), {
      wrapper,
    });
    await waitFor(() =>
      expect(result.current.vaultConnectionsQuery.isSuccess).toBe(true),
    );
    let finish!: () => void;
    list.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = () => resolve([]);
        }),
    );
    let saved!: Promise<unknown>;
    let settled = false;
    act(() => {
      saved = (
        action === "delete" ? result.current.delMu : result.current.testMu
      ).mutateAsync("connection-1");
      void saved.then(() => {
        settled = true;
      });
    });
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    expect(
      (action === "delete" ? result.current.delMu : result.current.testMu)
        .isPending,
    ).toBe(true);
    expect(settled).toBe(false);
    await act(async () => {
      finish();
      await saved;
    });
    await waitFor(() =>
      expect(
        (action === "delete" ? result.current.delMu : result.current.testMu)
          .isPending,
      ).toBe(false),
    );
    expect(settled).toBe(true);
    client.clear();
  },
);
