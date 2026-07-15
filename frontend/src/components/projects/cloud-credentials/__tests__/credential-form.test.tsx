/**
 * P5.2 pilot — TanStack Form conversion of the cloud-credential form.
 * Locks the pre-conversion contract: submit body shape, every prior
 * validation check (now as validators), and untouched-secret omission.
 */
import { act, fireEvent, render, screen } from '@testing-library/react';
import { CredentialForm } from '../credential-form';
import type {
  CloudCredentialProviderSpec,
  CloudCredentialWriteRequest,
} from '@/lib/api/project-detail';

// TargetRefsEditor pulls clusters/namespaces via react-query; the form's
// contract with it (value + onChange) is what matters here.
vi.mock('../target-refs-editor', () => ({
  TargetRefsEditor: () => <div data-testid="target-refs" />,
}));

const spec: CloudCredentialProviderSpec = {
  provider: 'aws',
  displayName: 'Amazon Web Services',
  fields: [
    { name: 'accessKeyId', label: 'Access key ID', required: true, secret: true },
    { name: 'secretAccessKey', label: 'Secret access key', required: false, secret: true },
    { name: 'region', label: 'Region', required: true, secret: false },
  ],
};

async function submit(name: RegExp) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name }));
  });
}

describe('CredentialForm (create)', () => {
  it('ports every prior validation check as a validator', async () => {
    const onSubmit = vi.fn();
    render(<CredentialForm provider="aws" spec={spec} onSubmit={onSubmit} />);

    await submit(/Create credential/);
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText('Name is required')).toBeInTheDocument();
    expect(screen.getByText('Access key ID is required')).toBeInTheDocument();
    expect(screen.getByText('Region is required')).toBeInTheDocument();
    // Optional secret does not error.
    expect(screen.queryByText('Secret access key is required')).not.toBeInTheDocument();
  });

  it('slug-cases the name as the user types (today\'s onChange transform)', () => {
    render(<CredentialForm provider="aws" spec={spec} onSubmit={vi.fn()} />);
    const input = screen.getByLabelText(/Name/);
    fireEvent.change(input, { target: { value: 'My AWS Keys!' } });
    expect(input).toHaveValue('my-aws-keys-');
  });

  it('submits the identical body: trimmed name, provider, undefined empty description, spec-only config', async () => {
    const onSubmit = vi.fn();
    render(<CredentialForm provider="aws" spec={spec} onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: 'my-aws-keys' } });
    fireEvent.change(screen.getByLabelText(/Access key ID/), { target: { value: 'AKIA123' } });
    fireEvent.change(screen.getByLabelText(/Region/), { target: { value: 'us-east-1' } });

    await submit(/Create credential/);
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'my-aws-keys',
      provider: 'aws',
      description: undefined,
      config: { accessKeyId: 'AKIA123', region: 'us-east-1' },
      targetRefs: [],
    } satisfies CloudCredentialWriteRequest);
  });
});

describe('CredentialForm (edit) — secret round-trip', () => {
  const initial = {
    name: 'my-aws-keys',
    description: 'prod keys',
    config: { accessKeyId: '', secretAccessKey: '', region: 'us-east-1' },
    targetRefs: [{ clusterId: 'c1', namespaces: ['default'] }],
    secretsSet: new Set(['accessKeyId', 'secretAccessKey']),
  };

  it('shows the stored placeholder + hint and omits untouched secrets on submit', async () => {
    const onSubmit = vi.fn();
    render(
      <CredentialForm provider="aws" spec={spec} isEdit initial={initial} onSubmit={onSubmit} />,
    );

    expect(screen.getAllByPlaceholderText('••••••••')).toHaveLength(2);
    expect(screen.getAllByText('Stored — type a new value to rotate')).toHaveLength(2);
    // Stored secrets satisfy the required check without retyping.
    await submit(/Save credential/);
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'my-aws-keys',
      provider: 'aws',
      description: 'prod keys',
      config: { region: 'us-east-1' },
      targetRefs: initial.targetRefs,
    });
  });

  it('includes only the secret the user actually rotated', async () => {
    const onSubmit = vi.fn();
    render(
      <CredentialForm provider="aws" spec={spec} isEdit initial={initial} onSubmit={onSubmit} />,
    );

    fireEvent.change(screen.getByLabelText(/Access key ID/), { target: { value: 'AKIA-NEW' } });
    await submit(/Save credential/);
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        config: { accessKeyId: 'AKIA-NEW', region: 'us-east-1' },
      }),
    );
  });

  it('disables the name input in edit mode and renders the server error', () => {
    render(
      <CredentialForm
        provider="aws"
        spec={spec}
        isEdit
        initial={initial}
        serverError="boom from server"
        onSubmit={vi.fn()}
      />,
    );
    expect(screen.getByLabelText(/Name/)).toBeDisabled();
    expect(screen.getByText('boom from server')).toBeInTheDocument();
  });
});
