import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type { LoggingOutput } from '@/types';

vi.mock('@/lib/toast', () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock('@/lib/api', () => ({
  deleteLoggingOutput: vi.fn(),
  updateLoggingOutput: vi.fn(),
}));

vi.mock('@/lib/hooks', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/hooks')>();
  return {
    ...actual,
    useLoggingOutputs: vi.fn(),
    useTestLoggingOutput: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  };
});

import { useLoggingOutputs } from '@/lib/hooks';
import { OutputsTab } from './-outputs-tab';

const useOutputs = vi.mocked(useLoggingOutputs);

function wrap(node: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}

const systemRow: LoggingOutput = {
  id: 'sys-1',
  name: 'Astronomer logs',
  outputType: 'loki',
  clusterId: 'cluster-1',
  enabled: true,
  isSystem: true,
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

const byoRow: LoggingOutput = {
  id: 'byo-1',
  name: 'Splunk HEC',
  outputType: 'splunk',
  clusterId: 'cluster-1',
  enabled: true,
  isSystem: false,
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

describe('OutputsTab system destination', () => {
  beforeEach(() => {
    useOutputs.mockReturnValue({
      data: [systemRow, byoRow],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useLoggingOutputs>);
  });

  it('shows a System badge and hides delete for is_system rows', () => {
    render(wrap(<OutputsTab />));
    expect(screen.getByTestId('system-output-badge')).toHaveTextContent('System');
    expect(screen.getByText('Astronomer logs')).toBeInTheDocument();
    const deleteButtons = screen.getAllByTitle('Delete output');
    expect(deleteButtons).toHaveLength(1);
  });
});
