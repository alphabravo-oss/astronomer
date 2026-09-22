import { createFileRoute } from "@tanstack/react-router";
import { FormShell } from "@/components/ui/form-shell";

/**
 * Forgot-password — collects an email address, posts to
 * /auth/password-reset/request and always shows the same "if it exists, a
 * link has been sent" success screen. The backend returns 202 unconditionally
 * (no user enumeration), so the UI mirrors that.
 */

import { useState } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import { Orbit, ArrowLeft, Check } from "lucide-react";
import { useAppForm, useStore } from "@/lib/form";
import { requestPasswordReset } from "@/lib/api/account-security";
import { ActionButton } from "@/components/ui/action-button";

function ForgotPasswordPage() {
  const [submitted, setSubmitted] = useState(false);
  const [submissionError, setSubmissionError] = useState<string | null>(null);
  const form = useAppForm({
    defaultValues: { email: "" },
    validators: {
      onSubmit: ({ value }) =>
        !value.email.trim() ? "Enter an email address." : undefined,
    },
    onSubmit: async ({ value }) => {
      setSubmissionError(null);
      try {
        await requestPasswordReset(value.email.trim());
        setSubmitted(true);
      } catch (error) {
        setSubmissionError(
          error instanceof Error
            ? error.message
            : "Could not send reset email.",
        );
      }
    },
  });
  const loading = useStore(form.store, (state) => state.isSubmitting);

  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-6">
      <div className="w-full max-w-md space-y-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <Orbit className="h-8 w-8 text-foreground" />
          {/* eslint-disable-line no-restricted-syntax -- standalone auth screen, not dashboard chrome */}<h1 className="text-xl font-semibold tracking-tight">
            {submitted ? "Check your inbox" : "Reset your password"}
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
              The link expires in 30 minutes. If you don&apos;t see it, check
              your spam folder or contact your administrator.
            </p>
            <RouterLink
              to="/auth/login"
              className="inline-flex items-center gap-1 text-sm text-foreground hover:underline"
            >
              <ArrowLeft className="h-4 w-4" /> Back to sign in
            </RouterLink>
          </div>
        ) : (
          <FormShell
            form={form}
            onSubmit={(event) => {
              event.preventDefault();
              void form.handleSubmit();
            }}
            className="space-y-4 rounded-lg border border-border bg-card p-6 shadow-xs"
          >
            <form.AppForm>
              <form.FormErrorSummary serverError={submissionError} />
            </form.AppForm>
            <form.AppField name="email">
              {(field) => (
                <field.TextField
                  label="Email address"
                  type="email"
                  autoComplete="email"
                  placeholder="you@example.com"
                  required
                />
              )}
            </form.AppField>
            <ActionButton
              type="submit"
              intent="primary"
              className="w-full"
              disabled={loading}
              loading={loading}
              loadingLabel="Send reset link"
            >
              Send reset link
            </ActionButton>
            <div className="text-center">
              <RouterLink
                to="/auth/login"
                className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
              >
                <ArrowLeft className="h-3 w-3" /> Back to sign in
              </RouterLink>
            </div>
          </FormShell>
        )}
      </div>
    </div>
  );
}

export const Route = createFileRoute("/auth/login/forgot-password/")({
  component: ForgotPasswordPage,
});
