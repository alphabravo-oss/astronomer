// Route files are the eslint-exempted surface for direct router imports.
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import { sanitizeReturnTo } from '@/lib/auth/session';
import { Orbit, Github, Chrome, KeyRound, Eye, EyeOff, Loader2, ArrowRight, Shield, ArrowLeft } from 'lucide-react';
import { useAuthStore } from '@/lib/store';
import { useSSOProviders } from '@/lib/hooks';
import { useAppForm, useStore } from '@/lib/form';
import {
  loginWithCredentialsChallengeAware,
  verifyTotpChallenge,
  type TotpChallenge,
} from '@/lib/api/account-security';
import type { SSOProvider, User } from '@/types';
import { toastApiError, toastError } from '@/lib/toast';

export const Route = createFileRoute('/auth/login/')({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { returnTo?: string } & Record<string, unknown>,
  component: LoginPage,
});

function LoginPage() {
  const router = useRouter();
  // returnTo round-trips the deep link the auth guard (or the api.ts 401
  // handler) captured; sanitizeReturnTo guards against open redirects (D3).
  const { returnTo } = Route.useSearch();
  const { login } = useAuthStore();
  const [showPassword, setShowPassword] = useState(false);
  const [ssoLoading, setSsoLoading] = useState<string | null>(null);
  // 423 challenge state: when present, render the TOTP screen instead of the
  // credentials form. `enrollmentRequired` distinguishes the "you must enroll
  // now" branch from the standard "enter your code" branch.
  const [challenge, setChallenge] = useState<TotpChallenge | null>(null);

  // SSO providers come from React Query (cached, retry-aware, devtools-visible).
  // On error the query data is undefined → we fall back to an empty list, the
  // same behavior the old imperative fetch had.
  const { data: ssoProvidersData } = useSSOProviders();
  const ssoProviders = (ssoProvidersData ?? []).filter((provider) => provider.enabled);

  const form = useAppForm({
    defaultValues: { email: '', password: '' },
    validators: {
      // Old check (imperative, pre-submit): `if (!form.email || !form.password)`
      // → ported 1:1 as a form-level onSubmit validator.
      onSubmit: ({ value }) =>
        !value.email || !value.password ? 'Please enter your email address and password' : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: () => toastError('Please enter your email address and password'),
    onSubmit: async ({ value }) => {
      try {
        const result = await loginWithCredentialsChallengeAware(value.email, value.password);
        if (result.kind === 'challenge') {
          setChallenge(result.challenge);
          return;
        }
        login(result.user);
        router.push(sanitizeReturnTo(returnTo));
      } catch (error) {
        toastApiError('', error, 'Login failed');
      }
    },
  });
  const loading = useStore(form.store, (state) => state.isSubmitting);

  const completeTotp = (_token: string, _refresh: string | undefined, user: User) => {
    login(user);
    router.push(sanitizeReturnTo(returnTo));
  };

  const handleSSO = async (provider: string) => {
    setSsoLoading(provider);
    try {
      window.location.href = `/api/v1/auth/login/${provider}/`;
    } catch {
      toastError(`Failed to initiate ${provider} login`);
      setSsoLoading(null);
    }
  };

  const providerIcon = (type: SSOProvider['type']) => {
    switch (type) {
      case 'github':
        return <Github className="h-4 w-4" />;
      case 'google':
        return <Chrome className="h-4 w-4" />;
      default:
        return <KeyRound className="h-4 w-4" />;
    }
  };

  return (
    <div className="min-h-screen flex">
      {/* Left panel - Branding */}
      <div className="hidden lg:flex lg:w-1/2 relative bg-gradient-to-br from-zinc-950 via-zinc-900 to-zinc-950 flex-col justify-between p-12 overflow-hidden">
        {/* Background pattern */}
        <div className="absolute inset-0 opacity-[0.03]">
          <svg className="w-full h-full" xmlns="http://www.w3.org/2000/svg">
            <defs>
              <pattern id="grid" width="40" height="40" patternUnits="userSpaceOnUse">
                <path d="M 40 0 L 0 0 0 40" fill="none" stroke="white" strokeWidth="0.5" />
              </pattern>
            </defs>
            <rect width="100%" height="100%" fill="url(#grid)" />
          </svg>
        </div>

        {/* Accent glow */}
        <div className="absolute top-1/3 left-1/2 -translate-x-1/2 w-96 h-96 bg-blue-500/10 rounded-full blur-[120px]" />
        <div className="absolute bottom-1/4 left-1/3 w-64 h-64 bg-violet-500/10 rounded-full blur-[100px]" />

        <div className="relative">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center">
              <Orbit className="h-5 w-5 text-white" />
            </div>
            <div className="flex flex-col">
              <span className="text-xl font-semibold text-white tracking-tight leading-tight">Astronomer</span>
              <span className="text-[11px] text-zinc-500 leading-tight">by AlphaBravo</span>
            </div>
          </div>
        </div>

        <div className="relative space-y-4">
          <h1 className="text-4xl font-bold text-white leading-tight">
            Kubernetes Multi-Cluster
            <br />
            <span className="text-gradient">Management Platform</span>
          </h1>
          <p className="text-lg text-zinc-400 max-w-md leading-relaxed">
            Manage, monitor, and secure your entire Kubernetes infrastructure
            from a single control plane. Built for enterprise scale.
          </p>
        </div>

        <div className="relative space-y-4">
          <div className="flex items-center gap-8 text-sm text-zinc-500">
            <div className="flex items-center gap-2">
              <div className="h-2 w-2 rounded-full bg-status-success" />
              Multi-cluster management
            </div>
            <div className="flex items-center gap-2">
              <div className="h-2 w-2 rounded-full bg-status-info" />
              GitOps with ArgoCD
            </div>
            <div className="flex items-center gap-2">
              <div className="h-2 w-2 rounded-full bg-violet-400" />
              Enterprise RBAC
            </div>
          </div>
          <p className="text-xs text-zinc-600">
            Developed by{' '}
            <a href="https://alphabravo.io" target="_blank" rel="noopener noreferrer" className="text-zinc-500 hover:text-zinc-300 transition-colors">
              AlphaBravo
            </a>
          </p>
        </div>
      </div>

      {/* Right panel - Login form */}
      <div className="flex-1 flex items-center justify-center p-8 bg-background">
        <div className="w-full max-w-sm space-y-8">
          {/* Mobile logo */}
          <div className="lg:hidden flex items-center gap-3 justify-center">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center">
              <Orbit className="h-5 w-5 text-white" />
            </div>
            <div className="flex flex-col">
              <span className="text-xl font-semibold text-foreground tracking-tight leading-tight">Astronomer</span>
              <span className="text-[11px] text-muted-foreground leading-tight">by AlphaBravo</span>
            </div>
          </div>

          <div className="space-y-2 text-center lg:text-left">
            <h2 className="text-2xl font-semibold text-foreground tracking-tight">
              Sign in to Astronomer
            </h2>
            <p className="text-sm text-muted-foreground">
              Enter your credentials or use SSO to continue
            </p>
          </div>

          {challenge && (
            <TotpChallengeForm
              challenge={challenge}
              onCancel={() => setChallenge(null)}
              onSuccess={completeTotp}
            />
          )}

          {!challenge && ssoProviders.length > 0 && (
            <div className="space-y-2.5">
              {ssoProviders.map((provider) => (
                <button
                  key={provider.id}
                  onClick={() => handleSSO(provider.provider)}
                  disabled={!!ssoLoading}
                  className="w-full inline-flex items-center justify-center gap-2.5 h-10 px-4 rounded-lg
                    border border-border bg-card text-sm font-medium text-foreground
                    hover:bg-accent transition-colors disabled:opacity-50"
                >
                  {ssoLoading === provider.provider ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    providerIcon(provider.type)
                  )}
                  Continue with {provider.name}
                </button>
              ))}
            </div>
          )}

          {/* Divider */}
          {!challenge && (
          <div className="relative">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full border-t border-border" />
            </div>
            <div className="relative flex justify-center text-xs">
              <span className="px-3 bg-background text-muted-foreground">
                {ssoProviders.length > 0 ? 'or continue with password' : 'continue with password'}
              </span>
            </div>
          </div>
          )}

          {/* Login Form */}
          {!challenge && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void form.handleSubmit();
            }}
            className="space-y-4"
          >
            <form.Field name="email">
              {(field) => (
                <div className="space-y-1.5">
                  <label htmlFor="identifier" className="text-sm font-medium text-foreground">
                    Email
                  </label>
                  <input
                    id="identifier"
                    type="email"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    placeholder="you@example.com"
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      text-foreground placeholder:text-muted-foreground
                      focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent
                      transition-colors"
                    autoComplete="email"
                    autoFocus
                  />
                </div>
              )}
            </form.Field>

            <form.Field name="password">
              {(field) => (
                <div className="space-y-1.5">
                  <label htmlFor="password" className="text-sm font-medium text-foreground">
                    Password
                  </label>
                  <div className="relative">
                    <input
                      id="password"
                      type={showPassword ? 'text' : 'password'}
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value)}
                      onBlur={field.handleBlur}
                      placeholder="Enter your password"
                      className="w-full h-10 px-3 pr-10 rounded-lg border border-border bg-background text-sm
                        text-foreground placeholder:text-muted-foreground
                        focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent
                        transition-colors"
                      autoComplete="current-password"
                    />
                    <button
                      type="button"
                      onClick={() => setShowPassword(!showPassword)}
                      className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                  </div>
                </div>
              )}
            </form.Field>

            <button
              type="submit"
              disabled={loading}
              className="w-full inline-flex items-center justify-center gap-2 h-10 px-4 rounded-lg
                bg-primary text-primary-foreground text-sm font-medium
                hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {loading ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <>
                  Sign in
                  <ArrowRight className="h-4 w-4" />
                </>
              )}
            </button>

            <div className="text-center">
              <Link
                href="/auth/login/forgot-password"
                className="text-xs text-muted-foreground hover:text-foreground transition-colors"
              >
                Forgot your password?
              </Link>
            </div>
          </form>
          )}

          <p className="text-xs text-center text-muted-foreground">
            By signing in, you agree to the Astronomer terms of service and privacy policy.
          </p>
        </div>
      </div>
    </div>
  );
}

/**
 * Two-factor challenge step shown after a 423 from /auth/login.
 *
 * Two variants:
 *  - `totp_required`            → user is enrolled, enter the 6-digit code.
 *  - `totp_enrollment_required` → operator policy mandates TOTP; user must
 *                                  finish enrolling before the dashboard.
 *
 * For the enrollment-required branch we route the user to the standalone
 * enrollment page, passing the `challenge_token` via the URL hash so it does
 * not appear in referer logs. The security page picks it up and uses it as
 * the enrollment session token instead of issuing a regular `/start/`.
 */
function TotpChallengeForm({
  challenge,
  onCancel,
  onSuccess,
}: {
  challenge: TotpChallenge;
  onCancel: () => void;
  onSuccess: (token: string, refresh: string | undefined, user: User) => void;
}) {
  const [useRecovery, setUseRecovery] = useState(false);

  const form = useAppForm({
    defaultValues: { code: '' },
    validators: {
      // Old checks (imperative): `if (!code) return` in submit plus the
      // disabled-button gate requiring 6 digits in authenticator mode —
      // ported 1:1 as a form-level onSubmit validator.
      onSubmit: ({ value }) =>
        !value.code || (!useRecovery && value.code.length !== 6) ? 'Enter your code' : undefined,
    },
    onSubmit: async ({ value }) => {
      try {
        const data = await verifyTotpChallenge(challenge.challengeToken, value.code);
        onSuccess(data.token, data.refresh, data.user);
      } catch (err) {
        toastApiError('', err, 'Invalid code');
      }
    },
  });
  const code = useStore(form.store, (state) => state.values.code);
  const busy = useStore(form.store, (state) => state.isSubmitting);

  if (challenge.error === 'totp_enrollment_required') {
    return (
      <div className="rounded-lg border border-status-warning/40 bg-status-warning/10 p-4 space-y-3">
        <div className="flex items-start gap-3">
          <Shield className="h-5 w-5 text-status-warning flex-shrink-0 mt-0.5" />
          <div>
            <p className="text-sm font-medium text-foreground">2FA setup is required</p>
            <p className="text-xs text-muted-foreground mt-1">
              Your administrator requires two-factor authentication for all accounts. Set up an
              authenticator app to continue.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={onCancel}
            className="inline-flex items-center gap-1 h-9 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <ArrowLeft className="h-4 w-4" />
            Back
          </button>
          <button
            onClick={() => {
              window.location.href = `/dashboard/account/security#enroll=${encodeURIComponent(challenge.challengeToken)}`;
            }}
            className="flex-1 inline-flex items-center justify-center gap-2 h-9 px-4 rounded bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90"
          >
            Set up 2FA now
            <ArrowRight className="h-4 w-4" />
          </button>
        </div>
      </div>
    );
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
      className="space-y-4"
    >
      <div className="flex items-start gap-3 p-3 rounded-md border border-border bg-card">
        <Shield className="h-4 w-4 text-foreground flex-shrink-0 mt-0.5" />
        <p className="text-xs text-muted-foreground">
          Enter the {useRecovery ? 'recovery code' : 'six-digit code'} from your authenticator.
        </p>
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">
          {useRecovery ? 'Recovery code' : 'Authenticator code'}
        </label>
        <form.Field name="code">
          {(field) =>
            useRecovery ? (
              <input
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value.trim())}
                onBlur={field.handleBlur}
                placeholder="xxxx-xxxx-xxxx"
                autoFocus
                autoComplete="off"
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              />
            ) : (
              <input
                type="text"
                inputMode="numeric"
                pattern="[0-9]*"
                maxLength={6}
                value={field.state.value}
                autoFocus
                autoComplete="one-time-code"
                onChange={(e) => field.handleChange(e.target.value.replace(/\D/g, '').slice(0, 6))}
                onBlur={field.handleBlur}
                placeholder="123 456"
                className="w-full h-12 px-3 rounded-md border border-border bg-background text-center text-2xl font-mono tracking-[0.4em] text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              />
            )
          }
        </form.Field>
      </div>
      <button
        type="submit"
        disabled={!code || busy || (!useRecovery && code.length !== 6)}
        className="w-full inline-flex items-center justify-center gap-2 h-10 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
      >
        {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <>Verify<ArrowRight className="h-4 w-4" /></>}
      </button>
      <div className="flex items-center justify-between text-xs">
        <button
          type="button"
          onClick={onCancel}
          className="text-muted-foreground hover:text-foreground transition-colors"
        >
          ← Use a different account
        </button>
        <button
          type="button"
          onClick={() => {
            setUseRecovery((v) => !v);
            form.setFieldValue('code', '');
          }}
          className="text-muted-foreground hover:text-foreground transition-colors"
        >
          {useRecovery ? 'Use authenticator code' : 'Use recovery code instead'}
        </button>
      </div>
    </form>
  );
}
