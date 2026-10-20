import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { useState, type ReactNode } from "react";
import { ThemeProvider } from "@/lib/theme";
import { KitToaster } from "@/ui/toaster";
import { IS_DEV } from "@/lib/env";
import { shouldRetryQuery, shouldThrowQueryError } from "@/lib/query-retry";
import { UserPreferencesProvider } from "@/lib/user-preferences";
import { AstronomerProductProvider } from "@/lib/product-config";

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
      <UserPreferencesProvider>
        {/* Theme: shared kit provider, three modes, server-synced once signed
            in (see lib/theme.tsx). Product config feeds the kit's shell. */}
        <ThemeProvider>
          <AstronomerProductProvider>
            {children}
            <KitToaster />
          </AstronomerProductProvider>
        </ThemeProvider>
      </UserPreferencesProvider>
      {IS_DEV && import.meta.env.VITE_RQ_DEVTOOLS === "1" && (
        <ReactQueryDevtools
          initialIsOpen={false}
          buttonPosition="bottom-left"
        />
      )}
    </QueryClientProvider>
  );
}
