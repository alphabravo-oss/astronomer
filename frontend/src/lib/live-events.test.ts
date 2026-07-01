import { renderHook, waitFor } from '@testing-library/react';

jest.mock('@/lib/api', () => ({
  createStreamTicket: jest.fn().mockResolvedValue({ ticket: 'tkt' }),
}));

import { useLiveEvents } from '@/lib/live-events';

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  listeners: string[] = [];
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: (() => void) | null = null;
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string): void {
    this.listeners.push(type);
  }
  close(): void {
    /* no-op */
  }
}

beforeEach(() => {
  FakeEventSource.instances = [];
  (globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource;
});

describe('live-events SSE listener registration', () => {
  it('registers addEventListener for cluster.registration.step/phase', async () => {
    renderHook(() => useLiveEvents());

    await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));

    const es = FakeEventSource.instances[FakeEventSource.instances.length - 1];
    expect(es.listeners).toContain('cluster.registration.step');
    expect(es.listeners).toContain('cluster.registration.phase');
  });
});
