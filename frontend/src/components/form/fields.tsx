/**
 * Dumb field components for the form kit (P5.1) — label + control + error
 * line, styled with today's exact class strings. Each component reads its
 * field API from the kit context (`form.AppField` provides it), so callers
 * write `<field.TextField label="Host" />` and nothing else.
 *
 * A11y (L6 strict improvement over today's label-without-htmlFor markup):
 * generated `id` + `htmlFor`, `aria-invalid` when errored, and the error
 * `<p>` wired via `aria-describedby`.
 */
import { useId, useState } from 'react';
import { Eye, EyeOff, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useFieldContext, useFormContext } from '@/lib/form';

/** Shared input class — today's exact string (connector-form / smtp et al). */
export const inputClassName =
  'w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring';

const textareaClassName =
  'w-full min-h-[120px] px-3 py-2 rounded-lg border border-border bg-background text-xs font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring';

const SECRET_PLACEHOLDER = '••••••••';

interface CommonFieldProps {
  label: string;
  helper?: string;
  required?: boolean;
  disabled?: boolean;
  placeholder?: string;
  /** Extra input classes merged over the shared base (twMerge — later wins),
   *  for forms whose inputs deviate from the default sizing (e.g. h-9/rounded-md). */
  className?: string;
}

function firstError(meta: { errors: unknown[] }): string | undefined {
  for (const e of meta.errors) {
    if (typeof e === 'string' && e) return e;
    if (e && typeof e === 'object') {
      const msg = (e as { message?: unknown }).message;
      if (typeof msg === 'string' && msg) return msg;
    }
  }
  return undefined;
}

/** Label + control + helper/error shell (today's FieldRow markup). */
function FieldShell({
  id,
  label,
  helper,
  required,
  error,
  children,
}: {
  id: string;
  label: string;
  helper?: string;
  required?: boolean;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error ml-0.5">*</span>}
      </label>
      {children}
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

function ariaProps(id: string, error: string | undefined) {
  return {
    'aria-invalid': error ? true : undefined,
    'aria-describedby': error ? `${id}-error` : undefined,
  };
}

export function TextField({
  label,
  helper,
  required,
  disabled,
  placeholder,
  className,
  type = 'text',
  autoComplete,
  transform,
}: CommonFieldProps & {
  type?: 'text' | 'email' | 'url';
  autoComplete?: string;
  /** Normalize keystrokes before they hit form state (e.g. slug-casing a name). */
  transform?: (value: string) => string;
}) {
  const field = useFieldContext<string>();
  const id = useId();
  const error = firstError(field.state.meta);
  return (
    <FieldShell id={id} label={label} helper={helper} required={required} error={error}>
      <input
        id={id}
        type={type}
        value={field.state.value ?? ''}
        onChange={(e) => field.handleChange(transform ? transform(e.target.value) : e.target.value)}
        onBlur={field.handleBlur}
        placeholder={placeholder}
        disabled={disabled}
        autoComplete={autoComplete}
        className={cn(inputClassName, disabled && 'opacity-60 cursor-not-allowed', className)}
        {...ariaProps(id, error)}
      />
    </FieldShell>
  );
}

export function NumberField({
  label,
  helper,
  required,
  disabled,
  placeholder,
  className,
  min,
  max,
  step,
}: CommonFieldProps & { min?: number; max?: number; step?: number }) {
  const field = useFieldContext<number>();
  const id = useId();
  const error = firstError(field.state.meta);
  return (
    <FieldShell id={id} label={label} helper={helper} required={required} error={error}>
      <input
        id={id}
        type="number"
        value={field.state.value ?? ''}
        onChange={(e) => field.handleChange(Number(e.target.value))}
        onBlur={field.handleBlur}
        placeholder={placeholder}
        disabled={disabled}
        min={min}
        max={max}
        step={step}
        className={cn(inputClassName, disabled && 'opacity-60 cursor-not-allowed', className)}
        {...ariaProps(id, error)}
      />
    </FieldShell>
  );
}

export function PasswordField({
  label,
  helper,
  required,
  disabled,
  placeholder,
  className,
  autoComplete,
}: CommonFieldProps & { autoComplete?: string }) {
  const field = useFieldContext<string>();
  const id = useId();
  const error = firstError(field.state.meta);
  return (
    <FieldShell id={id} label={label} helper={helper} required={required} error={error}>
      <input
        id={id}
        type="password"
        value={field.state.value ?? ''}
        onChange={(e) => field.handleChange(e.target.value)}
        onBlur={field.handleBlur}
        placeholder={placeholder}
        disabled={disabled}
        autoComplete={autoComplete}
        className={cn(inputClassName, disabled && 'opacity-60 cursor-not-allowed', className)}
        {...ariaProps(id, error)}
      />
    </FieldShell>
  );
}

/**
 * Password input for round-tripped secrets. When the server already holds a
 * value (`stored`) and the user hasn't typed this session (pristine, i.e.
 * `meta.isDirty` false — see secrets.ts for the D14 note), it renders the
 * `••••••••` placeholder and a "type to rotate" hint; `stripUntouchedSecrets`
 * then omits the field so the backend keeps the existing ciphertext.
 */
export function SecretField({
  label,
  helper,
  required,
  disabled,
  placeholder,
  className,
  stored,
  revealable,
  autoComplete = 'new-password',
}: CommonFieldProps & {
  stored: boolean;
  /** Adds the eye toggle that flips the input to plain text (credential-form pattern). */
  revealable?: boolean;
  autoComplete?: string;
}) {
  const field = useFieldContext<string>();
  const id = useId();
  const [reveal, setReveal] = useState(false);
  const error = firstError(field.state.meta);
  const pristine = !field.state.meta.isDirty;
  const showStored = stored && pristine;
  const input = (
    <input
      id={id}
      type={revealable && reveal ? 'text' : 'password'}
      value={field.state.value ?? ''}
      placeholder={showStored ? SECRET_PLACEHOLDER : placeholder}
      onChange={(e) => field.handleChange(e.target.value)}
      onBlur={field.handleBlur}
      disabled={disabled}
      autoComplete={autoComplete}
      className={cn(
        inputClassName,
        disabled && 'opacity-60 cursor-not-allowed',
        revealable && 'pr-9',
        className,
      )}
      {...ariaProps(id, error)}
    />
  );
  return (
    <FieldShell
      id={id}
      label={label}
      helper={showStored ? 'Stored — type a new value to rotate' : helper}
      required={required}
      error={error}
    >
      {revealable ? (
        <div className="relative">
          {input}
          <button
            type="button"
            onClick={() => setReveal((prev) => !prev)}
            className="absolute right-2 top-1/2 -translate-y-1/2 p-1 text-muted-foreground hover:text-foreground"
            title={reveal ? 'Hide' : 'Show'}
          >
            {reveal ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
          </button>
        </div>
      ) : (
        input
      )}
    </FieldShell>
  );
}

export function TextareaField({
  label,
  helper,
  required,
  disabled,
  placeholder,
  className,
}: CommonFieldProps) {
  const field = useFieldContext<string>();
  const id = useId();
  const error = firstError(field.state.meta);
  return (
    <FieldShell id={id} label={label} helper={helper} required={required} error={error}>
      <textarea
        id={id}
        value={field.state.value ?? ''}
        onChange={(e) => field.handleChange(e.target.value)}
        onBlur={field.handleBlur}
        placeholder={placeholder}
        disabled={disabled}
        className={cn(textareaClassName, disabled && 'opacity-60 cursor-not-allowed', className)}
        {...ariaProps(id, error)}
      />
    </FieldShell>
  );
}

/** Native `<select>` — pass `<option>` elements as children. */
export function SelectField({
  label,
  helper,
  required,
  disabled,
  className,
  children,
}: Omit<CommonFieldProps, 'placeholder'> & { children: React.ReactNode }) {
  const field = useFieldContext<string>();
  const id = useId();
  const error = firstError(field.state.meta);
  return (
    <FieldShell id={id} label={label} helper={helper} required={required} error={error}>
      <select
        id={id}
        value={field.state.value ?? ''}
        onChange={(e) => field.handleChange(e.target.value)}
        onBlur={field.handleBlur}
        disabled={disabled}
        className={cn(inputClassName, disabled && 'opacity-60 cursor-not-allowed', className)}
        {...ariaProps(id, error)}
      >
        {children}
      </select>
    </FieldShell>
  );
}

/** Toggle-button switch (today's smtp/connector-form pattern) with
 *  `role="switch"` + `aria-checked` a11y on top. */
export function SwitchField({
  label,
  helper,
  disabled,
}: Omit<CommonFieldProps, 'placeholder' | 'required'>) {
  const field = useFieldContext<boolean>();
  const id = useId();
  const checked = Boolean(field.state.value);
  return (
    <div className="flex items-center justify-between p-3 rounded-lg border border-border">
      <div>
        <label htmlFor={id} className="text-sm font-medium text-foreground">
          {label}
        </label>
        {helper && <p className="text-xs text-muted-foreground">{helper}</p>}
      </div>
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => field.handleChange(!checked)}
        onBlur={field.handleBlur}
        className={cn(
          'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
          checked ? 'bg-status-success' : 'bg-muted',
          disabled && 'opacity-60 cursor-not-allowed',
        )}
      >
        <span
          className={cn(
            'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
            checked ? 'translate-x-6' : 'translate-x-1',
          )}
        />
      </button>
    </div>
  );
}

/** Native checkbox in today's label + description row layout. */
export function CheckboxField({
  label,
  helper,
  disabled,
}: Omit<CommonFieldProps, 'placeholder' | 'required'>) {
  const field = useFieldContext<boolean>();
  const id = useId();
  return (
    <div className="flex items-start gap-3 text-sm">
      <input
        id={id}
        type="checkbox"
        checked={Boolean(field.state.value)}
        onChange={(e) => field.handleChange(e.target.checked)}
        onBlur={field.handleBlur}
        disabled={disabled}
        className="mt-0.5 h-4 w-4 rounded border-border"
      />
      <div>
        <label htmlFor={id} className="text-foreground font-medium">
          {label}
        </label>
        {helper && <div className="text-xs text-muted-foreground">{helper}</div>}
      </div>
    </div>
  );
}

/** Primary submit button — disabled while invalid, spinner while submitting. */
export function SubmitButton({ children }: { children: React.ReactNode }) {
  const form = useFormContext();
  return (
    <form.Subscribe selector={(state) => [state.canSubmit, state.isSubmitting] as const}>
      {([canSubmit, isSubmitting]) => (
        <button
          type="submit"
          disabled={!canSubmit}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {isSubmitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {children}
        </button>
      )}
    </form.Subscribe>
  );
}
