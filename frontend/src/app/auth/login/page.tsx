'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Orbit, Github, Chrome, KeyRound, Eye, EyeOff, Loader2, ArrowRight } from 'lucide-react';
import { useAuthStore } from '@/lib/store';
import { getSSOProviders, loginWithCredentials } from '@/lib/api';
import type { SSOProvider } from '@/types';
import { toast } from 'sonner';

export default function LoginPage() {
  const router = useRouter();
  const { login } = useAuthStore();
  const [showPassword, setShowPassword] = useState(false);
  const [loading, setLoading] = useState(false);
  const [ssoLoading, setSsoLoading] = useState<string | null>(null);
  const [ssoProviders, setSSOProviders] = useState<SSOProvider[]>([]);
  const [form, setForm] = useState({ email: '', password: '' });

  useEffect(() => {
    let cancelled = false;
    getSSOProviders()
      .then((providers) => {
        if (!cancelled) {
          setSSOProviders(providers.filter((provider) => provider.enabled));
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSSOProviders([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.email || !form.password) {
      toast.error('Please enter your email/username and password');
      return;
    }

    setLoading(true);
    try {
      const data = await loginWithCredentials(form.email, form.password);
      login(data.user, data.token);
      localStorage.setItem('astronomer_token', data.token);
      if (data.refresh) {
        localStorage.setItem('astronomer_refresh', data.refresh);
      }
      router.push('/dashboard');
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  const handleSSO = async (provider: string) => {
    setSsoLoading(provider);
    try {
      window.location.href = `/api/v1/auth/login/${provider}/`;
    } catch {
      toast.error(`Failed to initiate ${provider} login`);
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

          {ssoProviders.length > 0 && (
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

          {/* Login Form */}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1.5">
              <label htmlFor="identifier" className="text-sm font-medium text-foreground">
                Email or username
              </label>
              <input
                id="identifier"
                type="text"
                value={form.email}
                onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
                placeholder="admin or you@example.com"
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                  text-foreground placeholder:text-muted-foreground
                  focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent
                  transition-colors"
                autoComplete="username"
                autoFocus
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="password" className="text-sm font-medium text-foreground">
                Password
              </label>
              <div className="relative">
                <input
                  id="password"
                  type={showPassword ? 'text' : 'password'}
                  value={form.password}
                  onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
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
          </form>

          <p className="text-xs text-center text-muted-foreground">
            By signing in, you agree to the Astronomer terms of service and privacy policy.
          </p>
        </div>
      </div>
    </div>
  );
}
