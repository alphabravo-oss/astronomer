import { act, fireEvent, render, screen } from '@testing-library/react';
import { useAppForm } from '@/lib/form';

/** Click Save and flush the async form.handleSubmit() microtasks. */
async function submitForm() {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  });
}

function TextHarness({ onSubmit }: { onSubmit: (value: { host: string }) => void }) {
  const form = useAppForm({
    defaultValues: { host: '' },
    onSubmit: ({ value }) => onSubmit(value),
  });
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
    >
      <form.AppField
        name="host"
        validators={{
          onChange: ({ value }) => (value.trim() ? undefined : 'Host is required'),
        }}
      >
        {(field) => (
          <field.TextField label="Host" helper="SMTP host" required placeholder="smtp.example.com" />
        )}
      </form.AppField>
      <form.AppForm>
        <form.SubmitButton>Save</form.SubmitButton>
      </form.AppForm>
    </form>
  );
}

describe('TextField a11y wiring and error display', () => {
  it('associates the label via generated id + htmlFor', () => {
    render(<TextHarness onSubmit={vi.fn()} />);
    const input = screen.getByLabelText(/Host/);
    expect(input).toHaveAttribute('placeholder', 'smtp.example.com');
    expect(screen.getByText('SMTP host')).toBeInTheDocument();
    expect(input).not.toHaveAttribute('aria-invalid');
  });

  it('shows the validator error with aria-invalid + aria-describedby, replacing the helper', () => {
    render(<TextHarness onSubmit={vi.fn()} />);
    const input = screen.getByLabelText(/Host/);
    fireEvent.change(input, { target: { value: 'x' } });
    fireEvent.change(input, { target: { value: '' } });

    const error = screen.getByText('Host is required');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAttribute('aria-describedby', error.id);
    expect(screen.queryByText('SMTP host')).not.toBeInTheDocument();

    // Fixing the value clears the error and restores the helper.
    fireEvent.change(input, { target: { value: 'smtp.corp' } });
    expect(screen.queryByText('Host is required')).not.toBeInTheDocument();
    expect(screen.getByText('SMTP host')).toBeInTheDocument();
    expect(input).not.toHaveAttribute('aria-invalid');
  });

  it('disables SubmitButton while invalid and submits the form value when valid', async () => {
    const onSubmit = vi.fn();
    render(<TextHarness onSubmit={onSubmit} />);
    const input = screen.getByLabelText(/Host/);
    const button = screen.getByRole('button', { name: 'Save' });

    fireEvent.change(input, { target: { value: 'x' } });
    fireEvent.change(input, { target: { value: '' } });
    expect(button).toBeDisabled();

    fireEvent.change(input, { target: { value: 'smtp.corp' } });
    expect(button).not.toBeDisabled();
    await act(async () => {
      fireEvent.click(button);
    });
    expect(onSubmit).toHaveBeenCalledWith({ host: 'smtp.corp' });
  });
});

function ControlsHarness({ onSubmit }: { onSubmit: (value: unknown) => void }) {
  const form = useAppForm({
    defaultValues: { port: 587, encryption: 'starttls', requireTls: true, prune: false },
    onSubmit: ({ value }) => onSubmit(value),
  });
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
    >
      <form.AppField name="port">
        {(field) => <field.NumberField label="Port" min={1} />}
      </form.AppField>
      <form.AppField name="encryption">
        {(field) => (
          <field.SelectField label="Encryption">
            <option value="starttls">STARTTLS</option>
            <option value="tls">TLS</option>
            <option value="none">None</option>
          </field.SelectField>
        )}
      </form.AppField>
      <form.AppField name="requireTls">
        {(field) => <field.SwitchField label="Require TLS" helper="Reject non-TLS connections" />}
      </form.AppField>
      <form.AppField name="prune">
        {(field) => <field.CheckboxField label="Prune resources" helper="Delete removed resources" />}
      </form.AppField>
      <form.AppForm>
        <form.SubmitButton>Save</form.SubmitButton>
      </form.AppForm>
    </form>
  );
}

describe('Number/Select/Switch/Checkbox fields', () => {
  it('wires each control to the form value with label association', async () => {
    const onSubmit = vi.fn();
    render(<ControlsHarness onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText('Port'), { target: { value: '2525' } });
    fireEvent.change(screen.getByLabelText('Encryption'), { target: { value: 'tls' } });

    const toggle = screen.getByRole('switch', { name: 'Require TLS' });
    expect(toggle).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-checked', 'false');

    const checkbox = screen.getByLabelText('Prune resources');
    fireEvent.click(checkbox);
    expect(checkbox).toBeChecked();

    await submitForm();
    expect(onSubmit).toHaveBeenCalledWith({
      port: 2525,
      encryption: 'tls',
      requireTls: false,
      prune: true,
    });
  });
});
