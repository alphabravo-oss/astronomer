'use client';

/**
 * Schema-driven Dex connector form.
 *
 * Renders inputs from the backend's connector-type registry rather than a
 * hand-written switch per type. Each spec field maps to one of:
 *   - text input (default)
 *   - password input (when `secret`)
 *   - textarea (when `multiline` in CONNECTOR_META)
 *   - comma-separated list (when `list` in CONNECTOR_META; stored as string[])
 *
 * Nested groups (e.g. ldap's `userSearch`) are rendered as collapsible
 * sub-sections. The parent's required keys come from the registry's
 * `nested` array; deeper unknown keys are ignored — operators who need them
 * can use the YAML editor on the settings page.
 *
 * Secret round-trip: on edit, secret fields come back from the API as `""`
 * with a sibling `__<name>_set: true` flag. We render the placeholder
 * `••••••••` and only include the field in the submitted body if the user
 * actually types into it. The Go handler's `mergeSecretFromExisting` then
 * preserves the previous ciphertext.
 */
import { useMemo, useState, useEffect } from 'react';
import { ChevronDown, ChevronRight, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { DexConnectorTypeSpec } from '@/types';
import { getConnectorMeta, humaniseFieldName, type FieldMeta } from './connector-meta';

const SECRET_PLACEHOLDER = '••••••••';

// Mirror of the snake_case→camelCase transform the shared axios response
// interceptor (lib/api.ts) applies to every response key. The backend echoes a
// per-secret marker `__<key>_set: true`; the interceptor rewrites it (e.g.
// `__clientSecret_set` → `_ClientSecretSet`). We recompute that exact key so we
// can both (a) detect a stored secret and (b) strip the marker from the submit
// body instead of persisting it as a garbage config field.
function snakeToCamelKey(s: string): string {
  return s.replace(/_([a-z0-9])/g, (_, ch: string) => ch.toUpperCase());
}
function camelSecretMarker(key: string): string {
  return snakeToCamelKey(`__${key}_set`);
}

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

  const [name, setName] = useState(initial?.name ?? '');
  const [displayName, setDisplayName] = useState(initial?.displayName ?? '');
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [config, setConfig] = useState<Record<string, unknown>>(initial?.config ?? {});
  // Tracks which secret fields the user has touched in this session. Untouched
  // secrets are dropped from the submit body so the backend keeps the existing
  // ciphertext.
  const [touchedSecrets, setTouchedSecrets] = useState<Record<string, boolean>>({});
  const [clientErrors, setClientErrors] = useState<string[]>([]);

  // Reset state when switching connector types in the wizard.
  useEffect(() => {
    setConfig(initial?.config ?? {});
    setTouchedSecrets({});
  }, [spec.type, initial?.config]);

  const setField = (key: string, value: unknown) => {
    setConfig((prev) => ({ ...prev, [key]: value }));
  };

  const setNestedField = (parent: string, key: string, value: unknown) => {
    setConfig((prev) => {
      const existing = (prev[parent] as Record<string, unknown> | undefined) ?? {};
      return { ...prev, [parent]: { ...existing, [key]: value } };
    });
  };

  const isSecret = (key: string) => spec.secret.includes(key);

  /** True when the connector currently has a non-empty stored secret (read
   *  from the redacted-then-marked response shape).
   *
   *  The backend emits a sibling marker `__<key>_set: true`. The shared axios
   *  response interceptor (lib/api.ts) camelizes every response key, which
   *  rewrites `__clientSecret_set` into `_ClientSecretSet`. We therefore have
   *  to look up the marker under BOTH the raw and the camelized key, otherwise
   *  the `••••••••` placeholder never renders and edits force secret re-entry. */
  const secretIsSet = (key: string): boolean => {
    const cfg = initial?.config as Record<string, unknown> | undefined;
    if (!cfg) return false;
    return Boolean(cfg[`__${key}_set`] || cfg[camelSecretMarker(key)]);
  };

  const handleSubmit = () => {
    const errors: string[] = [];
    if (!name.trim()) errors.push('Name is required');
    if (!isEdit && !/^[a-z0-9-]+$/.test(name.trim())) {
      errors.push('Name must be lowercase letters, digits, and dashes');
    }
    for (const key of spec.required) {
      const v = config[key];
      if (isSecret(key)) {
        // Required secret: OK if either user typed one OR the server already
        // has one stored.
        if (!touchedSecrets[key] && !secretIsSet(key)) {
          errors.push(`${humaniseFieldName(key)} is required`);
        }
        continue;
      }
      if (v == null || (typeof v === 'string' && v.trim() === '')) {
        errors.push(`${humaniseFieldName(key)} is required`);
      }
    }
    for (const nested of spec.nested) {
      const parentVal = (config[nested.parent] as Record<string, unknown> | undefined) ?? {};
      for (const key of nested.keys) {
        const v = parentVal[key];
        if (v == null || (typeof v === 'string' && v.trim() === '')) {
          errors.push(`${humaniseFieldName(nested.parent)} · ${humaniseFieldName(key)} is required`);
        }
      }
    }
    if (errors.length > 0) {
      setClientErrors(errors);
      return;
    }
    setClientErrors([]);

    // Build the submit body: drop untouched secrets (preserve-on-empty), strip
    // the secret markers the API echoed back at us — both the raw
    // `__<name>_set` form and the camelized `_<Name>Set` form the axios
    // interceptor produces, so neither leaks back as a bogus config key.
    const markerKeys = new Set<string>();
    for (const s of spec.secret) {
      markerKeys.add(`__${s}_set`);
      markerKeys.add(camelSecretMarker(s));
    }
    const cleaned: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(config)) {
      if (k.startsWith('__') && k.endsWith('_set')) continue;
      if (markerKeys.has(k)) continue;
      if (isSecret(k) && !touchedSecrets[k]) continue;
      cleaned[k] = v;
    }
    onSubmit({
      name: name.trim(),
      displayName: displayName.trim() || name.trim(),
      enabled,
      config: cleaned,
    });
  };

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
        <FieldRow label="Name" required helper={isEdit ? 'Connector id is immutable' : 'Lowercase, letters/digits/dashes'}>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="corp-azure"
            disabled={isEdit}
            className={cn(inputCls, isEdit && 'opacity-60 cursor-not-allowed')}
          />
        </FieldRow>
        <FieldRow label="Display name" helper="Shown on the Dex login screen">
          <input
            type="text"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="Sign in with Azure AD"
            className={inputCls}
          />
        </FieldRow>
      </div>

      {/* Top-level config fields */}
      {orderedTopLevel.length > 0 && (
        <div className="space-y-4">
          {orderedTopLevel.map((key) => {
            const fmeta = meta.fields?.[key] ?? {};
            return (
              <ConnectorField
                key={key}
                fieldKey={key}
                meta={fmeta}
                required={spec.required.includes(key)}
                isSecret={isSecret(key)}
                secretIsSet={isSecret(key) && secretIsSet(key)}
                value={config[key]}
                onChange={(v) => setField(key, v)}
                onSecretFocus={() => setTouchedSecrets((p) => ({ ...p, [key]: true }))}
                touched={touchedSecrets[key]}
              />
            );
          })}
        </div>
      )}

      {/* Nested groups (ldap's userSearch / groupSearch) */}
      {spec.nested.map((nested) => (
        <NestedGroup
          key={nested.parent}
          parent={nested.parent}
          requiredKeys={nested.keys}
          /* The optional keys for a nested group are anything in optional
           * that starts with `<parent>.`. Dex's ldap groupSearch is treated
           * as a fully-optional section by the backend so it has no entry
           * in spec.nested; the user can still add it via the YAML editor. */
          value={(config[nested.parent] as Record<string, unknown> | undefined) ?? {}}
          onChange={(k, v) => setNestedField(nested.parent, k, v)}
          meta={meta.fields}
        />
      ))}

      {/* Enabled toggle */}
      <FieldRow label="Enabled" helper="Disabled connectors are excluded from the rendered Dex config">
        <label className="inline-flex items-center gap-2 cursor-pointer">
          <button
            type="button"
            onClick={() => setEnabled((v) => !v)}
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
      </FieldRow>

      {/* Validation summary */}
      {(clientErrors.length > 0 || serverError) && (
        <div className="rounded-lg border border-status-error/40 bg-status-error/5 p-3">
          <p className="text-sm font-medium text-status-error">Please fix the following:</p>
          <ul className="mt-1 list-disc list-inside text-xs text-status-error/90 space-y-0.5">
            {clientErrors.map((e) => (
              <li key={e}>{e}</li>
            ))}
            {serverError && <li>{serverError}</li>}
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
          onClick={handleSubmit}
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

const inputCls =
  'w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring';

const textareaCls =
  'w-full min-h-[120px] px-3 py-2 rounded-lg border border-border bg-background text-xs font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring';

function FieldRow({
  label,
  helper,
  required,
  children,
}: {
  label: string;
  helper?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error ml-0.5">*</span>}
      </label>
      {children}
      {helper && <p className="text-2xs text-muted-foreground">{helper}</p>}
    </div>
  );
}

function ConnectorField({
  fieldKey,
  meta,
  required,
  isSecret,
  secretIsSet,
  value,
  onChange,
  onSecretFocus,
  touched,
}: {
  fieldKey: string;
  meta: FieldMeta;
  required: boolean;
  isSecret: boolean;
  secretIsSet: boolean;
  value: unknown;
  onChange: (v: unknown) => void;
  onSecretFocus: () => void;
  touched?: boolean;
}) {
  const label = meta.label ?? humaniseFieldName(fieldKey);
  const helper = meta.helper;
  const placeholder = meta.placeholder ?? '';

  if (isSecret) {
    // Secret: empty placeholder when set on server and untouched.
    const display = touched ? (typeof value === 'string' ? value : '') : '';
    return (
      <FieldRow label={label} helper={secretIsSet && !touched ? 'Stored — leave blank to keep current value' : helper} required={required}>
        <input
          type="password"
          value={display}
          placeholder={secretIsSet && !touched ? SECRET_PLACEHOLDER : placeholder}
          onFocus={onSecretFocus}
          onChange={(e) => onChange(e.target.value)}
          className={inputCls}
          autoComplete="new-password"
        />
      </FieldRow>
    );
  }

  if (meta.multiline) {
    const text = typeof value === 'string' ? value : '';
    return (
      <FieldRow label={label} helper={helper} required={required}>
        <textarea
          value={text}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value)}
          className={textareaCls}
        />
      </FieldRow>
    );
  }

  if (meta.list) {
    const arr: string[] = Array.isArray(value)
      ? (value as unknown[]).map((v) => String(v))
      : typeof value === 'string'
        ? value.split(',').map((s) => s.trim()).filter(Boolean)
        : [];
    return (
      <FieldRow label={label} helper={helper} required={required}>
        <input
          type="text"
          value={arr.join(', ')}
          placeholder={placeholder}
          onChange={(e) => {
            const next = e.target.value
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean);
            onChange(next);
          }}
          className={inputCls}
        />
      </FieldRow>
    );
  }

  const text = typeof value === 'string' ? value : value == null ? '' : String(value);
  return (
    <FieldRow label={label} helper={helper} required={required}>
      <input
        type="text"
        value={text}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className={inputCls}
      />
    </FieldRow>
  );
}

function NestedGroup({
  parent,
  requiredKeys,
  value,
  onChange,
  meta,
}: {
  parent: string;
  requiredKeys: string[];
  value: Record<string, unknown>;
  onChange: (key: string, v: unknown) => void;
  meta?: Record<string, FieldMeta>;
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
        <span className="text-2xs text-muted-foreground">{requiredKeys.length} required</span>
      </button>
      {open && (
        <div className="p-4 space-y-4 border-t border-border">
          {requiredKeys.map((key) => {
            const lookupKey = `${parent}.${key}`;
            const fmeta = meta?.[lookupKey] ?? meta?.[key] ?? {};
            const currentValue = typeof value[key] === 'string' ? (value[key] as string) : '';
            return (
              <FieldRow
                key={key}
                label={fmeta.label ?? humaniseFieldName(key)}
                helper={fmeta.helper}
                required
              >
                <input
                  type="text"
                  value={currentValue}
                  placeholder={fmeta.placeholder ?? ''}
                  onChange={(e) => onChange(key, e.target.value)}
                  className={inputCls}
                />
              </FieldRow>
            );
          })}
        </div>
      )}
    </div>
  );
}
