import { act, fireEvent, render, screen } from '@testing-library/react';
import { camelizeKeys } from '@/lib/camelize';
import { useAppForm } from '@/lib/form';
import { isStoredSecret, secretMarkerKey, stripUntouchedSecrets } from '../secrets';

// Literal of SMTP_REDACTED_SENTINEL (lib/api/settings.ts) — not imported so
// this kit test doesn't drag the whole axios api module into jsdom.
const SENTINEL = '__redacted__';

const STORED_HINT = 'Stored — type a new value to rotate';

/** Click Save and flush the async form.handleSubmit() microtasks. */
async function submitForm() {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  });
}

describe('secretMarkerKey / isStoredSecret (axios camelize interaction)', () => {
  it('recomputes exactly the key the camelize interceptor produces', () => {
    // Wire shape → what the axios response interceptor hands the app.
    const wire = { client_id: 'abc', client_secret: '', __client_secret_set: true };
    const camelized = camelizeKeys<Record<string, unknown>>(wire);
    expect(camelized).toEqual({ clientId: 'abc', clientSecret: '', _ClientSecretSet: true });
    expect(camelized[secretMarkerKey('clientSecret')]).toBe(true);
  });

  it('detects the marker under both raw and camelized spellings', () => {
    expect(isStoredSecret({ __clientSecret_set: true }, 'clientSecret')).toBe(true);
    expect(isStoredSecret({ _ClientSecretSet: true }, 'clientSecret')).toBe(true);
    expect(isStoredSecret({ clientSecret: '' }, 'clientSecret')).toBe(false);
    expect(isStoredSecret(undefined, 'clientSecret')).toBe(false);
  });
});

describe('stripUntouchedSecrets (pure)', () => {
  const dirtyForm = (dirty: Record<string, boolean>) => ({
    getFieldMeta: (name: never) => ({ isDirty: dirty[name as string] ?? false }),
  });

  it('drops pristine secrets, keeps dirty ones, strips markers in both spellings', () => {
    const value = {
      clientId: 'abc',
      clientSecret: 'typed',
      bindPW: '',
      __bindPW_set: true,
      _ClientSecretSet: true,
    };
    const cleaned = stripUntouchedSecrets(
      value,
      dirtyForm({ clientSecret: true }),
      ['clientSecret', 'bindPW'],
    );
    expect(cleaned).toEqual({ clientId: 'abc', clientSecret: 'typed' });
  });

  it('strips any generic __<key>_set echo even for keys not listed as secrets', () => {
    const cleaned = stripUntouchedSecrets({ a: 1, __other_set: true }, dirtyForm({}), []);
    expect(cleaned).toEqual({ a: 1 });
  });
});

// ============================================================
// Marker variant, through the real kit — including the D14 check.
// ============================================================

type MarkerFormRef = {
  current: {
    reset: (values: { config: Record<string, unknown> }) => void;
    getFieldMeta: (field: never) => { isDirty: boolean } | undefined;
  } | null;
};

function MarkerHarness({
  initial,
  onSubmit,
  formRef,
}: {
  initial: Record<string, unknown>;
  onSubmit: (body: Record<string, unknown>) => void;
  formRef: MarkerFormRef;
}) {
  const form = useAppForm({
    defaultValues: { config: initial },
    onSubmit: ({ value }) =>
      onSubmit(stripUntouchedSecrets(value.config, form, ['clientSecret'], 'config.')),
  });
  formRef.current = form as unknown as MarkerFormRef['current'];
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
    >
      <form.AppField name="config.clientSecret">
        {(field) => (
          <field.SecretField label="Client secret" stored={isStoredSecret(initial, 'clientSecret')} />
        )}
      </form.AppField>
      <button type="submit">Save</button>
    </form>
  );
}

function markerInitial() {
  // Exactly what an edit page sees: wire response after axios camelization.
  return camelizeKeys<Record<string, unknown>>({
    client_id: 'abc',
    client_secret: '',
    __client_secret_set: true,
  });
}

describe('SecretField + stripUntouchedSecrets (marker variant)', () => {
  it('renders the stored placeholder + rotate hint while pristine and omits the secret on submit', async () => {
    const onSubmit = vi.fn();
    render(<MarkerHarness initial={markerInitial()} onSubmit={onSubmit} formRef={{ current: null }} />);

    expect(screen.getByPlaceholderText('••••••••')).toBeInTheDocument();
    expect(screen.getByText(STORED_HINT)).toBeInTheDocument();

    await submitForm();
    // Untouched secret dropped (preserve-on-empty) and the camelized marker
    // is not persisted as a garbage config key.
    expect(onSubmit).toHaveBeenCalledWith({ clientId: 'abc' });
  });

  it('includes the secret once the user types, and hides the rotate hint', async () => {
    const onSubmit = vi.fn();
    render(<MarkerHarness initial={markerInitial()} onSubmit={onSubmit} formRef={{ current: null }} />);

    fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 's3cret' } });
    expect(screen.queryByText(STORED_HINT)).not.toBeInTheDocument();

    await submitForm();
    expect(onSubmit).toHaveBeenCalledWith({ clientId: 'abc', clientSecret: 's3cret' });
  });

  it('D14: isDirty does not survive form.reset(refreshedInitial) — the secret is pristine (omitted) again', async () => {
    // Empirical answer to D14: TanStack Form v1 resets every field's meta on
    // `form.reset(values)` (FormApi.resetFieldMeta → defaultFieldMeta), so a
    // dirtied secret goes back to pristine. That matches today's behavior —
    // connector-form clears its `touchedSecrets` map in the same effect that
    // resets config when a fresh `initial` arrives — so no internal touched
    // map is needed. (A map surviving reset would be destructive: after
    // save → refetch → reset, a resubmit would send the reset empty value
    // and blank the stored ciphertext.)
    const onSubmit = vi.fn();
    const formRef: MarkerFormRef = { current: null };
    render(<MarkerHarness initial={markerInitial()} onSubmit={onSubmit} formRef={formRef} />);

    fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 's3cret' } });
    expect(formRef.current!.getFieldMeta('config.clientSecret' as never)?.isDirty).toBe(true);

    act(() => {
      formRef.current!.reset({ config: markerInitial() });
    });
    expect(formRef.current!.getFieldMeta('config.clientSecret' as never)?.isDirty).toBe(false);

    await submitForm();
    expect(onSubmit).toHaveBeenLastCalledWith({ clientId: 'abc' });

    // Typing again after the reset rotates the secret as usual.
    fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 'rotated' } });
    await submitForm();
    expect(onSubmit).toHaveBeenLastCalledWith({ clientId: 'abc', clientSecret: 'rotated' });
  });
});

// ============================================================
// Sentinel variant (smtp): sentinel stays in form state; the mutation strips
// it (lib/api/settings.ts) — unchanged by the kit.
// ============================================================

function SentinelHarness({ onSubmit }: { onSubmit: (value: { password: string }) => void }) {
  const form = useAppForm({
    defaultValues: { password: SENTINEL },
    onSubmit: ({ value }) => onSubmit(value),
  });
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
    >
      <form.AppField name="password">
        {(field) => <field.SecretField label="Password" stored={field.state.value === SENTINEL} />}
      </form.AppField>
      <button type="submit">Save</button>
    </form>
  );
}

describe('SecretField (sentinel variant)', () => {
  it('shows the rotate hint while the sentinel is pristine and keeps it in the submit value', async () => {
    const onSubmit = vi.fn();
    render(<SentinelHarness onSubmit={onSubmit} />);

    expect(screen.getByText(STORED_HINT)).toBeInTheDocument();
    await submitForm();
    // Sentinel preserved — strip-in-mutation behavior is the caller's, unchanged.
    expect(onSubmit).toHaveBeenCalledWith({ password: SENTINEL });
  });

  it('carries a typed replacement and drops the hint', async () => {
    const onSubmit = vi.fn();
    render(<SentinelHarness onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'new-pw' } });
    expect(screen.queryByText(STORED_HINT)).not.toBeInTheDocument();

    await submitForm();
    expect(onSubmit).toHaveBeenCalledWith({ password: 'new-pw' });
  });
});
