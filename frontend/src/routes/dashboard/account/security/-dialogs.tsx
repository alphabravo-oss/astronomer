import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { AlertTriangle, Eye, EyeOff } from "lucide-react";
import { useAppForm, useStore } from "@/lib/form";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import {
  disableTotp,
  regenerateRecoveryCodes,
} from "@/lib/api/account-security";
import { CodeInput, RecoveryCodesBlock } from "./-shared";

export function DisableDialog({
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

export function RegenerateDialog({
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
