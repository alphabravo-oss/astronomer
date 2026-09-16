import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { UserPreferencesProvider, useUserPreferences } from "./user-preferences";
import { ThemeProvider, THEME_STORAGE_KEY, useTheme } from "./theme";
import { useAuthStore } from "./store";
import type { User } from "@/types";

const api = vi.hoisted(() => ({
  getUserPreferences: vi.fn(),
  putUserPreferences: vi.fn(),
}));

vi.mock("@/lib/api/user-preferences", async (importOriginal) => {
  const original = await importOriginal<
    typeof import("@/lib/api/user-preferences")
  >();
  return { ...original, ...api };
});

const stored = {
  theme: "light" as const,
  table_density: "compact" as const,
  landing_route: "/dashboard/clusters" as const,
  time_format: "24h" as const,
  favorites: ["/dashboard/clusters" as const],
};

function Probe() {
  const { preferences, updatePreferences } = useUserPreferences();
  const { theme } = useTheme();
  return (
    <>
      <span data-testid="theme">{theme}</span>
      <span data-testid="density">{preferences.table_density}</span>
      <button
        type="button"
        onClick={() =>
          updatePreferences({ favorites: ["/dashboard/audit"] })
        }
      >
        Update
      </button>
    </>
  );
}

function renderPreferences() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <UserPreferencesProvider>
        <ThemeProvider>
          <Probe />
        </ThemeProvider>
      </UserPreferencesProvider>
    </QueryClientProvider>,
  );
}

describe("UserPreferencesProvider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    act(() =>
      useAuthStore.setState({
        isAuthenticated: true,
        user: { id: "0b44b65a-a893-4f22-a91c-10432c75503f" } as User,
      }),
    );
    api.getUserPreferences.mockResolvedValue(stored);
    api.putUserPreferences.mockImplementation(async (value) => value);
  });

  it("uses authenticated server preferences instead of local theme state", async () => {
    localStorage.setItem(THEME_STORAGE_KEY, "dark");
    renderPreferences();
    await waitFor(() => expect(screen.getByTestId("theme")).toHaveTextContent("light"));
    expect(screen.getByTestId("density")).toHaveTextContent("compact");
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();
  });

  it("updates the complete typed document", async () => {
    renderPreferences();
    await screen.findByText("compact");
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(api.putUserPreferences).toHaveBeenCalled());
    expect(api.putUserPreferences.mock.calls[0]?.[0]).toEqual({
      ...stored,
      favorites: ["/dashboard/audit"],
    });
  });
});
