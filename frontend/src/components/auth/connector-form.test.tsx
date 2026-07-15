import { act, fireEvent, render, screen } from '@testing-library/react';
import { ConnectorForm } from './connector-form';
import type { DexConnectorTypeSpec } from '@/types';

const spec: DexConnectorTypeSpec = {
  type: 'oidc',
  displayHint: '',
  required: ['clientID', 'clientSecret'],
  optional: [],
  secret: ['clientSecret'],
  nested: [],
};

// The shared axios interceptor camelizes response keys, so the backend's
// `__clientSecret_set` marker arrives on the client as `_ClientSecretSet`.
function editInitial() {
  return {
    name: 'corp',
    displayName: 'Corp',
    enabled: true,
    config: { clientID: 'abc', clientSecret: '', _ClientSecretSet: true } as Record<string, unknown>,
  };
}

async function submit(name: string) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name }));
  });
}

describe('ConnectorForm secret marker handling (camelized by axios interceptor)', () => {
  it('renders the stored-secret placeholder/helper from the camelized marker', () => {
    render(
      <ConnectorForm spec={spec} initial={editInitial()} submitLabel="Save" onSubmit={vi.fn()} isEdit />,
    );
    // Placeholder only renders when the stored-secret marker is recognised.
    expect(screen.getByPlaceholderText('••••••••')).toBeInTheDocument();
    expect(screen.getByText('Stored — type a new value to rotate')).toBeInTheDocument();
  });

  it('submits without re-entering the secret and strips the marker from config', async () => {
    const onSubmit = vi.fn();
    render(
      <ConnectorForm spec={spec} initial={editInitial()} submitLabel="Save" onSubmit={onSubmit} isEdit />,
    );

    await submit('Save');

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const body = onSubmit.mock.calls[0][0] as { config: Record<string, unknown> };
    // Untouched secret is dropped (preserve-on-empty) ...
    expect(body.config).not.toHaveProperty('clientSecret');
    // ... and the camelized marker is not persisted as a garbage config key.
    expect(body.config).not.toHaveProperty('_ClientSecretSet');
    expect(body.config).toEqual({ clientID: 'abc' });
  });

  it('includes the secret once the user actually types a new value', async () => {
    const onSubmit = vi.fn();
    render(
      <ConnectorForm spec={spec} initial={editInitial()} submitLabel="Save" onSubmit={onSubmit} isEdit />,
    );

    fireEvent.change(screen.getByPlaceholderText('••••••••'), { target: { value: 's3cret' } });
    await submit('Save');

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const body = onSubmit.mock.calls[0][0] as { config: Record<string, unknown> };
    expect(body.config).toEqual({ clientID: 'abc', clientSecret: 's3cret' });
  });
});

describe('ConnectorForm validators (ported 1:1 from the imperative checks)', () => {
  const ldapSpec: DexConnectorTypeSpec = {
    type: 'ldap',
    displayHint: '',
    required: ['host', 'bindPW'],
    optional: [],
    secret: ['bindPW'],
    nested: [{ parent: 'userSearch', keys: ['baseDN'] }],
  };

  it('blocks submit on missing name / required fields / required secret / nested keys', async () => {
    const onSubmit = vi.fn();
    render(<ConnectorForm spec={ldapSpec} submitLabel="Create" onSubmit={onSubmit} />);

    await submit('Create');
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText('Name is required')).toBeInTheDocument();
    expect(screen.getByText('Host is required')).toBeInTheDocument();
    expect(screen.getByText('Bind PW is required')).toBeInTheDocument();
    expect(screen.getByText('User Search · Base DN is required')).toBeInTheDocument();
  });

  it('rejects invalid connector names on create', async () => {
    const onSubmit = vi.fn();
    render(<ConnectorForm spec={spec} submitLabel="Create" onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: 'Bad Name!' } });
    await submit('Create');
    expect(onSubmit).not.toHaveBeenCalled();
    expect(
      screen.getByText('Name must be lowercase letters, digits, and dashes'),
    ).toBeInTheDocument();
  });

  it('submits the cleaned body (trimmed name, displayName fallback, nested config)', async () => {
    const onSubmit = vi.fn();
    render(<ConnectorForm spec={ldapSpec} submitLabel="Create" onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'corp-ldap' } });
    fireEvent.change(screen.getByLabelText(/Host/), { target: { value: 'ldap.corp:636' } });
    fireEvent.change(screen.getByLabelText(/Bind password/), { target: { value: 'hunter2' } });
    fireEvent.change(screen.getByLabelText(/Base DN/), { target: { value: 'ou=People,dc=corp' } });
    await submit('Create');

    expect(onSubmit).toHaveBeenCalledWith({
      name: 'corp-ldap',
      displayName: 'corp-ldap',
      enabled: true,
      config: {
        host: 'ldap.corp:636',
        bindPW: 'hunter2',
        userSearch: { baseDN: 'ou=People,dc=corp' },
      },
    });
  });
});
