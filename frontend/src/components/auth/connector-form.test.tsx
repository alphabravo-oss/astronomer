import { fireEvent, render, screen } from '@testing-library/react';
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

describe('ConnectorForm secret marker handling (camelized by axios interceptor)', () => {
  it('renders the stored-secret placeholder/helper from the camelized marker', () => {
    render(
      <ConnectorForm spec={spec} initial={editInitial()} submitLabel="Save" onSubmit={vi.fn()} isEdit />,
    );
    // Placeholder only renders when the stored-secret marker is recognised.
    expect(screen.getByPlaceholderText('••••••••')).toBeInTheDocument();
    expect(screen.getByText('Stored — leave blank to keep current value')).toBeInTheDocument();
  });

  it('submits without re-entering the secret and strips the marker from config', () => {
    const onSubmit = vi.fn();
    render(
      <ConnectorForm spec={spec} initial={editInitial()} submitLabel="Save" onSubmit={onSubmit} isEdit />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const body = onSubmit.mock.calls[0][0] as { config: Record<string, unknown> };
    // Untouched secret is dropped (preserve-on-empty) ...
    expect(body.config).not.toHaveProperty('clientSecret');
    // ... and the camelized marker is not persisted as a garbage config key.
    expect(body.config).not.toHaveProperty('_ClientSecretSet');
    expect(body.config).toEqual({ clientID: 'abc' });
  });
});
