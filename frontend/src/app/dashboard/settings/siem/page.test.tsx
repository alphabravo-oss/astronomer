import type { Mock } from 'vitest';
import { ReactNode } from 'react';
import { act, render, screen, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useAuthStore } from '@/lib/store';
import SIEMForwardersPage from './page';
import type { SIEMForwarder } from '@/types';

// Plain-anchor stand-in: these tests assert link text/href, not routing, and
// the real Link needs a <RouterProvider>.
vi.mock('@/lib/link', () => ({
  Link: ({ href, children, ...rest }: React.ComponentProps<'a'>) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

vi.mock('./hooks', () => ({
  useSIEMForwarders: vi.fn(),
  useCreateSIEMForwarder: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateSIEMForwarder: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteSIEMForwarder: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useTestSIEMForwarder: () => ({ mutate: vi.fn(), isPending: false }),
  useSIEMForwarderStatus: () => ({ data: undefined, isLoading: false }),
}));

import { useSIEMForwarders } from './hooks';

const mockedList = useSIEMForwarders as Mock;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function setSuperuser() {
  act(() => {
    useAuthStore.setState({
      isAuthenticated: true,
      user: {
        id: 'admin',
        email: 'a@b.io',
        username: 'admin',
        is_active: true,
        is_superuser: true,
        roles: { global: [], cluster: [], project: [] },
      } as never,
    });
  });
}

const forwarder: SIEMForwarder = {
  id: 'f1',
  name: 'corp-splunk',
  transport: 'splunk_hec',
  endpoint: 'splunk.corp:8088',
  auth: '<encrypted>',
  authConfigured: true,
  eventFilters: ['auth.login.failed'],
  format: '',
  tlsSkipVerify: false,
  caCertConfigured: false,
  batchSize: 100,
  flushIntervalMs: 5000,
  timeoutSeconds: 10,
  enabled: true,
  createdAt: '2026-07-01T00:00:00Z',
  updatedAt: '2026-07-01T00:00:00Z',
};

describe('SIEMForwardersPage', () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(() => {
    act(() => useAuthStore.setState({ user: null, isAuthenticated: false }));
  });

  it('gates non-superusers behind the settings auth gate', () => {
    act(() => useAuthStore.setState({ user: null, isAuthenticated: false }));
    mockedList.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() });
    render(<SIEMForwardersPage />, { wrapper });
    // Not a superuser -> the list heading never renders.
    expect(screen.queryByText('SIEM Forwarders')).not.toBeInTheDocument();
  });

  it('renders forwarders and opens the create modal for a superuser', () => {
    setSuperuser();
    mockedList.mockReturnValue({ data: [forwarder], isLoading: false, isError: false, refetch: vi.fn() });
    render(<SIEMForwardersPage />, { wrapper });

    expect(screen.getByText('corp-splunk')).toBeInTheDocument();
    expect(screen.getByText('splunk.corp:8088')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Add Forwarder/i }));
    expect(screen.getByText('Add SIEM Forwarder')).toBeInTheDocument();
  });
});
