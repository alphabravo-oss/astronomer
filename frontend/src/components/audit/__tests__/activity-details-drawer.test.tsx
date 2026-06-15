import { render, screen } from '@testing-library/react';
import { ActivityDetailsDrawer } from '@/components/audit/activity-details-drawer';

describe('ActivityDetailsDrawer', () => {
  it('renders activity fields and JSON detail', () => {
    render(
      <ActivityDetailsDrawer
        title="cluster.delete"
        subtitle={<span>success</span>}
        fields={[
          { label: 'Actor', value: 'admin@example.com' },
          { label: 'Target', value: 'prod-cluster' },
        ]}
        detail={{ request_id: 'req-1' }}
        onClose={() => undefined}
      />
    );

    expect(screen.getByRole('dialog', { name: 'cluster.delete' })).toBeInTheDocument();
    expect(screen.getByText('success')).toBeInTheDocument();
    expect(screen.getByText('Actor')).toBeInTheDocument();
    expect(screen.getByText('admin@example.com')).toBeInTheDocument();
    expect(screen.getByText('Target')).toBeInTheDocument();
    expect(screen.getByText('prod-cluster')).toBeInTheDocument();
    expect(screen.getByText(/request_id/)).toBeInTheDocument();
  });
});
