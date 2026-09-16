import type { User } from "@/types";
import { clearLegacyTokenStorage } from "@/lib/auth/session";
import { createBrowserState } from "@/lib/browser-state";

// ============================================================
// Auth Store
// ============================================================

interface AuthState extends Record<string, unknown> {
  user: User | null;
  isAuthenticated: boolean;
  login: (user: User) => void;
  logout: () => void;
  updateUser: (user: Partial<User>) => void;
}

export const useAuthStore = createBrowserState<AuthState>(
  {
    user: null,
    isAuthenticated: false,
    login: (user) =>
      useAuthStore.setState({
        user,
        isAuthenticated: true,
      }),
    logout: () => {
      // Migration cleanup for pre-HttpOnly-cookie builds. The active
      // browser session is cleared by POST /auth/logout on the backend.
      clearLegacyTokenStorage();
      useAuthStore.setState({
        user: null,
        isAuthenticated: false,
      });
    },
    updateUser: (updates) =>
      useAuthStore.setState((state) => ({
        user: state.user ? { ...state.user, ...updates } : null,
      })),
  },
  {
    storageKey: "astronomer-auth",
    version: 2,
    migrate: (persisted) => {
      if (!persisted || typeof persisted !== "object") return persisted;
      const state = persisted as Partial<AuthState> & {
        token?: string | null;
      };
      const { token: _legacyToken, ...rest } = state;
      return rest;
    },
    persist: (state) => ({
      user: state.user,
      isAuthenticated: state.isAuthenticated,
    }),
  },
);

// ============================================================
// UI Store
// ============================================================

interface UIState extends Record<string, unknown> {
  sidebarCollapsed: boolean;
  mobileSidebarOpen: boolean;
  commandPaletteOpen: boolean;
  toggleSidebarCollapsed: () => void;
  setMobileSidebarOpen: (open: boolean) => void;
  setCommandPaletteOpen: (open: boolean) => void;
}

export const useUIStore = createBrowserState<UIState>(
  {
    sidebarCollapsed: false,
    mobileSidebarOpen: false,
    commandPaletteOpen: false,
    toggleSidebarCollapsed: () =>
      useUIStore.setState((state) => ({
        sidebarCollapsed: !state.sidebarCollapsed,
      })),
    setMobileSidebarOpen: (open) =>
      useUIStore.setState({ mobileSidebarOpen: open }),
    setCommandPaletteOpen: (open) =>
      useUIStore.setState({ commandPaletteOpen: open }),
  },
  {
    storageKey: "astronomer-ui",
    // A stale persisted `theme` value from older builds hydrates as an
    // ignored extra key (theme is owned by lib/theme.tsx) and is dropped
    // from the envelope on the next write.
    persist: (state) => ({
      sidebarCollapsed: state.sidebarCollapsed,
    }),
  },
);
