import type { Mock } from 'vitest';
import { ReactNode } from 'react';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ClusterGatekeeperPage } from './-page';
import type { GatekeeperConstraint } from '@/types';

// Plain-anchor stand-in: these tests assert link text/href, not routing, and
// the real Link needs a <RouterProvider>.
vi.mock('@/lib/link', () => ({
  Link: ({ href, children, ...rest }: React.ComponentProps<'a'>) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

vi.mock('@/lib/navigation', () => ({
  useParams: () => ({ id: 'cl1' }),
  useRouter: () => ({ push: vi.fn() }),
}));

vi.mock('@/lib/hooks', () => ({
  useCluster: () => ({ data: { id: 'cl1', displayName: 'prod-east' }, isLoading: false }),
}));

// vi.mock factories are hoisted above this const, so hoist it alongside them.
const mockUseClustersUpdate = vi.hoisted(() => vi.fn());
vi.mock('@/lib/permission-hooks', () => ({
  useClustersUpdate: () => mockUseClustersUpdate(),
}));

vi.mock('./-hooks', () => ({
  useGatekeeperConstraints: vi.fn(),
  useValidateConstraint: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useApplyConstraint: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteConstraint: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

import { useGatekeeperConstraints } from './-hooks';

const mockedList = useGatekeeperConstraints as Mock;

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
  beforeEach(() => vi.clearAllMocks());

  it('renders constraint rows with violation counts', () => {
    mockUseClustersUpdate.mockReturnValue({ canWrite: true, reason: '' });
    mockedList.mockReturnValue({ data: [bundleConstraint], isLoading: false, isError: false, refetch: vi.fn() });
    render(<ClusterGatekeeperPage />, { wrapper });

    expect(screen.getByText('require-team-label')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    // Apply is enabled for a writer.
    expect(screen.getByRole('button', { name: /Apply/i })).not.toBeDisabled();
  });

  it('disables Apply when the user lacks cluster write permission', () => {
    mockUseClustersUpdate.mockReturnValue({ canWrite: false, reason: 'You need clusters:update' });
    mockedList.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() });
    render(<ClusterGatekeeperPage />, { wrapper });

    expect(screen.getByRole('button', { name: /Apply/i })).toBeDisabled();
    // Validate stays available to non-writers.
    expect(screen.getByRole('button', { name: /Validate/i })).not.toBeDisabled();
  });
});
