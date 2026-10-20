import { useCallback, useEffect, useMemo, type ReactNode } from "react";
import {
  ThemeProvider as KitThemeProvider,
  useTheme as useKitTheme,
  type ThemeMode,
  type ThemeStorage,
} from "@/ui/theme";
import { useAuthStore } from "@/lib/store";
import { useUserPreferences } from "@/lib/user-preferences";

export type Theme = ThemeMode;

// Namespaced key — NEVER bare `theme`: other applications may share the same
// origin and parse the bare `theme` key. public/theme-bootstrap.js reads the
// same key before first paint.
export const THEME_STORAGE_KEY = "astronomer-theme";

/**
 * Astronomer's theme adapter over the shared kit provider: three modes
 * (system/light/dark); pre-auth the choice lives in localStorage, signed in it
 * is owned by the server-side user preferences (PUT /auth/me/preferences).
 */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const { preferences, isServerOwned, updatePreferences } =
    useUserPreferences();
  const serverOwned = isAuthenticated && isServerOwned;

  useEffect(() => {
    if (!serverOwned) return;
    // Authenticated preference state has one owner. A prior login-screen
    // choice must not become a fallback for this or the next account.
    try {
      localStorage.removeItem(THEME_STORAGE_KEY);
    } catch {
      // Removing an optional pre-auth hint is best effort.
    }
  }, [serverOwned]);

  const onChange = useCallback(
    (mode: ThemeMode) => {
      if (serverOwned) updatePreferences({ theme: mode });
    },
    [serverOwned, updatePreferences],
  );

  const storage = useMemo<ThemeStorage>(
    () => ({
      storageKey: THEME_STORAGE_KEY,
      serverValue: serverOwned ? preferences.theme : undefined,
      onChange,
      defaultMode: "dark",
    }),
    [onChange, preferences.theme, serverOwned],
  );

  return <KitThemeProvider storage={storage}>{children}</KitThemeProvider>;
}

export function useTheme(): { theme: Theme; setTheme: (theme: Theme) => void } {
  const { mode, setMode } = useKitTheme();
  return { theme: mode, setTheme: setMode };
}
