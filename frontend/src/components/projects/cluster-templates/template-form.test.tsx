import { act, fireEvent, render, screen } from '@testing-library/react';
import { TemplateForm } from './template-form';
import type { ClusterTemplateSpec, ClusterTemplateWriteRequest } from '@/lib/api/project-detail';

// ToolsEditor pulls the catalog via react-query; its contract with the form
// (value + onChange) is what matters here.
vi.mock('@/lib/hooks', () => ({
  useTools: () => ({ data: [], isLoading: false }),
}));

async function submit(name: RegExp) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name }));
  });
}

describe('TemplateForm (create)', () => {
  it('ports every prior validation check as a validator', async () => {
    const onSubmit = vi.fn();
    render(<TemplateForm onSubmit={onSubmit} />);

    await submit(/Create template/);
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText('Name is required')).toBeInTheDocument();
    expect(screen.getByText('Display name is required')).toBeInTheDocument();

    // Token rotation must be > 0 — the field validates even while its
    // section is collapsed (hidden, not unmounted).
    fireEvent.change(screen.getByLabelText(/Token rotation \(days\)/), { target: { value: '0' } });
    await submit(/Create template/);
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText('Token rotation days must be > 0')).toBeInTheDocument();
  });

  it('slug-cases the name as the user types (today\'s onChange transform)', () => {
    render(<TemplateForm onSubmit={vi.fn()} />);
    const input = screen.getByLabelText(/^Name/);
    fireEvent.change(input, { target: { value: 'Prod Template!' } });
    expect(input).toHaveValue('prod-template-');
  });

  it('submits the identical default body (defaultSpec shape, null quotas)', async () => {
    const onSubmit = vi.fn();
    render(<TemplateForm onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'prod-template' } });
    fireEvent.change(screen.getByLabelText(/Display name/), {
      target: { value: 'Production Template' },
    });

    await submit(/Create template/);
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'prod-template',
      displayName: 'Production Template',
      description: undefined,
      spec: {
        environment: 'development',
        labels: [],
        tools: [],
        defaultProject: {
          name: '',
          podSecurityProfile: 'baseline',
          resourceQuotaCpu: null,
          resourceQuotaMemory: null,
          resourceQuotaPods: null,
          networkPolicyMode: 'isolated',
        },
        registrationPolicy: {
          tokenRotationDays: 90,
          requireApproval: false,
        },
      },
    } satisfies ClusterTemplateWriteRequest);
  });
});

describe('TemplateForm (edit)', () => {
  const spec: ClusterTemplateSpec = {
    environment: 'production',
    labels: [{ key: 'team', value: 'core' }],
    tools: [{ slug: 'argocd', preset: 'default', valuesOverride: '' }],
    defaultProject: {
      name: 'default-{cluster}',
      podSecurityProfile: 'restricted',
      resourceQuotaCpu: '4',
      resourceQuotaMemory: '8Gi',
      resourceQuotaPods: 50,
      networkPolicyMode: 'none',
    },
    registrationPolicy: { tokenRotationDays: 30, requireApproval: true },
  };

  it('round-trips the initial spec (quota string/number conversions intact)', async () => {
    const onSubmit = vi.fn();
    render(
      <TemplateForm
        isEdit
        initial={{ name: 'prod', displayName: 'Prod', description: 'desc', spec }}
        onSubmit={onSubmit}
      />,
    );

    expect(screen.getByLabelText(/^Name/)).toBeDisabled();

    await submit(/Save template/);
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'prod',
      displayName: 'Prod',
      description: 'desc',
      spec,
    });
  });

  it('renders the server error', () => {
    render(
      <TemplateForm
        isEdit
        initial={{ name: 'prod', displayName: 'Prod', spec }}
        serverError="boom from server"
        onSubmit={vi.fn()}
      />,
    );
    expect(screen.getByText('boom from server')).toBeInTheDocument();
  });
});
