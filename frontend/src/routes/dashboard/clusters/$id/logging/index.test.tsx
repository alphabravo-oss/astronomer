import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('@/lib/link', () => ({
  Link: ({ href, children, ...rest }: React.ComponentProps<'a'>) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

vi.mock('@/lib/navigation', () => ({
  useParams: () => ({ id: 'cluster-1' }),
}));

const mockUseCluster = vi.hoisted(() => vi.fn());
const mockUseLoggingAttachStatus = vi.hoisted(() => vi.fn());
const mockUseAttachAstronomerLogs = vi.hoisted(() => vi.fn());
const mockUsePermissionDecision = vi.hoisted(() => vi.fn());

vi.mock('@/lib/hooks', () => ({
  useCluster: () => mockUseCluster(),
  useLoggingAttachStatus: () => mockUseLoggingAttachStatus(),
  useAttachAstronomerLogs: () => mockUseAttachAstronomerLogs(),
}));

vi.mock('@/lib/permission-hooks', () => ({
  usePermissionDecision: (...args: unknown[]) => mockUsePermissionDecision(...args),
}));

vi.mock('@/routes/dashboard/logging/-pipelines-tab', () => ({
  PipelinesTab: () => <div>pipelines</div>,
}));

vi.mock('@/routes/dashboard/logging/-pipeline-modal', () => ({
  CreatePipelineModal: () => null,
}));

import { ClusterLoggingPage } from './-page';

function wrap(node: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}

describe('ClusterLoggingPage attach CTA', () => {
  beforeEach(() => {
    mockUseCluster.mockReturnValue({ data: { id: 'cluster-1', displayName: 'prod' } });
    mockUseAttachAstronomerLogs.mockReturnValue({ mutate: vi.fn(), isPending: false });
    mockUsePermissionDecision.mockReturnValue({ allowed: true });
  });

  it('shows the one-click CTA when ingestPublic is true', () => {
    mockUseLoggingAttachStatus.mockReturnValue({
      data: { ingestPublic: true, attached: false, status: 'healthy' },
    });
    render(wrap(<ClusterLoggingPage />));
    expect(screen.getByTestId('attach-astronomer-logs')).toHaveTextContent('Ship logs to Astronomer');
    expect(screen.getByTestId('attach-astronomer-disclaimer')).toHaveTextContent(
      'convenience, not compliance',
    );
  });

  it('hides the CTA when ingest is not public', () => {
    mockUseLoggingAttachStatus.mockReturnValue({
      data: { ingestPublic: false, attached: false, status: 'healthy' },
    });
    render(wrap(<ClusterLoggingPage />));
    expect(screen.queryByTestId('attach-astronomer-logs')).not.toBeInTheDocument();
    expect(screen.queryByTestId('attach-astronomer-disclaimer')).not.toBeInTheDocument();
  });

  it('hides the CTA without logging:create', () => {
    mockUsePermissionDecision.mockReturnValue({ allowed: false });
    mockUseLoggingAttachStatus.mockReturnValue({
      data: { ingestPublic: true, attached: false, status: 'healthy' },
    });
    render(wrap(<ClusterLoggingPage />));
    expect(screen.queryByTestId('attach-astronomer-logs')).not.toBeInTheDocument();
  });
});
