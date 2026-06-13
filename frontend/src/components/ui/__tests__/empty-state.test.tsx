import { fireEvent, render, screen } from '@testing-library/react';
import { Lock, Plus } from 'lucide-react';
import { EmptyState } from '@/components/ui/empty-state';

describe('EmptyState', () => {
  it('renders title and description', () => {
    render(
      <EmptyState
        icon={Lock}
        title="Admins only"
        description="This surface is gated to platform administrators."
      />,
    );

    expect(screen.getByText('Admins only')).toBeInTheDocument();
    expect(screen.getByText('This surface is gated to platform administrators.')).toBeInTheDocument();
  });

  it('renders an action button and invokes the handler', async () => {
    const onAction = jest.fn();

    render(
      <EmptyState
        icon={Plus}
        title="No storage locations yet"
        description="Add a storage target before scheduling backups."
        actionLabel="Add Storage"
        actionIcon={Plus}
        onAction={onAction}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: /add storage/i }));

    expect(onAction).toHaveBeenCalledTimes(1);
  });

  it('disables action buttons', () => {
    const onAction = jest.fn();

    render(
      <EmptyState
        icon={Plus}
        title="No schedules configured"
        description="Create a storage location first."
        actionLabel="Create Schedule"
        actionIcon={Plus}
        onAction={onAction}
        disabled
      />,
    );

    expect(screen.getByRole('button', { name: /create schedule/i })).toBeDisabled();
  });

  it('renders action links', () => {
    render(
      <EmptyState
        icon={Lock}
        title="Section disabled"
        description="This section is disabled by platform settings."
        actionLabel="Back to dashboard"
        actionHref="/dashboard"
      />,
    );

    expect(screen.getByRole('link', { name: /back to dashboard/i })).toHaveAttribute('href', '/dashboard');
  });
});
