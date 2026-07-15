'use client';

/**
 * Schema-driven Dex connector form (TanStack Form).
 *
 * Renders inputs from the backend's connector-type registry rather than a
 * hand-written switch per type. Each spec field maps to one of:
 *   - text input (default)
 *   - secret input (when `secret`) — `<set>`-aware password field that only
 *     ships a new value when the user actually types into it (per-field
 *     `meta.isDirty` + `stripUntouchedSecrets`, replacing the hand-rolled
 *     `touchedSecrets` map)
 *   - textarea (when `multiline` in CONNECTOR_META)
 *   - comma-separated list (when `list` in CONNECTOR_META; stored as string[])
 *
 * Nested groups (e.g. ldap's `userSearch`) are rendered as collapsible
 * sub-sections (hidden, not unmounted, so their validators still run when
 * collapsed). The parent's required keys come from the registry's `nested`
 * array; deeper unknown keys are ignored — operators who need them can use
 * the YAML editor on the settings page.
 *
 * Secret round-trip: on edit, secret fields come back from the API as `""`
 * with a sibling `__<name>_set: true` flag (camelized by the shared axios
 * interceptor — see components/form/secrets.ts). We render the placeholder
 * `••••••••` and only include the field in the submitted body if the user
 * actually types into it. The Go handler's `mergeSecretFromExisting` then
 * preserves the previous ciphertext.
 */
import { useEffect, useId, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAppForm, useFieldContext } from '@/lib/form';
import { inputClassName } from '@/components/form/fields';
import { isStoredSecret, stripUntouchedSecrets } from '@/components/form/secrets';
import type { DexConnectorTypeSpec } from '@/types';
import { getConnectorMeta, humaniseFieldName, type FieldMeta } from './connector-meta';

export interface ConnectorFormState {
  name: string;
  displayName: string;
  config: Record<string, unknown>;
  enabled: boolean;
}

interface ConnectorFormProps {
  spec: DexConnectorTypeSpec;
  /** Initial values (used for edit mode). When omitted we start blank. */
  initial?: Partial<ConnectorFormState>;
  /** Server-side validation errors (e.g. "missing required fields: ..."). */
  serverError?: string | null;
  submitLabel: string;
  submitting?: boolean;
  /** Called with the cleaned form state. Secrets that the user did NOT touch
   *  are stripped so the backend's preserve-on-empty merge kicks in. */
  onSubmit: (state: ConnectorFormState) => void;
  onCancel?: () => void;
  /** Edit mode disables the `name` input (it's the immutable connector id). */
  isEdit?: boolean;
}

export function ConnectorForm({
  spec,
  initial,
  serverError,
  submitLabel,
  submitting,
  onSubmit,
  onCancel,
  isEdit,
}: ConnectorFormProps) {
  const meta = useMemo(() => getConnectorMeta(spec.type), [spec.type]);

  const isSecret = (key: string) => spec.secret.includes(key);

  /** True when the connector currently has a non-empty stored secret (read
   *  from the redacted-then-marked response shape — raw AND camelized marker,
   *  see components/form/secrets.ts). */
  const secretIsSet = (key: string): boolean =>
    isStoredSecret(initial?.config as Record<string, unknown> | undefined, key);

  const form = useAppForm({
    defaultValues: {
      name: initial?.name ?? '',
      displayName: initial?.displayName ?? '',
      enabled: initial?.enabled ?? true,
      config: (initial?.config ?? {}) as Record<string, unknown>,
    },
    onSubmit: ({ value }) => {
      // Build the submit body: drop untouched secrets (preserve-on-empty) and
      // strip the secret markers the API echoed back at us — both the raw
      // `__<name>_set` form and the camelized `_<Name>Set` form the axios
      // interceptor produces, so neither leaks back as a bogus config key.
      const cleaned = stripUntouchedSecrets(value.config, form, spec.secret, 'config.');
      onSubmit({
        name: value.name.trim(),
        displayName: value.displayName.trim() || value.name.trim(),
        enabled: value.enabled,
        config: cleaned,
      });
    },
  });

  // Reset config when switching connector types in the wizard (values AND
  // dirty meta, so secrets go back to pristine) and re-base on the refetched
  // config after a save. Name / display name / enabled are preserved.
  useEffect(() => {
    form.reset({
      ...form.state.values,
      config: (initial?.config ?? {}) as Record<string, unknown>,
    });
  }, [form, spec.type, initial?.config]);

  // Group fields: top-level required, then top-level optional, then nested
  // groups. Fields that are listed under `nested` parents are excluded from
  // the flat list because they're rendered inside their parent section.
  const nestedParents = new Set(spec.nested.map((n) => n.parent));
  const topLevelFields = [
    ...spec.required.filter((k) => !nestedParents.has(k)),
    ...spec.optional.filter((k) => !nestedParents.has(k)),
  ];
  // De-dupe in case a field appears in both required and optional.
  const seen = new Set<string>();
  const orderedTopLevel = topLevelFields.filter((k) => {
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });

  return (
    <div className="space-y-5">
      {/* Identity row */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <form.AppField
          name="name"
          validators={{
            onSubmit: ({ value }) => {
              if (!value.trim()) return 'Name is required';
              if (!isEdit && !/^[a-z0-9-]+$/.test(value.trim())) {
                return 'Name must be lowercase letters, digits, and dashes';
              }
              return undefined;
            },
          }}
        >
          {(field) => (
            <field.TextField
              label="Name"
              required
              placeholder="corp-azure"
              disabled={isEdit}
              helper={isEdit ? 'Connector id is immutable' : 'Lowercase, letters/digits/dashes'}
            />
          )}
        </form.AppField>
        <form.AppField name="displayName">
          {(field) => (
            <field.TextField
              label="Display name"
              placeholder="Sign in with Azure AD"
              helper="Shown on the Dex login screen"
            />
          )}
        </form.AppField>
      </div>

      {/* Top-level config fields */}
      {orderedTopLevel.length > 0 && (
        <div className="space-y-4">
          {orderedTopLevel.map((key) => {
            const fmeta: FieldMeta = meta.fields?.[key] ?? {};
            const label = fmeta.label ?? humaniseFieldName(key);
            const required = spec.required.includes(key);
            if (isSecret(key)) {
              return (
                <form.AppField
                  key={key}
                  name={`config.${key}`}
                  validators={
                    required
                      ? {
                          // Required secret: OK if either user typed one OR the
                          // server already has one stored.
                          onSubmit: ({ fieldApi }) =>
                            !fieldApi.state.meta.isDirty && !secretIsSet(key)
                              ? `${humaniseFieldName(key)} is required`
                              : undefined,
                        }
                      : undefined
                  }
                >
                  {(field) => (
                    <field.SecretField
                      label={label}
                      helper={fmeta.helper}
                      placeholder={fmeta.placeholder ?? ''}
                      required={required}
                      stored={secretIsSet(key)}
                    />
                  )}
                </form.AppField>
              );
            }
            const requiredValidator = required
              ? {
                  onSubmit: ({ value }: { value: unknown }) =>
                    value == null || (typeof value === 'string' && value.trim() === '')
                      ? `${humaniseFieldName(key)} is required`
                      : undefined,
                }
              : undefined;
            if (fmeta.multiline) {
              return (
                <form.AppField key={key} name={`config.${key}`} validators={requiredValidator}>
                  {(field) => (
                    <field.TextareaField
                      label={label}
                      helper={fmeta.helper}
                      placeholder={fmeta.placeholder ?? ''}
                      required={required}
                    />
                  )}
                </form.AppField>
              );
            }
            if (fmeta.list) {
              return (
                <form.AppField key={key} name={`config.${key}`} validators={requiredValidator}>
                  {() => (
                    <ListField
                      label={label}
                      helper={fmeta.helper}
                      placeholder={fmeta.placeholder ?? ''}
                      required={required}
                    />
                  )}
                </form.AppField>
              );
            }
            return (
              <form.AppField key={key} name={`config.${key}`} validators={requiredValidator}>
                {(field) => (
                  <field.TextField
                    label={label}
                    helper={fmeta.helper}
                    placeholder={fmeta.placeholder ?? ''}
                    required={required}
                  />
                )}
              </form.AppField>
            );
          })}
        </div>
      )}

      {/* Nested groups (ldap's userSearch / groupSearch) */}
      {spec.nested.map((nested) => (
        <NestedGroup key={nested.parent} parent={nested.parent} count={nested.keys.length}>
          {/* The optional keys for a nested group are anything in optional
            * that starts with `<parent>.`. Dex's ldap groupSearch is treated
            * as a fully-optional section by the backend so it has no entry
            * in spec.nested; the user can still add it via the YAML editor. */}
          {nested.keys.map((key) => {
            const lookupKey = `${nested.parent}.${key}`;
            const fmeta: FieldMeta = meta.fields?.[lookupKey] ?? meta.fields?.[key] ?? {};
            return (
              <form.AppField
                key={key}
                name={`config.${nested.parent}.${key}`}
                validators={{
                  onSubmit: ({ value }) =>
                    value == null || (typeof value === 'string' && value.trim() === '')
                      ? `${humaniseFieldName(nested.parent)} · ${humaniseFieldName(key)} is required`
                      : undefined,
                }}
              >
                {(field) => (
                  <field.TextField
                    label={fmeta.label ?? humaniseFieldName(key)}
                    helper={fmeta.helper}
                    placeholder={fmeta.placeholder ?? ''}
                    required
                  />
                )}
              </form.AppField>
            );
          })}
        </NestedGroup>
      ))}

      {/* Enabled toggle */}
      <form.AppField name="enabled">{() => <EnabledField />}</form.AppField>

      {/* Server-side validation summary (client checks render inline per field) */}
      {serverError && (
        <div className="rounded-lg border border-status-error/40 bg-status-error/5 p-3">
          <p className="text-sm font-medium text-status-error">Please fix the following:</p>
          <ul className="mt-1 list-disc list-inside text-xs text-status-error/90 space-y-0.5">
            <li>{serverError}</li>
          </ul>
        </div>
      )}

      <div className="flex items-center justify-end gap-2 pt-2">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
        )}
        <button
          type="button"
          onClick={() => void form.handleSubmit()}
          disabled={submitting}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {submitLabel}
        </button>
      </div>
    </div>
  );
}

// ============================================================
// Field rendering helpers
// ============================================================

/** Comma-separated list input (stored as string[] in the config). Reads its
 *  field API from the kit context, like the fields in components/form. */
function ListField({
  label,
  helper,
  required,
  placeholder,
}: {
  label: string;
  helper?: string;
  required?: boolean;
  placeholder?: string;
}) {
  const field = useFieldContext<unknown>();
  const id = useId();
  const error = field.state.meta.errors.find(
    (e): e is string => typeof e === 'string' && e !== '',
  );
  const value = field.state.value;
  const arr: string[] = Array.isArray(value)
    ? (value as unknown[]).map((v) => String(v))
    : typeof value === 'string'
      ? value.split(',').map((s) => s.trim()).filter(Boolean)
      : [];
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error ml-0.5">*</span>}
      </label>
      <input
        id={id}
        type="text"
        value={arr.join(', ')}
        placeholder={placeholder}
        onChange={(e) => {
          const next = e.target.value
            .split(',')
            .map((s) => s.trim())
            .filter(Boolean);
          field.handleChange(next);
        }}
        onBlur={field.handleBlur}
        className={inputClassName}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
      />
      {error ? (
        <p id={`${id}-error`} className="text-2xs text-status-error">
          {error}
        </p>
      ) : (
        helper && <p className="text-2xs text-muted-foreground">{helper}</p>
      )}
    </div>
  );
}

/** Enabled toggle in today's exact markup (label above, toggle + status text). */
function EnabledField() {
  const field = useFieldContext<boolean>();
  const enabled = Boolean(field.state.value);
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">Enabled</label>
      <label className="inline-flex items-center gap-2 cursor-pointer">
        <button
          type="button"
          role="switch"
          aria-checked={enabled}
          onClick={() => field.handleChange(!enabled)}
          className={cn(
            'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
            enabled ? 'bg-status-success' : 'bg-muted'
          )}
        >
          <span
            className={cn(
              'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
              enabled ? 'translate-x-6' : 'translate-x-1'
            )}
          />
        </button>
        <span className="text-sm text-muted-foreground">{enabled ? 'Enabled' : 'Disabled'}</span>
      </label>
      <p className="text-2xs text-muted-foreground">
        Disabled connectors are excluded from the rendered Dex config
      </p>
    </div>
  );
}

function NestedGroup({
  parent,
  count,
  children,
}: {
  parent: string;
  count: number;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(true);
  return (
    <div className="rounded-lg border border-border">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center justify-between px-4 py-3 text-sm font-medium text-foreground hover:bg-accent/30 transition-colors rounded-t-lg"
      >
        <span className="flex items-center gap-2">
          {open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          {humaniseFieldName(parent)}
        </span>
        <span className="text-2xs text-muted-foreground">{count} required</span>
      </button>
      {/* Hidden (not unmounted) when collapsed so field validators keep running. */}
      <div className={cn('p-4 space-y-4 border-t border-border', !open && 'hidden')}>
        {children}
      </div>
    </div>
  );
}
