import { ReactNode } from 'react';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import ClusterGatekeeperPage from './page';
import type { GatekeeperConstraint } from '@/types';

jest.mock('@/lib/navigation', () => ({
  useParams: () => ({ id: 'cl1' }),
  useRouter: () => ({ push: jest.fn() }),
}));

jest.mock('@/lib/hooks', () => ({
  useCluster: () => ({ data: { id: 'cl1', displayName: 'prod-east' }, isLoading: false }),
}));

const mockUseClustersUpdate = jest.fn();
jest.mock('@/lib/permission-hooks', () => ({
  useClustersUpdate: () => mockUseClustersUpdate(),
}));

jest.mock('./hooks', () => ({
  useGatekeeperConstraints: jest.fn(),
  useValidateConstraint: () => ({ mutateAsync: jest.fn(), isPending: false }),
  useApplyConstraint: () => ({ mutateAsync: jest.fn(), isPending: false }),
  useDeleteConstraint: () => ({ mutateAsync: jest.fn(), isPending: false }),
}));

import { useGatekeeperConstraints } from './hooks';

const mockedList = useGatekeeperConstraints as jest.Mock;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const bundleConstraint: GatekeeperConstraint = {
  name: 'require-team-label',
  kind: 'K8sRequiredLabels',
  apiVersion: 'constraints.gatekeeper.sh/v1beta1',
  source: 'bundle',
  enforcementAction: 'deny',
  violationCount: 4,
};

describe('ClusterGatekeeperPage', () => {
  beforeEach(() => jest.clearAllMocks());

  it('renders constraint rows with violation counts', () => {
    mockUseClustersUpdate.mockReturnValue({ canWrite: true, reason: '' });
    mockedList.mockReturnValue({ data: [bundleConstraint], isLoading: false, isError: false, refetch: jest.fn() });
    render(<ClusterGatekeeperPage />, { wrapper });

    expect(screen.getByText('require-team-label')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    // Apply is enabled for a writer.
    expect(screen.getByRole('button', { name: /Apply/i })).not.toBeDisabled();
  });

  it('disables Apply when the user lacks cluster write permission', () => {
    mockUseClustersUpdate.mockReturnValue({ canWrite: false, reason: 'You need clusters:update' });
    mockedList.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: jest.fn() });
    render(<ClusterGatekeeperPage />, { wrapper });

    expect(screen.getByRole('button', { name: /Apply/i })).toBeDisabled();
    // Validate stays available to non-writers.
    expect(screen.getByRole('button', { name: /Validate/i })).not.toBeDisabled();
  });
});
