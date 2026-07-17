import { createFileRoute } from '@tanstack/react-router';

import { useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { Orbit, Eye, EyeOff, Loader2, KeyRound, ArrowRight } from 'lucide-react';
import { useAuthStore } from '@/lib/store';
import { changeOwnPassword } from '@/lib/api';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { useAppForm, useStore } from '@/lib/form';

// Forced password-rotation screen for the bootstrap admin and for any user
// whose `must_change_password` flag is set. Reachable directly at
// /auth/change-password; dashboard layout middleware also redirects here
// while the flag is set.

function ChangePasswordPage() {
  const router = useRouter();
  const { user, updateUser, logout } = useAuthStore();
  const [showCurrent, setShowCurrent] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);

  const form = useAppForm({
    defaultValues: { current: '', next: '', confirm: '' },
    validators: {
      // Old imperative gate (`if (!canSubmit) return`) ported 1:1: current
      // required, new ≥ 12 chars, new ≠ current, confirm matches. The button
      // below is additionally disabled on the same checks, exactly as before.
      onSubmit: ({ value }) =>
        !value.current ||
        value.next.length < 12 ||
        value.next === value.current ||
        value.next !== value.confirm
          ? 'Password requirements not met'
          : undefined,
    },
    onSubmit: async ({ value }) => {
      try {
        await changeOwnPassword(value.current, value.next);
        updateUser({ must_change_password: false, mustChangePassword: false });
        toastSuccess('Password updated');
        router.push('/dashboard');
      } catch (err) {
        toastApiError('', err, 'Failed to update password');
      }
    },
  });
  // Whole-values subscription: the hint lines below are live cross-field
  // feedback (success/muted/danger tones), recomputed per keystroke exactly
  // like the old useState form.
  const values = useStore(form.store, (state) => state.values);
  const loading = useStore(form.store, (state) => state.isSubmitting);

  const newLongEnough = values.next.length >= 12;
  const newDiffers = values.next !== values.current;
  const matches = values.next === values.confirm;
  const canSubmit = !!values.current && newLongEnough && newDiffers && matches;

  const forced = !!(user?.must_change_password || user?.mustChangePassword);

  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-6">
      <div className="w-full max-w-md space-y-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <Orbit className="h-8 w-8 text-foreground" />
          <h1 className="text-xl font-semibold tracking-tight">
            {forced ? 'Set a new password to continue' : 'Change your password'}
          </h1>
          {forced && (
            <p className="text-sm text-muted-foreground">
              You signed in with the bootstrap password. Choose a new one before continuing
              to {user?.username ? <span className="font-mono">{user.username}</span> : 'your account'}.
            </p>
          )}
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault();
            void form.handleSubmit();
          }}
          className="space-y-4 rounded-lg border border-border bg-card p-6 shadow-sm"
        >
          <form.Field name="current">
            {(field) => (
              <PasswordField
                label="Current password"
                id="current"
                visible={showCurrent}
                onToggleVisible={() => setShowCurrent((v) => !v)}
                value={field.state.value}
                onChange={field.handleChange}
                autoFocus
              />
            )}
          </form.Field>
          <form.Field name="next">
            {(field) => (
              <PasswordField
                label="New password"
                id="next"
                visible={showNew}
                onToggleVisible={() => setShowNew((v) => !v)}
                value={field.state.value}
                onChange={field.handleChange}
                hint={
                  values.next.length === 0
                    ? 'At least 12 characters.'
                    : !newLongEnough
                      ? 'Must be at least 12 characters.'
                      : !newDiffers
                        ? 'Must differ from the current password.'
                        : 'Looks good.'
                }
                hintTone={values.next.length === 0 ? 'muted' : (newLongEnough && newDiffers) ? 'success' : 'danger'}
              />
            )}
          </form.Field>
          <form.Field name="confirm">
            {(field) => (
              <PasswordField
                label="Confirm new password"
                id="confirm"
                visible={showConfirm}
                onToggleVisible={() => setShowConfirm((v) => !v)}
                value={field.state.value}
                onChange={field.handleChange}
                hint={
                  values.confirm.length === 0
                    ? ' '
                    : matches
                      ? 'Matches.'
                      : 'Does not match the new password.'
                }
                hintTone={values.confirm.length === 0 ? 'muted' : matches ? 'success' : 'danger'}
              />
            )}
          </form.Field>

          <button
            type="submit"
            disabled={!canSubmit || loading}
            className="w-full inline-flex items-center justify-center gap-2 h-10 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50"
          >
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <KeyRound className="h-4 w-4" />}
            Update password
            {!loading && <ArrowRight className="h-4 w-4" />}
          </button>

          {forced && (
            <button
              type="button"
              onClick={() => {
                logout();
                router.push('/auth/login');
              }}
              className="w-full text-xs text-muted-foreground hover:text-foreground transition-colors"
            >
              Sign out
            </button>
          )}
        </form>
      </div>
    </div>
  );
}

function PasswordField({
  label,
  id,
  visible,
  onToggleVisible,
  value,
  onChange,
  hint,
  hintTone = 'muted',
  autoFocus,
}: {
  label: string;
  id: string;
  visible: boolean;
  onToggleVisible: () => void;
  value: string;
  onChange: (v: string) => void;
  hint?: string;
  hintTone?: 'muted' | 'success' | 'danger';
  autoFocus?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="text-sm font-medium text-foreground">{label}</label>
      <div className="relative">
        <input
          id={id}
          type={visible ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete={id === 'current' ? 'current-password' : 'new-password'}
          autoFocus={autoFocus}
          className="w-full h-10 px-3 pr-10 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring transition-colors"
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

export const Route = createFileRoute('/auth/change-password/')({
  component: ChangePasswordPage,
});
