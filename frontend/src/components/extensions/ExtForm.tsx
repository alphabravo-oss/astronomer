'use client';

// §Schema Tier-1 — form renderer. Renders a declarative FormSpec as first-party
// inputs (closed type enum: text|number|select|toggle) and submits the collected
// values back through the data proxy to FormSpec.submit — a POST dataSource with
// a write verb the server re-checks against the user's RBAC. No third-party JS:
// the form is host-rendered; only the typed field values cross the proxy.

import { useState, type FormEvent } from 'react';
import { fetchExtensionData } from '@/lib/api/extensions';
import type { ExtensionContext, FormSpec, FormInput } from '@/lib/api/extensions';

export interface ExtFormProps {
  extensionName: string;
  spec: FormSpec;
  context?: ExtensionContext;
}

type Values = Record<string, string | number | boolean>;

// Initial value per input type, so a controlled input is never undefined.
function initialValues(inputs: FormInput[]): Values {
  const v: Values = {};
  for (const input of inputs) {
    if (input.type === 'toggle') v[input.name] = false;
    else if (input.type === 'number') v[input.name] = '';
    else if (input.type === 'select') v[input.name] = input.options?.[0] ?? '';
    else v[input.name] = '';
  }
  return v;
}

// Coerce a raw input value to the type the dataSource expects before it crosses
// the proxy. number inputs send a number (or are dropped when blank), toggles a
// boolean, everything else a string.
export function buildSubmitBody(inputs: FormInput[], values: Values): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  for (const input of inputs) {
    const raw = values[input.name];
    if (input.type === 'number') {
      if (raw === '' || raw === undefined) continue;
      body[input.name] = Number(raw);
    } else if (input.type === 'toggle') {
      body[input.name] = Boolean(raw);
    } else {
      body[input.name] = raw ?? '';
    }
  }
  return body;
}

// Which required fields are still empty — drives client-side gating before the
// server's own validation. Pure so it is unit-testable.
export function missingRequired(inputs: FormInput[], values: Values): string[] {
  return inputs
    .filter((i) => i.required)
    .filter((i) => {
      const v = values[i.name];
      if (i.type === 'toggle') return v !== true;
      return v === '' || v === undefined || v === null;
    })
    .map((i) => i.name);
}

export function ExtForm({ extensionName, spec, context }: ExtFormProps) {
  const [values, setValues] = useState<Values>(() => initialValues(spec.inputs));
  const [status, setStatus] = useState<'idle' | 'submitting' | 'success' | 'error'>('idle');
  const [error, setError] = useState<string | null>(null);

  const set = (name: string, value: string | number | boolean) =>
    setValues((prev) => ({ ...prev, [name]: value }));

  const missing = missingRequired(spec.inputs, values);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (missing.length > 0) return;
    setStatus('submitting');
    setError(null);
    try {
      await fetchExtensionData(extensionName, spec.submit, {
        context,
        body: buildSubmitBody(spec.inputs, values),
      });
      setStatus('success');
    } catch (err) {
      setStatus('error');
      setError(err instanceof Error ? err.message : 'Submit failed');
    }
  }

  return (
    <form onSubmit={onSubmit} className="space-y-4">
      {spec.inputs.map((input) => (
        <div key={input.name} className="space-y-1">
          <label htmlFor={`ext-${input.name}`} className="block text-sm font-medium text-foreground">
            {input.label}
            {input.required && <span className="ml-0.5 text-status-error">*</span>}
          </label>
          {input.type === 'select' ? (
            <select
              id={`ext-${input.name}`}
              value={String(values[input.name] ?? '')}
              onChange={(e) => set(input.name, e.target.value)}
              className="h-9 w-full rounded-lg border border-border bg-background px-3 text-sm"
            >
              {(input.options ?? []).map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
            </select>
          ) : input.type === 'toggle' ? (
            <input
              id={`ext-${input.name}`}
              type="checkbox"
              checked={Boolean(values[input.name])}
              onChange={(e) => set(input.name, e.target.checked)}
              className="h-4 w-4 rounded border-border"
            />
          ) : (
            <input
              id={`ext-${input.name}`}
              type={input.type === 'number' ? 'number' : 'text'}
              value={String(values[input.name] ?? '')}
              maxLength={input.maxLength}
              onChange={(e) => set(input.name, e.target.value)}
              className="h-9 w-full rounded-lg border border-border bg-background px-3 text-sm"
            />
          )}
        </div>
      ))}

      {status === 'error' && error && (
        <p role="alert" className="text-sm text-status-error">
          {error}
        </p>
      )}
      {status === 'success' && (
        <p role="status" className="text-sm text-status-success">
          Submitted.
        </p>
      )}

      <button
        type="submit"
        disabled={status === 'submitting' || missing.length > 0}
        className="inline-flex h-9 items-center rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {status === 'submitting' ? 'Submitting…' : spec.submitLabel || 'Submit'}
      </button>
    </form>
  );
}
