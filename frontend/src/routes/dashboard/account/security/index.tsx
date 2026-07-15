import { createFileRoute } from '@tanstack/react-router';

/**
 * Account → Security page. Houses the TOTP enrollment / disable / recovery-codes
 * flow for the logged-in user.
 *
 * The flow has three steady states and a multi-step wizard for enrollment:
 *  - Not enrolled  → "Enable 2FA" launches the 3-step wizard.
 *  - Enrolled      → status banner + "Disable 2FA" (password + current code).
 *  - Wizard step 3 → recovery codes shown once; user must check "I've saved
 *                    these" before leaving the page (codes won't be re-shown).
 *
 * Backend endpoints are documented in lib/api/account-security.ts; this page
 * deliberately makes no decisions about TOTP secrets (the QR + otpauth URL
 * come down pre-rendered).
 */

import { useEffect, useMemo, useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import {
  Shield,
  ShieldCheck,
  ShieldOff,
  Loader2,
  Eye,
  EyeOff,
  Copy,
  Download,
  RefreshCw,
  Check,
  AlertTriangle,
} from 'lucide-react';
import { formatRelativeTime, cn } from '@/lib/utils';
import { ModalShell } from '@/components/ui/modal-shell';
import {
  getTotpStatus,
  startTotpEnrollment,
  confirmTotpEnrollment,
  disableTotp,
  regenerateRecoveryCodes,
  type TotpStatus,
  type TotpEnrollStart,
} from '@/lib/api/account-security';

const TOTP_STATUS_KEY = ['account', 'security', 'totp', 'status'] as const;

function AccountSecurityPage() {
  const qc = useQueryClient();
  const { data: status, isLoading } = useQuery<TotpStatus>({
    queryKey: TOTP_STATUS_KEY,
    queryFn: getTotpStatus,
  });

  const [wizardOpen, setWizardOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  const [regenOpen, setRegenOpen] = useState(false);

  const refresh = () => qc.invalidateQueries({ queryKey: TOTP_STATUS_KEY });

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Security</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Two-factor authentication and recovery codes for your account.
        </p>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center h-32">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : status?.enrolled ? (
        <EnrolledCard
          status={status}
          onDisable={() => setDisableOpen(true)}
          onRegenerate={() => setRegenOpen(true)}
        />
      ) : (
        <NotEnrolledCard onEnable={() => setWizardOpen(true)} />
      )}

      {wizardOpen && (
        <EnrollmentWizard
          onClose={() => setWizardOpen(false)}
          onDone={() => {
            setWizardOpen(false);
            refresh();
          }}
        />
      )}

      {disableOpen && status?.enrolled && (
        <DisableDialog
          onClose={() => setDisableOpen(false)}
          onDone={() => {
            setDisableOpen(false);
            refresh();
          }}
        />
      )}

      {regenOpen && status?.enrolled && (
        <RegenerateDialog
          onClose={() => setRegenOpen(false)}
          onDone={() => {
            setRegenOpen(false);
            refresh();
          }}
        />
      )}
    </div>
  );
}

// ------------------------------------------------------------------
// Steady-state cards
// ------------------------------------------------------------------

function NotEnrolledCard({ onEnable }: { onEnable: () => void }) {
  return (
    <div className="rounded-lg border border-border bg-card p-6">
      <div className="flex items-start gap-4">
        <div className="flex-shrink-0 h-10 w-10 rounded-full bg-status-warning/10 flex items-center justify-center">
          <Shield className="h-5 w-5 text-status-warning" />
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-base font-semibold text-foreground">
            Two-factor authentication is off
          </h2>
          <p className="text-sm text-muted-foreground mt-1">
            Add a one-time-code authenticator app to protect your account from password leaks.
          </p>
          <button
            onClick={onEnable}
            className="mt-4 inline-flex items-center gap-2 h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
          >
            <ShieldCheck className="h-4 w-4" />
            Enable 2FA
          </button>
        </div>
      </div>
    </div>
  );
}

function EnrolledCard({
  status,
  onDisable,
  onRegenerate,
}: {
  status: TotpStatus;
  onDisable: () => void;
  onRegenerate: () => void;
}) {
  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-border bg-card p-6">
        <div className="flex items-start gap-4">
          <div className="flex-shrink-0 h-10 w-10 rounded-full bg-status-success/10 flex items-center justify-center">
            <ShieldCheck className="h-5 w-5 text-status-success" />
          </div>
          <div className="flex-1 min-w-0">
            <h2 className="text-base font-semibold text-foreground">
              Two-factor authentication is on
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              {status.lastUsedAt
                ? `Last used ${formatRelativeTime(status.lastUsedAt)}.`
                : 'Not used yet.'}
            </p>
            <button
              onClick={onDisable}
              className="mt-4 inline-flex items-center gap-2 h-9 px-4 rounded-md border border-border text-sm font-medium text-foreground hover:bg-accent transition-colors"
            >
              <ShieldOff className="h-4 w-4" />
              Disable 2FA
            </button>
          </div>
        </div>
      </div>

      <div className="rounded-lg border border-border bg-card p-6">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h3 className="text-base font-semibold text-foreground">Recovery codes</h3>
            <p className="text-sm text-muted-foreground mt-1">
              {status.recoveryCodesRemaining} of 10 remaining. Use them if you lose access to your
              authenticator.
            </p>
          </div>
          <button
            onClick={onRegenerate}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-md border border-border text-sm font-medium text-foreground hover:bg-accent transition-colors flex-shrink-0"
          >
            <RefreshCw className="h-4 w-4" />
            Regenerate
          </button>
        </div>
      </div>
    </div>
  );
}

// ------------------------------------------------------------------
// Enrollment wizard
// ------------------------------------------------------------------

type WizardStep = 'scan' | 'verify' | 'codes';

function EnrollmentWizard({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [step, setStep] = useState<WizardStep>('scan');
  const [enrollment, setEnrollment] = useState<TotpEnrollStart | null>(null);
  const [code, setCode] = useState('');
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);

  const startMut = useMutation({
    mutationFn: startTotpEnrollment,
    onSuccess: (data) => setEnrollment(data),
    onError: (err: Error) => toastApiError('', err, 'Failed to start enrollment'),
  });

  const confirmMut = useMutation({
    mutationFn: async () => {
      if (!enrollment) throw new Error('No enrollment session');
      return confirmTotpEnrollment(enrollment.sessionToken, code);
    },
    onSuccess: (data) => {
      setRecoveryCodes(data.recoveryCodes);
      setStep('codes');
    },
    onError: (err: Error) => toastApiError('', err, 'Invalid code'),
  });

  useEffect(() => {
    startMut.mutate();
    // start once on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <ModalShell onClose={onClose} title="Enable two-factor authentication">
      {/* Step indicator */}
      <div className="flex items-center gap-2 text-xs">
        {(['scan', 'verify', 'codes'] as WizardStep[]).map((s, i) => (
          <div key={s} className="flex items-center gap-2">
            <div
              className={cn(
                'h-6 w-6 rounded-full flex items-center justify-center text-xs font-medium',
                step === s
                  ? 'bg-primary text-primary-foreground'
                  : (['scan', 'verify', 'codes'].indexOf(step) > i)
                    ? 'bg-status-success/20 text-status-success'
                    : 'bg-muted text-muted-foreground'
              )}
            >
              {(['scan', 'verify', 'codes'].indexOf(step) > i) ? <Check className="h-3 w-3" /> : i + 1}
            </div>
            {i < 2 && <div className="h-px w-8 bg-border" />}
          </div>
        ))}
      </div>

      {step === 'scan' && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Scan this QR code with your authenticator app (Google Authenticator, 1Password, Authy, …).
            If you can&apos;t scan, type the secret in manually.
          </p>
          {startMut.isPending || !enrollment ? (
            <div className="flex items-center justify-center h-48">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <div className="flex flex-col items-center gap-3">
              <div className="rounded-lg bg-white p-2 border border-border">
                <img
                  src={`data:image/png;base64,${enrollment.qrPngBase64}`}
                  alt="TOTP QR code"
                  className="block [image-rendering:pixelated]"
                  width={256}
                  height={256}
                />
              </div>
              <details className="w-full">
                <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground">
                  Can&apos;t scan? Show setup URL
                </summary>
                <div className="mt-2 flex items-stretch gap-2">
                  <code className="flex-1 min-w-0 px-2 py-1.5 rounded bg-muted text-xs font-mono text-foreground overflow-x-auto whitespace-nowrap">
                    {enrollment.otpauthUrl}
                  </code>
                  <button
                    onClick={() => {
                      navigator.clipboard.writeText(enrollment.otpauthUrl);
                      toastSuccess('Copied');
                    }}
                    className="inline-flex items-center justify-center h-8 w-8 rounded border border-border text-muted-foreground hover:text-foreground hover:bg-accent"
                    title="Copy"
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </button>
                </div>
              </details>
            </div>
          )}
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
            <button
              onClick={onClose}
              className="inline-flex items-center h-9 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent"
            >
              Cancel
            </button>
            <button
              onClick={() => setStep('verify')}
              disabled={!enrollment}
              className="inline-flex items-center h-9 px-4 rounded bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              I&apos;ve added it
            </button>
          </div>
        </div>
      )}

      {step === 'verify' && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Enter the 6-digit code your authenticator app is showing right now.
          </p>
          <CodeInput value={code} onChange={setCode} autoFocus />
          <div className="flex items-center justify-between gap-2 pt-2 border-t border-border">
            <button
              onClick={() => setStep('scan')}
              className="inline-flex items-center h-9 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent"
            >
              Back
            </button>
            <div className="flex items-center gap-2">
              <button
                onClick={onClose}
                className="inline-flex items-center h-9 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent"
              >
                Cancel
              </button>
              <button
                onClick={() => confirmMut.mutate()}
                disabled={code.length !== 6 || confirmMut.isPending}
                className="inline-flex items-center gap-2 h-9 px-4 rounded bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
              >
                {confirmMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                Verify and continue
              </button>
            </div>
          </div>
        </div>
      )}

      {step === 'codes' && recoveryCodes && (
        <RecoveryCodesBlock
          codes={recoveryCodes}
          acknowledged={acknowledged}
          onAcknowledge={setAcknowledged}
          onFinish={onDone}
        />
      )}
    </ModalShell>
  );
}

// ------------------------------------------------------------------
// Disable / Regenerate dialogs
// ------------------------------------------------------------------

function DisableDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [showPassword, setShowPassword] = useState(false);

  const mut = useMutation({
    mutationFn: () => disableTotp(password, code),
    onSuccess: () => {
      toastSuccess('Two-factor authentication disabled');
      onDone();
    },
    onError: (err: Error) => toastApiError('', err, 'Could not disable 2FA'),
  });

  return (
    <ModalShell onClose={onClose} title="Disable two-factor authentication">
      <div className="space-y-4">
        <div className="flex items-start gap-3 p-3 rounded-md bg-status-warning/10 border border-status-warning/30">
          <AlertTriangle className="h-4 w-4 text-status-warning flex-shrink-0 mt-0.5" />
          <p className="text-xs text-status-warning">
            Disabling 2FA removes a layer of protection from your account. You&apos;ll need to enter
            your password and a current 6-digit code to confirm.
          </p>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Password</label>
          <div className="relative">
            <input
              type={showPassword ? 'text' : 'password'}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full h-10 px-3 pr-10 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
              autoComplete="current-password"
            />
            <button
              type="button"
              onClick={() => setShowPassword((v) => !v)}
              className="absolute right-2 top-1/2 -translate-y-1/2 p-1 text-muted-foreground hover:text-foreground"
              tabIndex={-1}
              aria-label={showPassword ? 'Hide password' : 'Show password'}
            >
              {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </button>
          </div>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Current 6-digit code</label>
          <CodeInput value={code} onChange={setCode} />
        </div>
        <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
          <button
            onClick={onClose}
            className="inline-flex items-center h-9 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent"
          >
            Cancel
          </button>
          <button
            onClick={() => mut.mutate()}
            disabled={!password || code.length !== 6 || mut.isPending}
            className="inline-flex items-center gap-2 h-9 px-4 rounded bg-status-error text-white text-sm font-medium hover:bg-status-error/90 disabled:opacity-50"
          >
            {mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Disable 2FA
          </button>
        </div>
      </div>
    </ModalShell>
  );
}

function RegenerateDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [code, setCode] = useState('');
  const [acknowledged, setAcknowledged] = useState(false);
  const [codes, setCodes] = useState<string[] | null>(null);

  const mut = useMutation({
    mutationFn: () => regenerateRecoveryCodes(code),
    onSuccess: (data) => setCodes(data.recoveryCodes),
    onError: (err: Error) => toastApiError('', err, 'Could not regenerate codes'),
  });

  return (
    <ModalShell onClose={onClose} title="Regenerate recovery codes">
      {codes ? (
        <RecoveryCodesBlock
          codes={codes}
          acknowledged={acknowledged}
          onAcknowledge={setAcknowledged}
          onFinish={onDone}
        />
      ) : (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Generating new codes invalidates any previous codes. Enter a current 6-digit code to
            confirm.
          </p>
          <CodeInput value={code} onChange={setCode} autoFocus />
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
            <button
              onClick={onClose}
              className="inline-flex items-center h-9 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent"
            >
              Cancel
            </button>
            <button
              onClick={() => mut.mutate()}
              disabled={code.length !== 6 || mut.isPending}
              className="inline-flex items-center gap-2 h-9 px-4 rounded bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              {mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Generate new codes
            </button>
          </div>
        </div>
      )}
    </ModalShell>
  );
}

// ------------------------------------------------------------------
// Shared bits
// ------------------------------------------------------------------

function RecoveryCodesBlock({
  codes,
  acknowledged,
  onAcknowledge,
  onFinish,
}: {
  codes: string[];
  acknowledged: boolean;
  onAcknowledge: (v: boolean) => void;
  onFinish: () => void;
}) {
  const codesText = useMemo(() => codes.join('\n'), [codes]);

  const download = () => {
    const blob = new Blob([codesText + '\n'], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'astronomer-recovery-codes.txt';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const copy = () => {
    navigator.clipboard.writeText(codesText);
    toastSuccess('Recovery codes copied to clipboard');
  };

  return (
    <div className="space-y-4">
      <div className="flex items-start gap-3 p-3 rounded-md bg-status-warning/10 border border-status-warning/30">
        <AlertTriangle className="h-4 w-4 text-status-warning flex-shrink-0 mt-0.5" />
        <p className="text-xs text-status-warning">
          Save these 10 single-use recovery codes somewhere safe. They will <strong>not</strong> be
          shown again. Each one lets you log in once without your authenticator.
        </p>
      </div>
      <pre className="rounded-md border border-border bg-muted/40 p-4 text-sm font-mono text-foreground grid grid-cols-2 gap-x-6 gap-y-1 leading-6">
        {codes.map((c) => (
          <span key={c}>{c}</span>
        ))}
      </pre>
      <div className="flex items-center gap-2">
        <button
          onClick={copy}
          className="inline-flex items-center gap-2 h-9 px-3 rounded border border-border text-sm font-medium text-foreground hover:bg-accent"
        >
          <Copy className="h-4 w-4" />
          Copy
        </button>
        <button
          onClick={download}
          className="inline-flex items-center gap-2 h-9 px-3 rounded border border-border text-sm font-medium text-foreground hover:bg-accent"
        >
          <Download className="h-4 w-4" />
          Download as text
        </button>
      </div>
      <label className="flex items-center gap-2 text-sm text-foreground">
        <input
          type="checkbox"
          checked={acknowledged}
          onChange={(e) => onAcknowledge(e.target.checked)}
          className="rounded border-border"
        />
        I&apos;ve saved my recovery codes somewhere safe
      </label>
      <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
        <button
          onClick={onFinish}
          disabled={!acknowledged}
          className="inline-flex items-center h-9 px-4 rounded bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          Done
        </button>
      </div>
    </div>
  );
}

// Not exported: route files should only export the Route so autoCodeSplitting
// keeps the whole page body in the lazy chunk (no external consumers exist).
function CodeInput({
  value,
  onChange,
  autoFocus,
}: {
  value: string;
  onChange: (v: string) => void;
  autoFocus?: boolean;
}) {
  // Single 6-char numeric input: simpler than 6 separate boxes, paste works
  // out of the box, and it still feels good with `inputMode=numeric`.
  return (
    <input
      type="text"
      inputMode="numeric"
      pattern="[0-9]*"
      maxLength={6}
      value={value}
      autoFocus={autoFocus}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, '').slice(0, 6))}
      placeholder="123 456"
      className="w-full h-12 px-3 rounded-md border border-border bg-background text-center text-2xl font-mono tracking-[0.4em] text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
      autoComplete="one-time-code"
    />
  );
}

export const Route = createFileRoute('/dashboard/account/security/')({
  component: AccountSecurityPage,
});
