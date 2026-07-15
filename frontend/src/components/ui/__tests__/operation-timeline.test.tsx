import { fireEvent, render, screen } from '@testing-library/react';
import { OperationTimeline } from '@/components/ui/operation-timeline';

describe('OperationTimeline', () => {
  it('renders header, steps, progress, errors, and actions', () => {
    const onRetry = vi.fn();

    render(
      <OperationTimeline
        header={<span>Phase: applying baseline</span>}
        headerMeta="Started today"
        steps={[
          {
            id: 'namespaces',
            label: 'Create namespaces',
            status: 'success',
            detail: 'namespace: monitoring',
          },
          {
            id: 'policies',
            label: 'Apply policies',
            status: 'running',
            progressPct: 40,
          },
          {
            id: 'operators',
            label: 'Install operators',
            status: 'failed',
            error: 'helm install failed',
            action: <button onClick={onRetry}>Retry</button>,
          },
        ]}
      />,
    );

    expect(screen.getByText('Phase: applying baseline')).toBeInTheDocument();
    expect(screen.getByText('Started today')).toBeInTheDocument();
    expect(screen.getByText('Create namespaces')).toBeInTheDocument();
    expect(screen.getByText('namespace: monitoring')).toBeInTheDocument();
    expect(screen.getByText('Apply policies')).toBeInTheDocument();
    expect(screen.getByText('helm install failed')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('renders an empty label when there are no steps', () => {
    render(
      <OperationTimeline
        header={<span>Phase: pending</span>}
        steps={[]}
        emptyLabel="Waiting for first step..."
      />,
    );

    expect(screen.getByText('Waiting for first step...')).toBeInTheDocument();
  });
});
