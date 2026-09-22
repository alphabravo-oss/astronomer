import { Input } from "@/components/ui/input";
import { createFileRoute } from "@tanstack/react-router";

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

import { useEffect, useMemo, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
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
} from "lucide-react";
import { formatRelativeTime, cn, downloadBlob } from "@/lib/utils";
import { useAppForm, useStore } from "@/lib/form";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import {
  getTotpStatus,
  startTotpEnrollment,
  confirmTotpEnrollment,
  disableTotp,
  regenerateRecoveryCodes,
  type TotpStatus,
  type TotpEnrollStart,
} from "@/lib/api/account-security";

const TOTP_STATUS_KEY = ["account", "security", "totp", "status"] as const;

function AccountSecurityPage() {
  const qc = useQueryClient();
  const statusQuery = useQuery<TotpStatus>({
    queryKey: TOTP_STATUS_KEY,
    queryFn: getTotpStatus,
  });

  const [wizardOpen, setWizardOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  const [regenOpen, setRegenOpen] = useState(false);
  const status = statusQuery.data;

  const refresh = () => qc.invalidateQueries({ queryKey: TOTP_STATUS_KEY });

  return (
    <PageShell>
      <PageHeader
        title="Security"
        description="Two-factor authentication and recovery codes for your account."
      />

      <QueryStates query={statusQuery} permission="account:read">
        {(loadedStatus) => loadedStatus.enrolled ? (
          <EnrolledCard
            status={loadedStatus}
            onDisable={() => setDisableOpen(true)}
            onRegenerate={() => setRegenOpen(true)}
          />
        ) : (
          <NotEnrolledCard onEnable={() => setWizardOpen(true)} />
        )}
      </QueryStates>

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
    </PageShell>
  );
}

// ------------------------------------------------------------------
// Steady-state cards
// ------------------------------------------------------------------

function NotEnrolledCard({ onEnable }: { onEnable: () => void }) {
  return (
    <Card padding="lg">
      <div className="flex items-start gap-4">
        <div className="shrink-0 h-10 w-10 rounded-full bg-status-warning/10 flex items-center justify-center">
          <Shield className="h-5 w-5 text-status-warning" />
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-base font-semibold text-foreground">
            Two-factor authentication is off
          </h2>
          <p className="text-sm text-muted-foreground mt-1">
            Add a one-time-code authenticator app to protect your account from
            password leaks.
          </p>
          <ActionButton
            onClick={onEnable}
            intent="primary"
            icon={<ShieldCheck className="h-4 w-4" />}
            className="mt-4"
          >
            Enable 2FA
          </ActionButton>
        </div>
      </div>
    </Card>
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
      <Card padding="lg">
        <div className="flex items-start gap-4">
          <div className="shrink-0 h-10 w-10 rounded-full bg-status-success/10 flex items-center justify-center">
            <ShieldCheck className="h-5 w-5 text-status-success" />
          </div>
          <div className="flex-1 min-w-0">
            <h2 className="text-base font-semibold text-foreground">
              Two-factor authentication is on
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              {status.lastUsedAt
                ? `Last used ${formatRelativeTime(status.lastUsedAt)}.`
                : "Not used yet."}
            </p>
            <ActionButton
              onClick={onDisable}
              icon={<ShieldOff className="h-4 w-4" />}
              className="mt-4"
            >
              Disable 2FA
            </ActionButton>
          </div>
        </div>
      </Card>

      <Card padding="lg">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h3 className="text-base font-semibold text-foreground">
              Recovery codes
            </h3>
            <p className="text-sm text-muted-foreground mt-1">
              {status.recoveryCodesRemaining} of 10 remaining. Use them if you
              lose access to your authenticator.
            </p>
          </div>
          <ActionButton
            onClick={onRegenerate}
            icon={<RefreshCw className="h-4 w-4" />}
            className="shrink-0"
          >
            Regenerate
          </ActionButton>
        </div>
      </Card>
    </div>
  );
}

// ------------------------------------------------------------------
// Enrollment wizard
// ------------------------------------------------------------------

type WizardStep = "scan" | "verify" | "codes";

function EnrollmentWizard({
  onClose,
  onDone,
}: {
  onClose: () => void;
  onDone: () => void;
}) {
  const [step, setStep] = useState<WizardStep>("scan");
  const [enrollment, setEnrollment] = useState<TotpEnrollStart | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);

  const startMut = useMutation({
    mutationFn: () => startTotpEnrollment(),
    onSuccess: (data) => setEnrollment(data),
    onError: (err: Error) =>
      toastApiError("", err, "Failed to start enrollment"),
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
        {(["scan", "verify", "codes"] as WizardStep[]).map((s, i) => (
          <div key={s} className="flex items-center gap-2">
            <div
              className={cn(
                "h-6 w-6 rounded-full flex items-center justify-center text-xs font-medium",
                step === s
                  ? "bg-primary text-primary-foreground"
                  : ["scan", "verify", "codes"].indexOf(step) > i
                    ? "bg-status-success/20 text-status-success"
                    : "bg-muted text-muted-foreground",
              )}
            >
              {["scan", "verify", "codes"].indexOf(step) > i ? (
                <Check className="h-3 w-3" />
              ) : (
                i + 1
              )}
            </div>
            {i < 2 && <div className="h-px w-8 bg-border" />}
          </div>
        ))}
      </div>

      {step === "scan" && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Scan this QR code with your authenticator app (Google Authenticator,
            1Password, Authy, …). If you can&apos;t scan, type the secret in
            manually.
          </p>
          {startMut.isPending || !enrollment ? (
            <div className="flex items-center justify-center h-48">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <div className="flex flex-col items-center gap-3">
              <div className="rounded-lg bg-white p-2 border border-border">
                <img
                  src={enrollment.qrDataUrl}
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
                  <code className="flex-1 min-w-0 px-2 py-1.5 rounded-sm bg-muted text-xs font-mono text-foreground overflow-x-auto whitespace-nowrap">
                    {enrollment.otpauthUrl}
                  </code>
                  <ActionButton
                    onClick={() => {
                      navigator.clipboard.writeText(enrollment.otpauthUrl);
                      toastSuccess("Copied");
                    }}
                    size="icon"
                    icon={<Copy className="h-3.5 w-3.5" />}
                    title="Copy"
                    aria-label="Copy"
                  />
                </div>
              </details>
            </div>
          )}
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
            <ActionButton onClick={onClose} intent="ghost">
              Cancel
            </ActionButton>
            <ActionButton
              onClick={() => setStep("verify")}
              disabled={!enrollment}
              intent="primary"
            >
              I&apos;ve added it
            </ActionButton>
          </div>
        </div>
      )}

      {step === "verify" && enrollment && (
        <VerifyStepForm
          sessionToken={enrollment.sessionToken}
          challenge={enrollment.challenge}
          onBack={() => setStep("scan")}
          onCancel={onClose}
          onVerified={(codes) => {
            setRecoveryCodes(codes);
            setStep("codes");
          }}
        />
      )}

      {step === "codes" && recoveryCodes && (
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

/** Wizard step 2 — its own small form (one per step, not a mega-form). */
function VerifyStepForm({
  sessionToken,
  challenge,
  onBack,
  onCancel,
  onVerified,
}: {
  sessionToken: string;
  challenge: string;
  onBack: () => void;
  onCancel: () => void;
  onVerified: (recoveryCodes: string[]) => void;
}) {
  const confirmMut = useMutation({
    mutationFn: (code: string) =>
      confirmTotpEnrollment(sessionToken, challenge, code),
    onSuccess: (data) => onVerified(data.recoveryCodes),
    onError: (err: Error) => toastApiError("", err, "Invalid code"),
  });

  const form = useAppForm({
    defaultValues: { code: "" },
    validators: {
      // Old check (disabled-button gate): a full 6-digit code — ported 1:1.
      onSubmit: ({ value }) =>
        value.code.length !== 6 ? "Enter the 6-digit code" : undefined,
    },
    onSubmit: async ({ value }) => {
      try {
        await confirmMut.mutateAsync(value.code);
      } catch {
        // Mutation toasts on error.
      }
    },
  });
  const code = useStore(form.store, (state) => state.values.code);

  return (
    <div className="space-y-4">
      <form.AppForm>
        <form.FormErrorSummary serverError={confirmMut.error?.message} />
      </form.AppForm>
      <p className="text-sm text-muted-foreground">
        Enter the 6-digit code your authenticator app is showing right now.
      </p>
      <form.Field name="code">
        {(field) => (
          <CodeInput
            value={field.state.value}
            onChange={field.handleChange}
            data-initial-focus
          />
        )}
      </form.Field>
      <div className="flex items-center justify-between gap-2 pt-2 border-t border-border">
        <ActionButton onClick={onBack} intent="ghost">
          Back
        </ActionButton>
        <div className="flex items-center gap-2">
          <ActionButton onClick={onCancel} intent="ghost">
            Cancel
          </ActionButton>
          <ActionButton
            onClick={() => void form.handleSubmit()}
            disabled={code.length !== 6 || confirmMut.isPending}
            loading={confirmMut.isPending}
            intent="primary"
          >
            Verify and continue
          </ActionButton>
        </div>
      </div>
    </div>
  );
}

// ------------------------------------------------------------------
// Disable / Regenerate dialogs
// ------------------------------------------------------------------

function DisableDialog({
  onClose,
  onDone,
}: {
  onClose: () => void;
  onDone: () => void;
}) {
  const [showPassword, setShowPassword] = useState(false);

  const mut = useMutation({
    mutationFn: (value: { password: string; code: string }) =>
      disableTotp(value.password, value.code),
    onSuccess: () => {
      toastSuccess("Two-factor authentication disabled");
      onDone();
    },
    onError: (err: Error) => toastApiError("", err, "Could not disable 2FA"),
  });

  const form = useAppForm({
    defaultValues: { password: "", code: "" },
    validators: {
      // Old check (disabled-button gate): password present + full 6-digit
      // code — ported 1:1.
      onSubmit: ({ value }) =>
        !value.password || value.code.length !== 6
          ? "Enter your password and a current code"
          : undefined,
    },
    onSubmit: async ({ value }) => {
      try {
        await mut.mutateAsync(value);
      } catch {
        // Mutation toasts on error.
      }
    },
  });
  const { password, code } = useStore(form.store, (state) => state.values);

  return (
    <ModalShell onClose={onClose} title="Disable two-factor authentication">
      <form.AppForm>
        <form.FormErrorSummary serverError={mut.error?.message} />
      </form.AppForm>
      <div className="space-y-4">
        <div className="flex items-start gap-3 p-3 rounded-md bg-status-warning/10 border border-status-warning/30">
          <AlertTriangle className="h-4 w-4 text-status-warning shrink-0 mt-0.5" />
          <p className="text-xs text-status-warning">
            Disabling 2FA removes a layer of protection from your account.
            You&apos;ll need to enter your password and a current 6-digit code
            to confirm.
          </p>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-25a26509-455"
          >
            Password
          </label>
          <form.Field name="password">
            {(field) => (
              <div className="relative">
                <Input
                  name={field.name}
                  id="field-25a26509-455"
                  type={showPassword ? "text" : "password"}
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  className="w-full h-10 px-3 pr-10 rounded-md border border-border bg-background text-sm focus:outline-hidden focus:ring-2 focus:ring-ring"
                  autoComplete="current-password"
                />
                <ActionButton
                  onClick={() => setShowPassword((v) => !v)}
                  intent="ghost"
                  size="icon"
                  className="absolute right-2 top-1/2 h-auto w-auto -translate-y-1/2 p-1"
                  tabIndex={-1}
                  aria-label={showPassword ? "Hide password" : "Show password"}
                  icon={
                    showPassword ? (
                      <EyeOff className="h-4 w-4" />
                    ) : (
                      <Eye className="h-4 w-4" />
                    )
                  }
                />
              </div>
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            htmlFor="totp-current-code"
            className="text-sm font-medium text-foreground"
          >
            Current 6-digit code
          </label>
          <form.Field name="code">
            {(field) => (
              <CodeInput
                id="totp-current-code"
                value={field.state.value}
                onChange={field.handleChange}
              />
            )}
          </form.Field>
        </div>
        <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
          <ActionButton onClick={onClose} intent="ghost">
            Cancel
          </ActionButton>
          <ActionButton
            onClick={() => void form.handleSubmit()}
            disabled={!password || code.length !== 6 || mut.isPending}
            loading={mut.isPending}
            intent="destructive"
          >
            Disable 2FA
          </ActionButton>
        </div>
      </div>
    </ModalShell>
  );
}

function RegenerateDialog({
  onClose,
  onDone,
}: {
  onClose: () => void;
  onDone: () => void;
}) {
  const [acknowledged, setAcknowledged] = useState(false);
  const [codes, setCodes] = useState<string[] | null>(null);

  const mut = useMutation({
    mutationFn: (code: string) => regenerateRecoveryCodes(code),
    onSuccess: (data) => setCodes(data.recoveryCodes),
    onError: (err: Error) =>
      toastApiError("", err, "Could not regenerate codes"),
  });

  const form = useAppForm({
    defaultValues: { code: "" },
    validators: {
      // Old check (disabled-button gate): a full 6-digit code — ported 1:1.
      onSubmit: ({ value }) =>
        value.code.length !== 6 ? "Enter the 6-digit code" : undefined,
    },
    onSubmit: async ({ value }) => {
      try {
        await mut.mutateAsync(value.code);
      } catch {
        // Mutation toasts on error.
      }
    },
  });
  const code = useStore(form.store, (state) => state.values.code);

  return (
    <ModalShell onClose={onClose} title="Regenerate recovery codes">
      <form.AppForm>
        <form.FormErrorSummary serverError={mut.error?.message} />
      </form.AppForm>
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
            Generating new codes invalidates any previous codes. Enter a current
            6-digit code to confirm.
          </p>
          <form.Field name="code">
            {(field) => (
              <CodeInput
                value={field.state.value}
                onChange={field.handleChange}
                data-initial-focus
              />
            )}
          </form.Field>
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
            <ActionButton onClick={onClose} intent="ghost">
              Cancel
            </ActionButton>
            <ActionButton
              onClick={() => void form.handleSubmit()}
              disabled={code.length !== 6 || mut.isPending}
              loading={mut.isPending}
              intent="primary"
            >
              Generate new codes
            </ActionButton>
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
  const codesText = useMemo(() => codes.join("\n"), [codes]);

  const download = () => {
    downloadBlob(
      codesText + "\n",
      "astronomer-recovery-codes.txt",
      "text/plain",
    );
  };

  const copy = () => {
    navigator.clipboard.writeText(codesText);
    toastSuccess("Recovery codes copied to clipboard");
  };

  return (
    <div className="space-y-4">
      <div className="flex items-start gap-3 p-3 rounded-md bg-status-warning/10 border border-status-warning/30">
        <AlertTriangle className="h-4 w-4 text-status-warning shrink-0 mt-0.5" />
        <p className="text-xs text-status-warning">
          Save these 10 single-use recovery codes somewhere safe. They will{" "}
          <strong>not</strong> be shown again. Each one lets you log in once
          without your authenticator.
        </p>
      </div>
      <pre className="rounded-md border border-border bg-muted/40 p-4 text-sm font-mono text-foreground grid grid-cols-2 gap-x-6 gap-y-1 leading-6">
        {codes.map((c) => (
          <span key={c}>{c}</span>
        ))}
      </pre>
      <div className="flex items-center gap-2">
        <ActionButton onClick={copy} icon={<Copy className="h-4 w-4" />}>
          Copy
        </ActionButton>
        <ActionButton onClick={download} icon={<Download className="h-4 w-4" />}>
          Download as text
        </ActionButton>
      </div>
      <label className="flex items-center gap-2 text-sm text-foreground">
        <Input
          type="checkbox"
          checked={acknowledged}
          onChange={(e) => onAcknowledge(e.target.checked)}
          className="rounded-sm border-border"
        />
        I&apos;ve saved my recovery codes somewhere safe
      </label>
      <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
        <ActionButton onClick={onFinish} disabled={!acknowledged} intent="primary">
          Done
        </ActionButton>
      </div>
    </div>
  );
}

// Not exported: route files should only export the Route so autoCodeSplitting
// keeps the whole page body in the lazy chunk (no external consumers exist).
function CodeInput({
  id,
  value,
  onChange,
  autoFocus,
}: {
  id?: string;
  value: string;
  onChange: (v: string) => void;
  autoFocus?: boolean;
}) {
  // Single 6-char numeric input: simpler than 6 separate boxes, paste works
  // out of the box, and it still feels good with `inputMode=numeric`.
  return (
    <Input
      id={id}
      type="text"
      inputMode="numeric"
      pattern="[0-9]*"
      maxLength={6}
      value={value}
      data-initial-focus={autoFocus}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, "").slice(0, 6))}
      placeholder="123 456"
      className="w-full h-12 px-3 rounded-md border border-border bg-background text-center text-2xl font-mono tracking-[0.4em] text-foreground focus:outline-hidden focus:ring-2 focus:ring-ring"
      autoComplete="one-time-code"
    />
  );
}

export const Route = createFileRoute("/dashboard/account/security/")({
  component: AccountSecurityPage,
});
