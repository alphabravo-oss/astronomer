import type { MockedFunction } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { ExtForm, buildSubmitBody, missingRequired } from './ExtForm';
import * as extensionsApi from '@/lib/api/extensions';
import type { FormInput, FormSpec } from '@/lib/api/extensions';

vi.mock('@/lib/api/extensions', () => ({
  __esModule: true,
  fetchExtensionData: vi.fn(),
}));

const mockedFetch = extensionsApi.fetchExtensionData as MockedFunction<
  typeof extensionsApi.fetchExtensionData
>;

const inputs: FormInput[] = [
  { name: 'title', label: 'Title', type: 'text', required: true },
  { name: 'count', label: 'Count', type: 'number', required: false },
  { name: 'enabled', label: 'Enabled', type: 'toggle', required: false },
];

describe('buildSubmitBody', () => {
  it('coerces number/toggle/text and drops blank numbers', () => {
    expect(buildSubmitBody(inputs, { title: 'hi', count: '5', enabled: true })).toEqual({
      title: 'hi',
      count: 5,
      enabled: true,
    });
    expect(buildSubmitBody(inputs, { title: 'hi', count: '', enabled: false })).toEqual({
      title: 'hi',
      enabled: false,
    });
  });
});

describe('missingRequired', () => {
  it('flags an empty required text field', () => {
    expect(missingRequired(inputs, { title: '', count: '', enabled: false })).toEqual(['title']);
  });

  it('passes when required fields are filled', () => {
    expect(missingRequired(inputs, { title: 'ok', count: '', enabled: false })).toEqual([]);
  });

  it('treats a required toggle as missing until checked', () => {
    const req: FormInput[] = [{ name: 'agree', label: 'Agree', type: 'toggle', required: true }];
    expect(missingRequired(req, { agree: false })).toEqual(['agree']);
    expect(missingRequired(req, { agree: true })).toEqual([]);
  });
});

describe('ExtForm submit', () => {
  afterEach(() => vi.clearAllMocks());

  const spec: FormSpec = {
    submit: 'createThing',
    submitLabel: 'Create',
    inputs: [{ name: 'title', label: 'Title', type: 'text', required: true }],
  };

  it('disables submit until a required field is filled, then POSTs through the proxy', async () => {
    mockedFetch.mockResolvedValue({ data: {}, shape: 'object', meta: { dataSourceId: 'createThing' } });
    render(<ExtForm extensionName="cost" spec={spec} context={{ clusterId: 'c1' }} />);

    const button = screen.getByRole('button', { name: 'Create' });
    expect(button).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/Title/), { target: { value: 'Widget' } });
    expect(button).toBeEnabled();

    fireEvent.click(button);

    await waitFor(() =>
      expect(mockedFetch).toHaveBeenCalledWith('cost', 'createThing', {
        context: { clusterId: 'c1' },
        body: { title: 'Widget' },
      }),
    );
    expect(await screen.findByText('Submitted.')).toBeInTheDocument();
  });

  it('surfaces a submit error', async () => {
    mockedFetch.mockRejectedValue(new Error('extension_rbac_denied'));
    render(<ExtForm extensionName="cost" spec={spec} />);
    fireEvent.change(screen.getByLabelText(/Title/), { target: { value: 'X' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('extension_rbac_denied');
  });
});
