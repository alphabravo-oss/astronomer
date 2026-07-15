import type { MockedFunction } from 'vitest';
import { render, waitFor, act } from '@testing-library/react';
import * as api from '@/lib/api';

// The wterm terminal is WASM-backed; stub it. Its <Terminal> fires onReady on
// mount, which is what gates PodTerminal's connect effect.
vi.mock('@wterm/react', async () => {
  const React = await import('react');
  return {
    Terminal: ({ onReady }: { onReady?: () => void }) => {
      React.useEffect(() => {
        onReady?.();
      }, [onReady]);
      return React.createElement('div', { 'data-testid': 'terminal' });
    },
    useTerminal: () => ({
      ref: { current: null },
      write: vi.fn(),
      resize: vi.fn(),
      focus: vi.fn(),
    }),
  };
});
vi.mock('@/lib/theme', () => ({ useTheme: () => ({ theme: 'dark' }) }));
vi.mock('@/lib/api', () => ({ createStreamTicket: vi.fn() }));

import { PodTerminal } from './pod-terminal';

const mockedCreateStreamTicket = api.createStreamTicket as MockedFunction<
  typeof api.createStreamTicket
>;

// Regression: unmounting (or an effect re-run) before the stream-ticket XHR
// resolves must cancel the pending connect so the late .then does NOT open an
// orphaned WebSocket (which would leak a server-side exec stream + agent SPDY
// session and consume a shared per-agent slot).
describe('PodTerminal — cancel pending connect on unmount', () => {
  const OriginalWebSocket = (global as { WebSocket?: unknown }).WebSocket;

  afterEach(() => {
    (global as { WebSocket?: unknown }).WebSocket = OriginalWebSocket;
    vi.clearAllMocks();
  });

  it('does not construct a WebSocket if unmounted while the ticket is in flight', async () => {
    let resolveTicket!: (v: { ticket: string; expiresAt: string }) => void;
    mockedCreateStreamTicket.mockImplementation(
      () => new Promise<{ ticket: string; expiresAt: string }>((res) => { resolveTicket = res; }),
    );

    const wsCtor = vi.fn();
    (global as { WebSocket?: unknown }).WebSocket = wsCtor;

    const { unmount } = render(
      <PodTerminal clusterId="c1" namespace="ns" pod="pod-a" container="main" />,
    );

    // The connect effect (gated on wterm readiness) has fired the ticket XHR.
    await waitFor(() => expect(mockedCreateStreamTicket).toHaveBeenCalled());

    // Navigate away before the ticket resolves.
    unmount();

    // Ticket resolves late — the cancelled attempt must bail before new WebSocket.
    await act(async () => {
      resolveTicket({ ticket: 'tok-123', expiresAt: new Date(Date.now() + 60000).toISOString() });
      await Promise.resolve();
    });

    expect(wsCtor).not.toHaveBeenCalled();
  });
});
