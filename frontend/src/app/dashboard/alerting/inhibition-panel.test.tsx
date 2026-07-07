import { ReactNode } from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { InhibitionPanel } from './inhibition-panel';
import type { AlertInhibition } from '@/types';

jest.mock('./inhibition-hooks', () => ({
  useInhibitions: jest.fn(),
  useCreateInhibition: () => ({ mutateAsync: jest.fn(), isPending: false }),
  useUpdateInhibition: () => ({ mutateAsync: jest.fn(), isPending: false }),
  useDeleteInhibition: () => ({ mutateAsync: jest.fn(), isPending: false }),
}));

import { useInhibitions } from './inhibition-hooks';

const mockedUseInhibitions = useInhibitions as jest.Mock;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const sample: AlertInhibition = {
  id: 'i1',
  name: 'Suppress node alerts',
  sourceMatchers: [{ label: 'alertname', value: 'ClusterDown', isRegex: false }],
  targetMatchers: [{ label: 'severity', value: 'warn.*', isRegex: true }],
  equalLabels: ['cluster'],
  enabled: true,
  createdAt: '2026-07-01T00:00:00Z',
  updatedAt: '2026-07-01T00:00:00Z',
};

describe('InhibitionPanel', () => {
  beforeEach(() => jest.clearAllMocks());

  it('renders inhibition rows with regex-aware matcher chips', () => {
    mockedUseInhibitions.mockReturnValue({ data: [sample], isLoading: false, isError: false, refetch: jest.fn() });
    render(<InhibitionPanel />, { wrapper });

    expect(screen.getByText('Suppress node alerts')).toBeInTheDocument();
    // Regex matcher uses `=~`; exact matcher uses `=`.
    expect(screen.getByText('severity=~warn.*')).toBeInTheDocument();
    expect(screen.getByText('alertname=ClusterDown')).toBeInTheDocument();
  });

  it('opens the create modal when the create button is clicked', () => {
    mockedUseInhibitions.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: jest.fn() });
    render(<InhibitionPanel />, { wrapper });

    expect(screen.queryByText('Create Inhibition Rule')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Create Inhibition/i }));
    expect(screen.getByText('Create Inhibition Rule')).toBeInTheDocument();
    // Source + target matcher editors are present (label also appears as an
    // empty-table column header, so there are 2 matches for each).
    expect(screen.getAllByText('Source matchers').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Target matchers').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Equal labels').length).toBeGreaterThan(0);
  });
});
