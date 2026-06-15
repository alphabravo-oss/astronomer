'use client';

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ReactQueryDevtools } from '@tanstack/react-query-devtools';
import { ThemeProvider } from 'next-themes';
import { Toaster } from 'sonner';
import { useState, type ReactNode } from 'react';

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
      <ThemeProvider
        attribute="class"
        defaultTheme="dark"
        enableSystem
        disableTransitionOnChange
        // Use a namespaced key so we don't collide with the upstream ArgoCD
        // SPA, which is served on the same origin under /argocd/* and reads
        // the bare `theme` localStorage key as JSON. next-themes writes the
        // value as a literal string ("dark"), and ArgoCD's `JSON.parse` then
        // throws "Unexpected token 'd', "dark" is not valid JSON" and the
        // ArgoCD applications page renders blank.
        storageKey="astronomer-theme"
      >
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
      {process.env.NODE_ENV !== 'production' && (
        <ReactQueryDevtools initialIsOpen={false} buttonPosition="bottom-left" />
      )}
    </QueryClientProvider>
  );
}
