import { useMutation } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { Copy, Check } from "lucide-react";
import { cn } from "@/lib/utils";
import { useAppForm, useStore } from "@/lib/form";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { confirmTotpEnrollment } from "@/lib/api/account-security";
import { LoadingSkeleton } from "@/components/form/loading-skeleton";
import { CodeInput, RecoveryCodesBlock } from "./-shared";
import { useEnrollmentWizard, type WizardStep } from "./-use-enrollment-wizard";

export function EnrollmentWizard({
  onClose,
  onDone,
}: {
  onClose: () => void;
  onDone: () => void;
}) {
  const {
    step,
    setStep,
    enrollment,
    recoveryCodes,
    setRecoveryCodes,
    acknowledged,
    setAcknowledged,
    startMut,
  } = useEnrollmentWizard();

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
            <LoadingSkeleton label="Loading" heading />
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
                    tooltip="Copy"
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
