
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { ThemeProvider, useTheme } from "@/lib/theme";
import { Toaster } from "sonner";
import { useState, type ReactNode } from "react";
import { IS_DEV } from "@/lib/env";
import { shouldRetryQuery, shouldThrowQueryError } from "@/lib/query-retry";
import { UserPreferencesProvider } from "@/lib/user-preferences";

function ThemedToaster() {
  const { theme } = useTheme();
  return (
    <Toaster
      position="bottom-right"
      theme={theme}
      richColors
      closeButton
      toastOptions={{
        className: "border border-border",
        duration: 4000,
      }}
    />
  );
}

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30 * 1000,
            gcTime: 5 * 60 * 1000,
            refetchOnWindowFocus: true,
            retry: shouldRetryQuery,
            throwOnError: shouldThrowQueryError,
          },
          mutations: {
            retry: false,
          },
        },
      }),
  );

  return (
    <QueryClientProvider client={queryClient}>
      {/* Native provider (D12): class strategy, system tracking, default dark.
          The load-bearing `astronomer-theme` storage key (never bare `theme` —
          other co-hosted applications may also use that key) lives in @/lib/theme. */}
      <UserPreferencesProvider>
        <ThemeProvider>
          {children}
          <ThemedToaster />
        </ThemeProvider>
      </UserPreferencesProvider>
      {IS_DEV && (
        <ReactQueryDevtools
          initialIsOpen={false}
          buttonPosition="bottom-left"
        />
      )}
    </QueryClientProvider>
  );
}
