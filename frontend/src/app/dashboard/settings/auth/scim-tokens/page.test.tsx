import { ReactNode } from 'react';
import { act, render, screen, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useAuthStore } from '@/lib/store';
import SCIMTokensPage from './page';
import type { SCIMToken } from '@/types';

jest.mock('@/lib/toast', () => ({
  toastSuccess: jest.fn(),
  toastError: jest.fn(),
  toastApiError: jest.fn(),
}));

jest.mock('./hooks', () => ({
  useSCIMTokens: jest.fn(),
  useCreateSCIMToken: () => ({ mutateAsync: jest.fn(), isPending: false }),
  useRevokeSCIMToken: () => ({ mutateAsync: jest.fn(), isPending: false }),
}));

import { useSCIMTokens } from './hooks';

const mockedList = useSCIMTokens as jest.Mock;

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

const token: SCIMToken = {
  id: 't1',
  name: 'okta-provisioning',
  prefix: 'astro_scim_ab',
  lastUsedAt: null,
  createdAt: '2026-07-01T00:00:00Z',
};

describe('SCIMTokensPage', () => {
  beforeEach(() => jest.clearAllMocks());
  afterEach(() => {
    act(() => useAuthStore.setState({ user: null, isAuthenticated: false }));
  });

  it('renders token metadata and opens the mint modal for a superuser', () => {
    setSuperuser();
    mockedList.mockReturnValue({ data: [token], isLoading: false, isError: false, refetch: jest.fn() });
    render(<SCIMTokensPage />, { wrapper });

    expect(screen.getByText('okta-provisioning')).toBeInTheDocument();
    expect(screen.getByText('Never')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Mint Token/i }));
    expect(screen.getByText('Mint SCIM Token')).toBeInTheDocument();
  });
});
