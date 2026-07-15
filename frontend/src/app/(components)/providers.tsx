'use client';

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactQueryDevtools } from '@tanstack/react-query-devtools';
import { ThemeProvider } from '@/lib/theme';
import { Toaster } from 'sonner';
import { useState, type ReactNode } from 'react';
import { IS_DEV } from '@/lib/env';

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30 * 1000,
            gcTime: 5 * 60 * 1000,
            refetchOnWindowFocus: true,
            retry: (failureCount, error) => {
              if (error instanceof Error && error.message.includes('401')) return false;
              return failureCount < 2;
            },
          },
          mutations: {
            retry: false,
          },
        },
      })
  );

  return (
    <QueryClientProvider client={queryClient}>
      {/* Native provider (D12): class strategy, system tracking, default dark.
          The load-bearing `astronomer-theme` storage key (never bare `theme` —
          the co-hosted ArgoCD SPA JSON-parses that key) lives in @/lib/theme. */}
      <ThemeProvider>
        {children}
        <Toaster
          position="bottom-right"
          theme="dark"
          richColors
          closeButton
          toastOptions={{
            className: 'border border-border',
            duration: 4000,
          }}
        />
      </ThemeProvider>
      {IS_DEV && (
        <ReactQueryDevtools initialIsOpen={false} buttonPosition="bottom-left" />
      )}
    </QueryClientProvider>
  );
}
