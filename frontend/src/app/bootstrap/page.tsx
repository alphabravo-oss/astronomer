'use client';

import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import {
  Orbit,
  ArrowRight,
  ArrowLeft,
  Check,
  Eye,
  EyeOff,
  Loader2,
  Shield,
  Globe,
  Settings,
  Rocket,
} from 'lucide-react';
import { useAuthStore } from '@/lib/store';
import api, { setSessionCookie } from '@/lib/api';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';

interface BootstrapResponse {
  token: string;
  user: {
    id: string;
    username: string;
    email: string;
    displayName: string;
    avatarUrl?: string;
    provider: 'local';
    globalRoles: string[];
    enabled: boolean;
    lastLogin: string;
    createdAt: string;
  };
}

const steps = [
  { id: 'welcome', label: 'Welcome', icon: Orbit },
  { id: 'admin', label: 'Admin Account', icon: Shield },
  { id: 'server', label: 'Server URL', icon: Globe },
  { id: 'preferences', label: 'Preferences', icon: Settings },
  { id: 'complete', label: 'Complete', icon: Rocket },
];

export default function BootstrapPage() {
  const router = useRouter();
  const { login } = useAuthStore();
  const [currentStep, setCurrentStep] = useState(0);
  const [loading, setLoading] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [showConfirmPassword, setShowConfirmPassword] = useState(false);
  const [setupDone, setSetupDone] = useState(false);

  const [form, setForm] = useState({
    password: '',
    confirmPassword: '',
    email: '',
    serverUrl: '',
    enableTelemetry: false,
    platformName: 'Astronomer',
  });

  useEffect(() => {
    if (typeof window !== 'undefined') {
      setForm((f) => ({ ...f, serverUrl: window.location.origin }));
    }
  }, []);

  const passwordValid = form.password.length >= 12;
  const passwordsMatch = form.password === form.confirmPassword;
  const emailValid = form.email.includes('@') && form.email.includes('.');

  const canAdvance = (): boolean => {
    switch (currentStep) {
      case 0: return true;
      case 1: return passwordValid && passwordsMatch && emailValid;
      case 2: return form.serverUrl.length > 0;
      case 3: return true;
      case 4: return true;
      default: return false;
    }
  };

  const handleNext = () => {
    if (currentStep < steps.length - 1 && canAdvance()) {
      setCurrentStep((s) => s + 1);
    }
  };

  const handleBack = () => {
    if (currentStep > 0) {
      setCurrentStep((s) => s - 1);
    }
  };

  const handleComplete = async () => {
    setLoading(true);
    try {
      const res = await api.post<{ data: BootstrapResponse }>('/bootstrap/complete', {
        password: form.password,
        email: form.email,
        serverUrl: form.serverUrl,
        enableTelemetry: form.enableTelemetry,
        platformName: form.platformName,
      });

      const { token, user } = res.data.data;
      login(user, token);
      localStorage.setItem('astronomer_token', token);
      // Mirror the JWT into the cookie used by the /argocd/* reverse proxy.
      setSessionCookie(token);
      setSetupDone(true);
      toast.success('Setup complete! Redirecting to dashboard...');
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Bootstrap failed');
    } finally {
      setLoading(false);
    }
  };

  const goToDashboard = () => {
    router.push('/dashboard');
  };

  return (
    <div className="min-h-screen flex bg-background">
      {/* Left panel - Steps */}
      <div className="hidden lg:flex lg:w-80 bg-gradient-to-b from-zinc-950 via-zinc-900 to-zinc-950 flex-col p-8">
        {/* Logo */}
        <div className="flex items-center gap-3 mb-12">
          <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center">
            <Orbit className="h-5 w-5 text-white" />
          </div>
          <span className="text-xl font-semibold text-white tracking-tight">Astronomer</span>
        </div>

        {/* Step indicators */}
        <nav className="space-y-1 flex-1">
          {steps.map((step, idx) => {
            const StepIcon = step.icon;
            const isActive = idx === currentStep;
            const isComplete = idx < currentStep;
            return (
              <div
                key={step.id}
                className={cn(
                  'flex items-center gap-3 px-4 py-3 rounded-lg transition-colors',
                  isActive ? 'bg-white/10 text-white' : isComplete ? 'text-zinc-400' : 'text-zinc-600'
                )}
              >
                <div
                  className={cn(
                    'flex items-center justify-center w-8 h-8 rounded-full border-2 transition-colors flex-shrink-0',
                    isActive ? 'border-blue-500 bg-blue-500/20' :
                    isComplete ? 'border-zinc-500 bg-zinc-500/20' : 'border-zinc-700'
                  )}
                >
                  {isComplete ? (
                    <Check className="h-4 w-4 text-zinc-400" />
                  ) : (
                    <StepIcon className={cn('h-4 w-4', isActive ? 'text-blue-400' : 'text-zinc-600')} />
                  )}
                </div>
                <span className={cn('text-sm font-medium', isActive ? 'text-white' : isComplete ? 'text-zinc-400' : 'text-zinc-600')}>
                  {step.label}
                </span>
              </div>
            );
          })}
        </nav>

        <p className="text-xs text-zinc-600">
          Step {currentStep + 1} of {steps.length}
        </p>
      </div>

      {/* Right panel - Content */}
      <div className="flex-1 flex items-center justify-center p-8">
        <div className="w-full max-w-md space-y-8">
          {/* Mobile step indicator */}
          <div className="lg:hidden flex items-center justify-between mb-4">
            <div className="flex items-center gap-3">
              <div className="w-8 h-8 rounded-xl bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center">
                <Orbit className="h-4 w-4 text-white" />
              </div>
              <span className="font-semibold text-foreground">Astronomer</span>
            </div>
            <span className="text-xs text-muted-foreground">Step {currentStep + 1}/{steps.length}</span>
          </div>

          {/* Progress bar */}
          <div className="w-full h-1 rounded-full bg-muted overflow-hidden">
            <div
              className="h-full rounded-full bg-gradient-to-r from-blue-500 to-violet-500 transition-all duration-500"
              style={{ width: `${((currentStep + 1) / steps.length) * 100}%` }}
            />
          </div>

          {/* Step 1: Welcome */}
          {currentStep === 0 && (
            <div className="space-y-6 animate-fade-in">
              <div className="flex justify-center">
                <div className="w-20 h-20 rounded-2xl bg-gradient-to-br from-blue-500 to-violet-600 flex items-center justify-center shadow-lg">
                  <Orbit className="h-10 w-10 text-white" />
                </div>
              </div>
              <div className="text-center space-y-3">
                <h1 className="text-3xl font-bold text-foreground tracking-tight">
                  Welcome to Astronomer
                </h1>
                <p className="text-muted-foreground leading-relaxed">
                  Let&apos;s set up your Kubernetes multi-cluster management platform.
                  This wizard will guide you through the initial configuration in just a few steps.
                </p>
              </div>
              <div className="grid grid-cols-3 gap-3 pt-4">
                <div className="rounded-lg border border-border p-3 text-center">
                  <Shield className="h-6 w-6 text-blue-500 mx-auto mb-2" />
                  <p className="text-xs text-muted-foreground">Enterprise RBAC</p>
                </div>
                <div className="rounded-lg border border-border p-3 text-center">
                  <Globe className="h-6 w-6 text-violet-500 mx-auto mb-2" />
                  <p className="text-xs text-muted-foreground">Multi-Cluster</p>
                </div>
                <div className="rounded-lg border border-border p-3 text-center">
                  <Settings className="h-6 w-6 text-emerald-500 mx-auto mb-2" />
                  <p className="text-xs text-muted-foreground">GitOps Ready</p>
                </div>
              </div>
            </div>
          )}

          {/* Step 2: Admin Account */}
          {currentStep === 1 && (
            <div className="space-y-6 animate-fade-in">
              <div className="space-y-2">
                <h2 className="text-2xl font-semibold text-foreground tracking-tight">
                  Admin Account
                </h2>
                <p className="text-sm text-muted-foreground">
                  Set up your administrator credentials. This account will have full platform access.
                </p>
              </div>

              <div className="space-y-4">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Email</label>
                  <input
                    type="email"
                    value={form.email}
                    onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
                    placeholder="admin@example.com"
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      text-foreground placeholder:text-muted-foreground
                      focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent"
                    autoFocus
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Password</label>
                  <div className="relative">
                    <input
                      type={showPassword ? 'text' : 'password'}
                      value={form.password}
                      onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                      placeholder="Minimum 12 characters"
                      className="w-full h-10 px-3 pr-10 rounded-lg border border-border bg-background text-sm
                        text-foreground placeholder:text-muted-foreground
                        focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent"
                    />
                    <button
                      type="button"
                      onClick={() => setShowPassword(!showPassword)}
                      className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                  </div>
                  {form.password && !passwordValid && (
                    <p className="text-xs text-status-error">Password must be at least 12 characters</p>
                  )}
                  {form.password && passwordValid && (
                    <p className="text-xs text-status-success">Password meets requirements</p>
                  )}
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Confirm Password</label>
                  <div className="relative">
                    <input
                      type={showConfirmPassword ? 'text' : 'password'}
                      value={form.confirmPassword}
                      onChange={(e) => setForm((f) => ({ ...f, confirmPassword: e.target.value }))}
                      placeholder="Confirm your password"
                      className="w-full h-10 px-3 pr-10 rounded-lg border border-border bg-background text-sm
                        text-foreground placeholder:text-muted-foreground
                        focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent"
                    />
                    <button
                      type="button"
                      onClick={() => setShowConfirmPassword(!showConfirmPassword)}
                      className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {showConfirmPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                  </div>
                  {form.confirmPassword && !passwordsMatch && (
                    <p className="text-xs text-status-error">Passwords do not match</p>
                  )}
                  {form.confirmPassword && passwordsMatch && form.password && (
                    <p className="text-xs text-status-success">Passwords match</p>
                  )}
                </div>
              </div>
            </div>
          )}

          {/* Step 3: Server URL */}
          {currentStep === 2 && (
            <div className="space-y-6 animate-fade-in">
              <div className="space-y-2">
                <h2 className="text-2xl font-semibold text-foreground tracking-tight">
                  Server URL
                </h2>
                <p className="text-sm text-muted-foreground">
                  This is the URL that cluster agents will use to reach the Astronomer server.
                  It has been auto-detected from your current browser location.
                </p>
              </div>

              <div className="space-y-4">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Server URL</label>
                  <input
                    type="url"
                    value={form.serverUrl}
                    onChange={(e) => setForm((f) => ({ ...f, serverUrl: e.target.value }))}
                    placeholder="https://astronomer.example.com"
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      text-foreground placeholder:text-muted-foreground font-mono
                      focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent"
                    autoFocus
                  />
                  <p className="text-xs text-muted-foreground">
                    Agents installed on your clusters will connect back to this address.
                    Make sure it is reachable from within your cluster networks.
                  </p>
                </div>

                <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-2">
                  <p className="text-sm font-medium text-foreground">How agents connect</p>
                  <ul className="text-xs text-muted-foreground space-y-1.5">
                    <li className="flex items-start gap-2">
                      <span className="text-status-info mt-0.5">1.</span>
                      Install the agent on your Kubernetes cluster
                    </li>
                    <li className="flex items-start gap-2">
                      <span className="text-status-info mt-0.5">2.</span>
                      The agent connects to this server URL via gRPC
                    </li>
                    <li className="flex items-start gap-2">
                      <span className="text-status-info mt-0.5">3.</span>
                      Cluster data flows securely to the Astronomer dashboard
                    </li>
                  </ul>
                </div>
              </div>
            </div>
          )}

          {/* Step 4: Preferences */}
          {currentStep === 3 && (
            <div className="space-y-6 animate-fade-in">
              <div className="space-y-2">
                <h2 className="text-2xl font-semibold text-foreground tracking-tight">
                  Preferences
                </h2>
                <p className="text-sm text-muted-foreground">
                  Configure optional platform preferences. These can be changed later in Settings.
                </p>
              </div>

              <div className="space-y-4">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Platform Name</label>
                  <input
                    type="text"
                    value={form.platformName}
                    onChange={(e) => setForm((f) => ({ ...f, platformName: e.target.value }))}
                    placeholder="Astronomer"
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      text-foreground placeholder:text-muted-foreground
                      focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent"
                    autoFocus
                  />
                  <p className="text-xs text-muted-foreground">
                    Displayed in the navigation and page titles
                  </p>
                </div>

                <div className="rounded-lg border border-border p-4">
                  <label className="flex items-start gap-3 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={form.enableTelemetry}
                      onChange={(e) => setForm((f) => ({ ...f, enableTelemetry: e.target.checked }))}
                      className="mt-0.5 rounded border-border text-primary focus:ring-ring"
                    />
                    <div>
                      <p className="text-sm font-medium text-foreground">Anonymous Telemetry</p>
                      <p className="text-xs text-muted-foreground mt-0.5">
                        Help improve Astronomer by sharing anonymous usage data.
                        No personal information or cluster data is collected.
                      </p>
                    </div>
                  </label>
                </div>
              </div>
            </div>
          )}

          {/* Step 5: Complete */}
          {currentStep === 4 && (
            <div className="space-y-6 animate-fade-in text-center">
              <div className="flex justify-center">
                <div className={cn(
                  'w-20 h-20 rounded-full flex items-center justify-center',
                  setupDone
                    ? 'bg-status-success/10'
                    : 'bg-gradient-to-br from-blue-500/10 to-violet-500/10'
                )}>
                  {setupDone ? (
                    <Check className="h-10 w-10 text-status-success" />
                  ) : (
                    <Rocket className="h-10 w-10 text-blue-500" />
                  )}
                </div>
              </div>
              <div className="space-y-3">
                <h2 className="text-2xl font-bold text-foreground tracking-tight">
                  {setupDone ? 'Setup Complete!' : 'Ready to Launch'}
                </h2>
                <p className="text-muted-foreground">
                  {setupDone
                    ? 'Your Astronomer platform is configured and ready to use. Click below to access your dashboard.'
                    : 'Review your configuration and complete the setup to get started with Astronomer.'
                  }
                </p>
              </div>

              {!setupDone && (
                <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-3 text-left">
                  <h4 className="text-sm font-medium text-foreground">Configuration Summary</h4>
                  <div className="space-y-2 text-sm">
                    <div className="flex justify-between">
                      <span className="text-muted-foreground">Admin Email</span>
                      <span className="text-foreground font-mono text-xs">{form.email}</span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-muted-foreground">Server URL</span>
                      <span className="text-foreground font-mono text-xs">{form.serverUrl}</span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-muted-foreground">Platform Name</span>
                      <span className="text-foreground">{form.platformName}</span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-muted-foreground">Telemetry</span>
                      <span className="text-foreground">{form.enableTelemetry ? 'Enabled' : 'Disabled'}</span>
                    </div>
                  </div>
                </div>
              )}

              {setupDone ? (
                <button
                  onClick={goToDashboard}
                  className="w-full inline-flex items-center justify-center gap-2 h-10 px-4 rounded-lg
                    bg-primary text-primary-foreground text-sm font-medium
                    hover:opacity-90 transition-opacity"
                >
                  Go to Dashboard
                  <ArrowRight className="h-4 w-4" />
                </button>
              ) : (
                <button
                  onClick={handleComplete}
                  disabled={loading}
                  className="w-full inline-flex items-center justify-center gap-2 h-10 px-4 rounded-lg
                    bg-primary text-primary-foreground text-sm font-medium
                    hover:opacity-90 transition-opacity disabled:opacity-50"
                >
                  {loading ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <>
                      Complete Setup
                      <Rocket className="h-4 w-4" />
                    </>
                  )}
                </button>
              )}
            </div>
          )}

          {/* Navigation */}
          {currentStep < 4 && (
            <div className="flex items-center justify-between pt-4">
              <button
                onClick={handleBack}
                disabled={currentStep === 0}
                className="inline-flex items-center gap-2 h-9 px-4 rounded-lg border border-border text-sm font-medium
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors
                  disabled:opacity-50 disabled:pointer-events-none"
              >
                <ArrowLeft className="h-4 w-4" />
                Back
              </button>
              <button
                onClick={handleNext}
                disabled={!canAdvance()}
                className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                  text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
              >
                Next
                <ArrowRight className="h-4 w-4" />
              </button>
            </div>
          )}

          {currentStep === 4 && !setupDone && (
            <div className="flex items-center justify-start pt-4">
              <button
                onClick={handleBack}
                className="inline-flex items-center gap-2 h-9 px-4 rounded-lg border border-border text-sm font-medium
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                <ArrowLeft className="h-4 w-4" />
                Back
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
