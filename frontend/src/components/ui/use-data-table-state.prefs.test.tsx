/**
 * Plan 024 step 8: useDataTableState's initial page size should come from
 * the user's rows_per_page preference once it's loaded from the server,
 * and fall back to the caller's own pageSize constant otherwise (signed
 * out, still loading, or a preference document with no rows_per_page).
 * Split out from data-table.behavior.test.tsx (shared with the sibling
 * executor) per the plan's file-scope note.
 */
import type { PropsWithChildren } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { useDataTableState } from "@/components/ui/use-data-table-state";
import {
  UserPreferencesProvider,
  useUserPreferences,
} from "@/lib/user-preferences";
import { defaultUserPreferences } from "@/lib/api/user-preferences";

const getUserPreferences = vi.fn();
vi.mock("@/lib/api/user-preferences", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/api/user-preferences")>();
  return {
    ...actual,
    getUserPreferences: () => getUserPreferences(),
    putUserPreferences: vi.fn(),
  };
});

let authState = { isAuthenticated: false, user: null as { id: string } | null };
vi.mock("@/lib/store", () => ({
  useAuthStore: (selector: (state: typeof authState) => unknown) =>
    selector(authState),
}));

function wrapper({ children }: PropsWithChildren) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <QueryClientProvider client={client}>
      <UserPreferencesProvider>{children}</UserPreferencesProvider>
    </QueryClientProvider>
  );
}

function useHarness(pageSize: number) {
  const preferences = useUserPreferences();
  const table = useDataTableState({
    data: [] as unknown[],
    columns: [],
    keyExtractor: () => "",
    pageSize,
    emptyState: { title: "empty", description: "" },
    filtersActive: false,
    resizable: false,
  });
  return { preferences, table };
}

describe("useDataTableState rows_per_page preference", () => {
  it("uses the caller's pageSize while signed out", () => {
    authState = { isAuthenticated: false, user: null };
    const { result } = renderHook(() => useHarness(20), { wrapper });
    expect(result.current.table.clientPagination.pageSize).toBe(20);
  });

  it("adopts the server-owned rows_per_page preference once loaded", async () => {
    authState = { isAuthenticated: true, user: { id: "user-1" } };
    getUserPreferences.mockResolvedValue({
      ...defaultUserPreferences,
      rows_per_page: 50,
    });
    const { result } = renderHook(() => useHarness(20), { wrapper });

    await waitFor(() =>
      expect(result.current.preferences.isServerOwned).toBe(true),
    );
    expect(result.current.table.clientPagination.pageSize).toBe(50);
  });
});
