/**
 * Project namespace assignment — the authoring surface that makes a
 * project-scoped role binding mean something. A project's namespaces ARE its
 * members' cluster-resource grants, so what this card sends and what it refuses
 * to send is an authorization contract, not cosmetics.
 */
import { fireEvent, render, screen } from '@testing-library/react';
import { ProjectNamespacesCard, validateNamespaceName } from '../namespaces-card';

const addMutate = vi.fn();
const removeMutate = vi.fn();

vi.mock('../hooks', () => ({
  useAddProjectNamespace: () => ({ mutate: addMutate, isPending: false }),
  useRemoveProjectNamespace: () => ({ mutate: removeMutate, isPending: false }),
}));

beforeEach(() => {
  addMutate.mockReset();
  removeMutate.mockReset();
});

describe('validateNamespaceName', () => {
  it('accepts RFC 1123 labels', () => {
    for (const ok of ['team-a', 'a', 'ns1', 'a-b-c-9']) {
      expect(validateNamespaceName(ok)).toBeNull();
    }
  });

  it('rejects what the apiserver would reject', () => {
    for (const bad of ['', '   ', 'Team-A', '-lead', 'trail-', 'has space', 'a'.repeat(64)]) {
      expect(validateNamespaceName(bad)).not.toBeNull();
    }
  });
});

describe('ProjectNamespacesCard', () => {
  it('sends the trimmed namespace to add-namespace', () => {
    render(<ProjectNamespacesCard projectId="p1" namespaces={[]} canEdit />);

    fireEvent.change(screen.getByLabelText('Namespace name'), { target: { value: '  team-a  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    expect(addMutate).toHaveBeenCalledTimes(1);
    expect(addMutate.mock.calls[0][0]).toBe('team-a');
  });

  it('does not call the API for an invalid or duplicate namespace', () => {
    render(<ProjectNamespacesCard projectId="p1" namespaces={['team-a']} canEdit />);
    const input = screen.getByLabelText('Namespace name');

    fireEvent.change(input, { target: { value: 'Team A' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(addMutate).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toBeTruthy();

    fireEvent.change(input, { target: { value: 'team-a' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(addMutate).not.toHaveBeenCalled();
  });

  it('removes an assigned namespace', () => {
    render(<ProjectNamespacesCard projectId="p1" namespaces={['team-a', 'team-b']} canEdit />);

    fireEvent.click(screen.getByLabelText('Remove namespace team-b'));

    expect(removeMutate).toHaveBeenCalledWith('team-b');
  });

  it('offers no mutation controls without projects:update', () => {
    render(<ProjectNamespacesCard projectId="p1" namespaces={['team-a']} canEdit={false} />);

    expect(screen.getByText('team-a')).toBeTruthy();
    expect(screen.queryByLabelText('Namespace name')).toBeNull();
    expect(screen.queryByLabelText('Remove namespace team-a')).toBeNull();
  });

  it('says out loud that an unassigned project grants nothing', () => {
    render(<ProjectNamespacesCard projectId="p1" namespaces={[]} canEdit={false} />);

    expect(screen.getByText(/grant nothing on cluster resources/)).toBeTruthy();
  });
});
