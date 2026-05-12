'use client';

/**
 * Forgot-password — collects an email/username, posts to
 * /auth/password-reset/request and always shows the same "if it exists, a
 * link has been sent" success screen. The backend returns 202 unconditionally
 * (no user enumeration), so the UI mirrors that.
 */

import { useState } from 'react';
import Link from 'next/link';
import { Orbit, Loader2, Mail, ArrowLeft, Check } from 'lucide-react';
import { toast } from 'sonner';
import { requestPasswordReset } from '@/lib/api/account-security';

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState('');
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email) {
      toast.error('Enter an email address');
      return;
    }
    setLoading(true);
    try {
      await requestPasswordReset(email);
      setSubmitted(true);
    } catch (err) {
      // The endpoint always returns 202; surface only network errors.
      toast.error(err instanceof Error ? err.message : 'Could not send reset email');
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
            {submitted ? 'Check your inbox' : 'Reset your password'}
          </h1>
          <p className="text-sm text-muted-foreground">
            {submitted
              ? "If an account exists for that email, we've sent a reset link."
              : "We'll email you a link to set a new password."}
          </p>
        </div>

        {submitted ? (
          <div className="rounded-lg border border-border bg-card p-6 space-y-4 text-center">
            <div className="mx-auto h-12 w-12 rounded-full bg-status-success/10 flex items-center justify-center">
              <Check className="h-6 w-6 text-status-success" />
            </div>
            <p className="text-sm text-muted-foreground">
              The link expires in 30 minutes. If you don&apos;t see it, check your spam folder or
              contact your administrator.
            </p>
            <Link
              href="/auth/login"
              className="inline-flex items-center gap-1 text-sm text-foreground hover:underline"
            >
              <ArrowLeft className="h-4 w-4" /> Back to sign in
            </Link>
          </div>
        ) : (
          <form
            onSubmit={submit}
            className="space-y-4 rounded-lg border border-border bg-card p-6 shadow-sm"
          >
            <div className="space-y-1.5">
              <label htmlFor="email" className="text-sm font-medium text-foreground">
                Email address
              </label>
              <div className="relative">
                <input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  autoComplete="email"
                  autoFocus
                  className="w-full h-10 pl-9 pr-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                  placeholder="you@example.com"
                />
                <Mail className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              </div>
            </div>
            <button
              type="submit"
              disabled={loading || !email}
              className="w-full inline-flex items-center justify-center gap-2 h-10 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Send reset link'}
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
