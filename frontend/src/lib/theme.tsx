import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useAuthStore } from "@/lib/store";
import { useUserPreferences } from "@/lib/user-preferences";

export type Theme = "light" | "dark" | "system";

// Namespaced key — NEVER bare `theme`: other applications may share the
// same origin and parse the bare `theme` key, so a
// raw "dark" string there blanks its applications page. Values are stored raw
// ("light" | "dark" | "system") so prefs written by next-themes survive.
export const THEME_STORAGE_KEY = "astronomer-theme";

interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  theme: "dark",
  setTheme: () => {},
});

function readStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    return stored === "light" || stored === "dark" || stored === "system"
      ? stored
      : "dark";
  } catch {
    return "dark";
  }
}

function applyTheme(theme: Theme) {
  const dark =
    theme === "system"
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
      : theme !== "light";
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preAuthTheme, setPreAuthTheme] = useState<Theme>(readStoredTheme);
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const { preferences, isServerOwned, updatePreferences } =
    useUserPreferences();
  const theme =
    isAuthenticated && isServerOwned ? preferences.theme : preAuthTheme;

  const setTheme = useCallback((next: Theme) => {
    if (isAuthenticated && isServerOwned) {
      updatePreferences({ theme: next });
    } else {
      setPreAuthTheme(next);
      try {
        localStorage.setItem(THEME_STORAGE_KEY, next);
      } catch {
        // Storage unavailable: pre-auth theme still applies in-session.
      }
    }
  }, [isAuthenticated, isServerOwned, updatePreferences]);

  useEffect(() => {
    if (!isAuthenticated || !isServerOwned) return;
    // Authenticated preference state has one owner. A prior login-screen choice
    // must not silently become a fallback for this or the next account.
    try {
      localStorage.removeItem(THEME_STORAGE_KEY);
    } catch {
      // Removing an optional pre-auth hint is best effort.
    }
  }, [isAuthenticated, isServerOwned]);

  useEffect(() => {
    applyTheme(theme);
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => applyTheme("system");
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [theme]);

  const value = useMemo(() => ({ theme, setTheme }), [theme, setTheme]);
  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  return useContext(ThemeContext);
}
