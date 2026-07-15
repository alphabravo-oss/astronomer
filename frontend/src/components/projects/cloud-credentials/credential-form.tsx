'use client';

/**
 * Schema-driven cloud-credential form (TanStack Form). Mirrors the Dex
 * connector-form pattern: fields render from the provider's spec in
 * `/cloud-credentials/providers/`, with secret fields rendered as
 * `<set>`-aware password inputs that only ship a new value when the user
 * actually types into them (per-field `meta.isDirty` +
 * `stripUntouchedSecrets`, replacing the hand-rolled `touchedSecrets` map).
 *
 * The form is intentionally generic over the provider — adding a new
 * provider only requires a backend registry entry.
 */
import { useEffect } from 'react';
import { Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAppForm } from '@/lib/form';
import { stripUntouchedSecrets } from '@/components/form/secrets';
import type {
  CloudCredentialProviderSpec,
  CloudCredentialTargetRef,
  CloudCredentialWriteRequest,
  CloudProvider,
} from '@/lib/api/project-detail';
import { TargetRefsEditor } from './target-refs-editor';

// This form's inputs are one notch tighter than the kit default — merged
// over the kit's base input class (twMerge, later wins).
const credInputClassName = 'h-9 rounded-md focus:ring-1';

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
  const secretIsSet = (key: string): boolean => Boolean(initial?.secretsSet?.has(key));

  const form = useAppForm({
    defaultValues: {
      name: initial?.name ?? '',
      description: initial?.description ?? '',
      config: (initial?.config ?? {}) as Record<string, unknown>,
      targetRefs: initial?.targetRefs ?? [],
    },
    onSubmit: ({ value }) => {
      // Drop untouched secrets so the backend keeps the existing ciphertext,
      // then keep only the provider's declared, non-null fields.
      const stripped = stripUntouchedSecrets(
        value.config,
        form,
        spec.fields.filter((f) => f.secret).map((f) => f.name),
        'config.',
      );
      const cleaned: Record<string, unknown> = {};
      for (const field of spec.fields) {
        if (field.name in stripped && stripped[field.name] != null) {
          cleaned[field.name] = stripped[field.name];
        }
      }
      onSubmit({
        name: value.name.trim(),
        provider,
        description: value.description.trim() || undefined,
        config: cleaned,
        targetRefs: value.targetRefs,
      });
    },
  });

  // Switching providers in the wizard wipes the per-provider config (values
  // AND dirty meta, so secrets go back to pristine) but preserves
  // name / description / targets (those are provider-agnostic).
  useEffect(() => {
    if (!isEdit) {
      form.reset({
        ...form.state.values,
        config: (initial?.config ?? {}) as Record<string, unknown>,
      });
    }
  }, [form, provider, isEdit, initial?.config]);

  return (
    <div className="space-y-5">
      {/* Identity */}
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
              disabled={isEdit}
              placeholder="my-aws-keys"
              transform={(v) => v.toLowerCase().replace(/[^a-z0-9-]/g, '-')}
              className={credInputClassName}
            />
          )}
        </form.AppField>
        <form.AppField name="description">
          {(field) => (
            <field.TextField
              label="Description"
              placeholder="What this credential is used for"
              className={credInputClassName}
            />
          )}
        </form.AppField>
      </div>

      {/* Provider fields */}
      <div className="space-y-3">
        <h3 className="text-sm font-medium text-foreground">{spec.displayName} fields</h3>
        {spec.fields.length === 0 ? (
          <p className="text-xs text-muted-foreground">This provider has no fields.</p>
        ) : (
          spec.fields.map((fieldSpec) =>
            fieldSpec.secret ? (
              <form.AppField
                key={fieldSpec.name}
                name={`config.${fieldSpec.name}`}
                validators={
                  fieldSpec.required
                    ? {
                        onSubmit: ({ fieldApi }) =>
                          !fieldApi.state.meta.isDirty && !secretIsSet(fieldSpec.name)
                            ? `${fieldSpec.label || fieldSpec.name} is required`
                            : undefined,
                      }
                    : undefined
                }
              >
                {(field) => (
                  <field.SecretField
                    label={fieldSpec.label || fieldSpec.name}
                    required={fieldSpec.required}
                    helper={fieldSpec.helper}
                    placeholder={fieldSpec.placeholder}
                    stored={secretIsSet(fieldSpec.name)}
                    revealable
                    className={cn(credInputClassName, 'font-mono')}
                  />
                )}
              </form.AppField>
            ) : (
              <form.AppField
                key={fieldSpec.name}
                name={`config.${fieldSpec.name}`}
                validators={
                  fieldSpec.required
                    ? {
                        onSubmit: ({ value }) =>
                          value == null || (typeof value === 'string' && value.trim() === '')
                            ? `${fieldSpec.label || fieldSpec.name} is required`
                            : undefined,
                      }
                    : undefined
                }
              >
                {(field) => (
                  <field.TextField
                    label={fieldSpec.label || fieldSpec.name}
                    required={fieldSpec.required}
                    helper={fieldSpec.helper}
                    placeholder={fieldSpec.placeholder}
                    className={credInputClassName}
                  />
                )}
              </form.AppField>
            ),
          )
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
        <form.AppField name="targetRefs">
          {(field) => <TargetRefsEditor value={field.state.value} onChange={field.handleChange} />}
        </form.AppField>
      </div>

      {serverError && (
        <div className="rounded-lg border border-status-error/40 bg-status-error/10 p-3 space-y-1">
          <p className="text-xs text-status-error">{serverError}</p>
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
          onClick={() => void form.handleSubmit()}
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
