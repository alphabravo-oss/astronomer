'use client';

/**
 * Last-resort error boundary (F-04). This catches errors thrown in the root
 * layout itself, so it must render its own <html>/<body> and cannot depend on
 * the app's providers, fonts, or global stylesheet. Styling is therefore
 * inline rather than Tailwind.
 */

import { useEffect } from 'react';

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error('Root layout error:', error);
  }, [error]);

  return (
    <html lang="en">
      <body
        style={{
          margin: 0,
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
          {error.digest && (
            <p style={{ fontSize: 12, color: '#6b7280', fontFamily: 'monospace', margin: '0 0 20px' }}>
              ref: {error.digest}
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
      </body>
    </html>
  );
}
