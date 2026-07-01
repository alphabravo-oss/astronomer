import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { RestoreModal } from './restore-modal';
import type { BackupRun } from '@/types';

// Router is only used for the post-success redirect; a no-op push is enough.
jest.mock('@/lib/navigation', () => ({
  useRouter: () => ({ push: jest.fn() }),
}));

// Capture the restore-creation call so we can assert on the request body.
const mockMutateAsync = jest.fn().mockResolvedValue({ id: 'r1' });
jest.mock('./hooks', () => ({
  useB2CreateRestore: () => ({ mutateAsync: mockMutateAsync, isPending: false }),
}));

function makeBackup(): BackupRun {
  return {
    id: 'b1',
    name: 'daily',
    includedNamespaces: ['prod', 'staging'],
  } as unknown as BackupRun;
}

beforeEach(() => {
  mockMutateAsync.mockClear();
});

describe('RestoreModal namespace selection', () => {
  it('blocks submit when every namespace is deselected (never widens to restore-all)', () => {
    render(<RestoreModal backup={makeBackup()} onClose={jest.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'prod' }));
    fireEvent.click(screen.getByRole('button', { name: 'staging' }));
    fireEvent.change(screen.getByPlaceholderText('daily'), { target: { value: 'daily' } });

    const start = screen.getByRole('button', { name: 'Start Restore' });
    expect(start).toBeDisabled();
    expect(screen.getByText('Select at least one namespace to restore.')).toBeInTheDocument();

    fireEvent.click(start);
    expect(mockMutateAsync).not.toHaveBeenCalled();
  });

  it('sends the explicit subset when a strict subset is selected', async () => {
    render(<RestoreModal backup={makeBackup()} onClose={jest.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'staging' })); // deselect staging
    fireEvent.change(screen.getByPlaceholderText('daily'), { target: { value: 'daily' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start Restore' }));

    await waitFor(() => expect(mockMutateAsync).toHaveBeenCalledTimes(1));
    expect(mockMutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ included_namespaces: ['prod'] }),
    );
  });

  it('omits the filter (restore everything) only when the full set is selected', async () => {
    render(<RestoreModal backup={makeBackup()} onClose={jest.fn()} />);

    fireEvent.change(screen.getByPlaceholderText('daily'), { target: { value: 'daily' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start Restore' }));

    await waitFor(() => expect(mockMutateAsync).toHaveBeenCalledTimes(1));
    expect(mockMutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ included_namespaces: undefined }),
    );
  });
});
