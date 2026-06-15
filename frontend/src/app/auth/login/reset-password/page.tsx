'use client';

/**
 * Reset-password — completes a password reset using the one-time `token`
 * delivered by email. The token lives in the URL query (`?token=...`) so it
 * survives clipboard copy/paste; we never log it.
 *
 * Validation:
 *   - new password ≥ 12 chars (matches the change-password screen)
 *   - confirm field must match
 *   - on success → toast + redirect to /auth/login
 */

import { useEffect, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import Link from 'next/link';
import { Orbit, Loader2, Eye, EyeOff, Check, AlertTriangle, ArrowLeft, ArrowRight } from 'lucide-react';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { completePasswordReset } from '@/lib/api/account-security';

export default function ResetPasswordPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const token = searchParams?.get('token') ?? '';

  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [showNext, setShowNext] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [loading, setLoading] = useState(false);
  const [done, setDone] = useState(false);

  const longEnough = next.length >= 12;
  const matches = next === confirm;
  const canSubmit = !!token && longEnough && matches;

  useEffect(() => {
    if (done) {
      // Send the user to /auth/login after a brief pause so they see the
      // success banner.
      const t = setTimeout(() => router.push('/auth/login'), 2000);
      return () => clearTimeout(t);
    }
  }, [done, router]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setLoading(true);
    try {
      await completePasswordReset(token, next);
      toastSuccess('Password reset — sign in with your new password.');
      setDone(true);
    } catch (err) {
      toastApiError('', err, 'Reset failed. The link may have expired.');
    } finally {
      setLoading(false);
    }
  };

  if (!token) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background px-6">
        <div className="w-full max-w-md space-y-6 text-center">
          <Orbit className="h-8 w-8 text-foreground mx-auto" />
          <div className="rounded-lg border border-status-error/40 bg-status-error/10 p-6 space-y-3">
            <AlertTriangle className="h-6 w-6 text-status-error mx-auto" />
            <h1 className="text-base font-semibold text-foreground">Invalid reset link</h1>
            <p className="text-sm text-muted-foreground">
              This reset link is missing its token. Request a new one to continue.
            </p>
            <Link
              href="/auth/login/forgot-password"
              className="inline-flex items-center gap-1 text-sm text-foreground hover:underline"
            >
              Request a new link <ArrowRight className="h-4 w-4" />
            </Link>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-6">
      <div className="w-full max-w-md space-y-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <Orbit className="h-8 w-8 text-foreground" />
          <h1 className="text-xl font-semibold tracking-tight">
            {done ? 'Password updated' : 'Choose a new password'}
          </h1>
          {!done && (
            <p className="text-sm text-muted-foreground">At least 12 characters.</p>
          )}
        </div>

        {done ? (
          <div className="rounded-lg border border-border bg-card p-6 space-y-4 text-center">
            <div className="mx-auto h-12 w-12 rounded-full bg-status-success/10 flex items-center justify-center">
              <Check className="h-6 w-6 text-status-success" />
            </div>
            <p className="text-sm text-muted-foreground">
              You can sign in with your new password now.
            </p>
            <Link
              href="/auth/login"
              className="inline-flex items-center gap-1 text-sm text-foreground hover:underline"
            >
              <ArrowLeft className="h-4 w-4" /> Back to sign in
            </Link>
          </div>
        ) : (
          <form onSubmit={submit} className="space-y-4 rounded-lg border border-border bg-card p-6 shadow-sm">
            <PasswordField
              id="next"
              label="New password"
              value={next}
              onChange={setNext}
              visible={showNext}
              onToggleVisible={() => setShowNext((v) => !v)}
              hint={
                next.length === 0
                  ? 'At least 12 characters.'
                  : !longEnough
                    ? 'Must be at least 12 characters.'
                    : 'Looks good.'
              }
              hintTone={next.length === 0 ? 'muted' : longEnough ? 'success' : 'danger'}
              autoFocus
            />
            <PasswordField
              id="confirm"
              label="Confirm new password"
              value={confirm}
              onChange={setConfirm}
              visible={showConfirm}
              onToggleVisible={() => setShowConfirm((v) => !v)}
              hint={
                confirm.length === 0
                  ? ' '
                  : matches
                    ? 'Matches.'
                    : 'Does not match the new password.'
              }
              hintTone={confirm.length === 0 ? 'muted' : matches ? 'success' : 'danger'}
            />
            <button
              type="submit"
              disabled={!canSubmit || loading}
              className="w-full inline-flex items-center justify-center gap-2 h-10 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Update password'}
            </button>
            <div className="text-center">
              <Link
                href="/auth/login"
                className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
              >
                <ArrowLeft className="h-3 w-3" /> Back to sign in
              </Link>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}

function PasswordField({
  id,
  label,
  value,
  onChange,
  visible,
  onToggleVisible,
  hint,
  hintTone = 'muted',
  autoFocus,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  visible: boolean;
  onToggleVisible: () => void;
  hint?: string;
  hintTone?: 'muted' | 'success' | 'danger';
  autoFocus?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="text-sm font-medium text-foreground">
        {label}
      </label>
      <div className="relative">
        <input
          id={id}
          type={visible ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="new-password"
          autoFocus={autoFocus}
          className="w-full h-10 px-3 pr-10 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
        />
        <button
          type="button"
          onClick={onToggleVisible}
          className="absolute right-2 top-1/2 -translate-y-1/2 p-1 text-muted-foreground hover:text-foreground"
          tabIndex={-1}
          aria-label={visible ? 'Hide password' : 'Show password'}
        >
          {visible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </button>
      </div>
      {hint && (
        <p
          className={
            hintTone === 'success'
              ? 'text-xs text-status-success'
              : hintTone === 'danger'
                ? 'text-xs text-status-error'
                : 'text-xs text-muted-foreground'
          }
        >
          {hint}
        </p>
      )}
    </div>
  );
}
