'use client';

/**
 * Schema-driven cloud-credential form. Mirrors the Dex connector-form
 * pattern: fields render from the provider's spec in
 * `/cloud-credentials/providers/`, with secret fields rendered as
 * `<set>`-aware password inputs that only ship a new value when the user
 * actually types into them.
 *
 * The form is intentionally generic over the provider — adding a new
 * provider only requires a backend registry entry.
 */
import { useEffect, useMemo, useState } from 'react';
import { Eye, EyeOff, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import type {
  CloudCredentialProviderSpec,
  CloudCredentialTargetRef,
  CloudCredentialWriteRequest,
  CloudProvider,
} from '@/lib/api/project-detail';
import { TargetRefsEditor } from './target-refs-editor';

const SECRET_PLACEHOLDER = '••••••••';

export interface CredentialFormState {
  name: string;
  description: string;
  config: Record<string, unknown>;
  targetRefs: CloudCredentialTargetRef[];
}

interface CredentialFormProps {
  provider: CloudProvider;
  spec: CloudCredentialProviderSpec;
  /** Pre-fill for edit mode. Untouched secret fields stay redacted. */
  initial?: Partial<CredentialFormState> & {
    /** Set of field names that already have a stored secret. */
    secretsSet?: Set<string>;
  };
  isEdit?: boolean;
  submitting?: boolean;
  serverError?: string | null;
  onSubmit: (body: CloudCredentialWriteRequest) => void;
  onCancel?: () => void;
}

export function CredentialForm({
  provider,
  spec,
  initial,
  isEdit,
  submitting,
  serverError,
  onSubmit,
  onCancel,
}: CredentialFormProps) {
  const [name, setName] = useState(initial?.name ?? '');
  const [description, setDescription] = useState(initial?.description ?? '');
  const [config, setConfig] = useState<Record<string, unknown>>(initial?.config ?? {});
  const [targetRefs, setTargetRefs] = useState<CloudCredentialTargetRef[]>(
    initial?.targetRefs ?? [],
  );
  // Whether the user has actually typed into a secret field this session.
  // Untouched secrets get stripped on submit so the backend preserves the
  // existing ciphertext.
  const [touchedSecrets, setTouchedSecrets] = useState<Record<string, boolean>>({});
  // Per-field "show password" toggle, keyed by field name.
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const [clientErrors, setClientErrors] = useState<string[]>([]);

  // Switching providers in the wizard wipes the per-provider config but
  // preserves name / description / targets (those are provider-agnostic).
  useEffect(() => {
    if (!isEdit) {
      setConfig(initial?.config ?? {});
      setTouchedSecrets({});
    }
  }, [provider, isEdit, initial?.config]);

  const setField = (key: string, value: unknown) =>
    setConfig((prev) => ({ ...prev, [key]: value }));

  const secretIsSet = (key: string): boolean => Boolean(initial?.secretsSet?.has(key));

  const handleSubmit = () => {
    const errors: string[] = [];
    if (!name.trim()) errors.push('Name is required');
    if (!isEdit && !/^[a-z0-9-]+$/.test(name.trim())) {
      errors.push('Name must be lowercase letters, digits, and dashes');
    }
    for (const field of spec.fields) {
      if (!field.required) continue;
      if (field.secret) {
        if (!touchedSecrets[field.name] && !secretIsSet(field.name)) {
          errors.push(`${field.label || field.name} is required`);
        }
        continue;
      }
      const v = config[field.name];
      if (v == null || (typeof v === 'string' && v.trim() === '')) {
        errors.push(`${field.label || field.name} is required`);
      }
    }
    if (errors.length) {
      setClientErrors(errors);
      return;
    }
    setClientErrors([]);

    // Drop untouched secrets so the backend keeps the existing ciphertext.
    const cleaned: Record<string, unknown> = {};
    for (const field of spec.fields) {
      if (field.secret && !touchedSecrets[field.name]) continue;
      if (config[field.name] != null) cleaned[field.name] = config[field.name];
    }

    onSubmit({
      name: name.trim(),
      provider,
      description: description.trim() || undefined,
      config: cleaned,
      targetRefs,
    });
  };

  const allErrors = useMemo(() => {
    const out = [...clientErrors];
    if (serverError) out.unshift(serverError);
    return out;
  }, [clientErrors, serverError]);

  return (
    <div className="space-y-5">
      {/* Identity */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Name</label>
          <input
            type="text"
            value={name}
            disabled={isEdit}
            placeholder="my-aws-keys"
            onChange={(e) =>
              setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))
            }
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-60"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Description</label>
          <input
            type="text"
            value={description}
            placeholder="What this credential is used for"
            onChange={(e) => setDescription(e.target.value)}
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
      </div>

      {/* Provider fields */}
      <div className="space-y-3">
        <h3 className="text-sm font-medium text-foreground">{spec.displayName} fields</h3>
        {spec.fields.length === 0 ? (
          <p className="text-xs text-muted-foreground">This provider has no fields.</p>
        ) : (
          spec.fields.map((field) => {
            const value = (config[field.name] as string | undefined) ?? '';
            const touched = touchedSecrets[field.name];
            const reveal = revealed[field.name];

            if (field.secret) {
              return (
                <div key={field.name} className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">
                    {field.label || field.name}
                    {field.required && <span className="text-status-error"> *</span>}
                  </label>
                  <div className="relative">
                    <input
                      type={reveal ? 'text' : 'password'}
                      value={value}
                      placeholder={
                        secretIsSet(field.name) && !touched
                          ? `<set> · ${SECRET_PLACEHOLDER}`
                          : field.placeholder
                      }
                      onChange={(e) => {
                        setField(field.name, e.target.value);
                        setTouchedSecrets((prev) => ({ ...prev, [field.name]: true }));
                      }}
                      className="w-full h-9 px-3 pr-9 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring font-mono"
                    />
                    <button
                      type="button"
                      onClick={() =>
                        setRevealed((prev) => ({ ...prev, [field.name]: !prev[field.name] }))
                      }
                      className="absolute right-2 top-1/2 -translate-y-1/2 p-1 text-muted-foreground hover:text-foreground"
                      title={reveal ? 'Hide' : 'Show'}
                    >
                      {reveal ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                    </button>
                  </div>
                  {field.helper && (
                    <p className="text-xs text-muted-foreground">{field.helper}</p>
                  )}
                </div>
              );
            }

            return (
              <div key={field.name} className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">
                  {field.label || field.name}
                  {field.required && <span className="text-status-error"> *</span>}
                </label>
                <input
                  type="text"
                  value={value}
                  placeholder={field.placeholder}
                  onChange={(e) => setField(field.name, e.target.value)}
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
                />
                {field.helper && <p className="text-xs text-muted-foreground">{field.helper}</p>}
              </div>
            );
          })
        )}
      </div>

      {/* Target refs */}
      <div className="space-y-3">
        <div>
          <h3 className="text-sm font-medium text-foreground">Where to materialize</h3>
          <p className="text-xs text-muted-foreground mt-0.5">
            Pick the clusters and namespaces where this credential should be created as a Secret.
          </p>
        </div>
        <TargetRefsEditor value={targetRefs} onChange={setTargetRefs} />
      </div>

      {allErrors.length > 0 && (
        <div className="rounded-lg border border-status-error/40 bg-status-error/10 p-3 space-y-1">
          {allErrors.map((err, i) => (
            <p key={i} className="text-xs text-status-error">
              {err}
            </p>
          ))}
        </div>
      )}

      <div className="flex justify-end gap-2">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
        )}
        <button
          type="button"
          onClick={handleSubmit}
          disabled={submitting}
          className={cn(
            'inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity',
            submitting && 'opacity-50 cursor-wait',
          )}
        >
          {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {isEdit ? 'Save credential' : 'Create credential'}
        </button>
      </div>
    </div>
  );
}
