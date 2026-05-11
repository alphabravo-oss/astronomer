'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { Orbit, Eye, EyeOff, Loader2, KeyRound, ArrowRight } from 'lucide-react';
import { useAuthStore } from '@/lib/store';
import { changeOwnPassword } from '@/lib/api';
import { toast } from 'sonner';

// Forced password-rotation screen for the bootstrap admin and for any user
// whose `must_change_password` flag is set. Reachable directly at
// /auth/change-password; dashboard layout middleware also redirects here
// while the flag is set.

export default function ChangePasswordPage() {
  const router = useRouter();
  const { user, updateUser, logout } = useAuthStore();
  const [showCurrent, setShowCurrent] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [loading, setLoading] = useState(false);
  const [form, setForm] = useState({ current: '', next: '', confirm: '' });

  const newLongEnough = form.next.length >= 12;
  const newDiffers = form.next !== form.current;
  const matches = form.next === form.confirm;
  const canSubmit = !!form.current && newLongEnough && newDiffers && matches;

  const forced = !!user?.must_change_password;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setLoading(true);
    try {
      await changeOwnPassword(form.current, form.next);
      updateUser({ must_change_password: false });
      toast.success('Password updated');
      router.push('/dashboard');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update password');
    } finally {
      setLoading(false);
    }
  };

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

        <form onSubmit={handleSubmit} className="space-y-4 rounded-lg border border-border bg-card p-6 shadow-sm">
          <PasswordField
            label="Current password"
            id="current"
            visible={showCurrent}
            onToggleVisible={() => setShowCurrent((v) => !v)}
            value={form.current}
            onChange={(v) => setForm((f) => ({ ...f, current: v }))}
            autoFocus
          />
          <PasswordField
            label="New password"
            id="next"
            visible={showNew}
            onToggleVisible={() => setShowNew((v) => !v)}
            value={form.next}
            onChange={(v) => setForm((f) => ({ ...f, next: v }))}
            hint={
              form.next.length === 0
                ? 'At least 12 characters.'
                : !newLongEnough
                  ? 'Must be at least 12 characters.'
                  : !newDiffers
                    ? 'Must differ from the current password.'
                    : 'Looks good.'
            }
            hintTone={form.next.length === 0 ? 'muted' : (newLongEnough && newDiffers) ? 'success' : 'danger'}
          />
          <PasswordField
            label="Confirm new password"
            id="confirm"
            visible={showConfirm}
            onToggleVisible={() => setShowConfirm((v) => !v)}
            value={form.confirm}
            onChange={(v) => setForm((f) => ({ ...f, confirm: v }))}
            hint={
              form.confirm.length === 0
                ? ' '
                : matches
                  ? 'Matches.'
                  : 'Does not match the new password.'
            }
            hintTone={form.confirm.length === 0 ? 'muted' : matches ? 'success' : 'danger'}
          />

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
                ? 'text-xs text-status-danger'
                : 'text-xs text-muted-foreground'
          }
        >
          {hint}
        </p>
      )}
    </div>
  );
}
