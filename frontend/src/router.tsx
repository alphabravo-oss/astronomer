import { useEffect } from 'react';
import { createRouter, type ErrorComponentProps } from '@tanstack/react-router';
import { routeTree } from './routeTree.gen';

/**
 * Last-resort router error panel (port of the old Next `global-error.tsx`,
 * P2.4). It renders when a route without its own `errorComponent` throws, so
 * it must not depend on the app's providers or global stylesheet — styling is
 * inline rather than Tailwind (no html/body: the document shell is main.tsx's).
 */
function RootErrorPanel({ error, reset }: ErrorComponentProps) {
  useEffect(() => {
    console.error('Root route error:', error);
  }, [error]);

  // Next.js attached a `digest` ref to server-thrown errors; keep reading it
  // defensively for anything that still tags one on.
  const digest = 'digest' in error ? String((error as { digest?: string }).digest ?? '') : '';

  return (
    <div
      data-testid="route-error-boundary"
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#0b0b0f',
        color: '#e5e5e5',
        fontFamily: 'system-ui, -apple-system, Segoe UI, Roboto, sans-serif',
      }}
    >
      <div style={{ maxWidth: 420, padding: 24, textAlign: 'center' }}>
        <h1 style={{ fontSize: 20, fontWeight: 600, margin: '0 0 8px' }}>
          Something went wrong
        </h1>
        <p style={{ fontSize: 14, color: '#9ca3af', margin: '0 0 4px' }}>
          {error.message || 'The application failed to load.'}
        </p>
        {digest && (
          <p style={{ fontSize: 12, color: '#6b7280', fontFamily: 'monospace', margin: '0 0 20px' }}>
            ref: {digest}
          </p>
        )}
        <div style={{ display: 'flex', gap: 8, justifyContent: 'center', marginTop: 16 }}>
          <button
            type="button"
            onClick={reset}
            style={{
              height: 36,
              padding: '0 16px',
              borderRadius: 8,
              border: 'none',
              background: '#3b82f6',
              color: '#fff',
              fontSize: 14,
              fontWeight: 500,
              cursor: 'pointer',
            }}
          >
            Try again
          </button>
          <a
            href="/dashboard"
            style={{
              height: 36,
              padding: '0 16px',
              borderRadius: 8,
              border: '1px solid #27272a',
              color: '#e5e5e5',
              fontSize: 14,
              fontWeight: 500,
              textDecoration: 'none',
              display: 'inline-flex',
              alignItems: 'center',
            }}
          >
            Back to dashboard
          </a>
        </div>
      </div>
    </div>
  );
}

// scrollRestoration: true is deliberate (D25): it resets scroll to top on new
// history entries — matching Next App Router's push-navigation behavior — and
// restores position on back/forward. `false` would preserve the current offset
// across pushes, landing detail pages mid-scroll after long-list navigation.
// The useTabParam replace path keeps `resetScroll: false` (P2.1) so tab
// switches still don't jump.
export const router = createRouter({
  routeTree,
  defaultPreload: false,
  scrollRestoration: true,
  defaultErrorComponent: RootErrorPanel,
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
