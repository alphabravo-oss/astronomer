import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';
import type { User } from '@/types';

// ============================================================
// Auth Store
// ============================================================

interface AuthState {
  user: User | null;
  token: string | null;
  isAuthenticated: boolean;
  login: (user: User, token: string) => void;
  logout: () => void;
  updateUser: (user: Partial<User>) => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      token: null,
      isAuthenticated: false,
      login: (user, token) =>
        set({
          user,
          token,
          isAuthenticated: true,
        }),
      logout: () => {
        localStorage.removeItem('astronomer_token');
        localStorage.removeItem('astronomer_refresh');
        // Clear the session cookie so the /argocd/* reverse proxy stops
        // accepting this user. Inline so we don't drag api.ts (and its
        // axios import) into the store bundle.
        if (typeof document !== 'undefined') {
          document.cookie = 'astronomer_session=; Path=/; Max-Age=0; SameSite=Lax';
        }
        set({
          user: null,
          token: null,
          isAuthenticated: false,
        });
      },
      updateUser: (updates) =>
        set((state) => ({
          user: state.user ? { ...state.user, ...updates } : null,
        })),
    }),
    {
      name: 'astronomer-auth',
      storage: createJSONStorage(() => localStorage),
      partialize: (state) => ({
        user: state.user,
        token: state.token,
        isAuthenticated: state.isAuthenticated,
      }),
    }
  )
);

// ============================================================
// UI Store
// ============================================================

interface UIState {
  sidebarOpen: boolean;
  sidebarCollapsed: boolean;
  commandPaletteOpen: boolean;
  theme: 'light' | 'dark' | 'system';
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  toggleSidebarCollapsed: () => void;
  setCommandPaletteOpen: (open: boolean) => void;
  setTheme: (theme: 'light' | 'dark' | 'system') => void;
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      sidebarOpen: true,
      sidebarCollapsed: false,
      commandPaletteOpen: false,
      theme: 'dark',
      toggleSidebar: () =>
        set((state) => ({ sidebarOpen: !state.sidebarOpen })),
      setSidebarOpen: (open) =>
        set({ sidebarOpen: open }),
      toggleSidebarCollapsed: () =>
        set((state) => ({ sidebarCollapsed: !state.sidebarCollapsed })),
      setCommandPaletteOpen: (open) =>
        set({ commandPaletteOpen: open }),
      setTheme: (theme) =>
        set({ theme }),
    }),
    {
      name: 'astronomer-ui',
      storage: createJSONStorage(() => localStorage),
      partialize: (state) => ({
        sidebarCollapsed: state.sidebarCollapsed,
        theme: state.theme,
      }),
    }
  )
);
