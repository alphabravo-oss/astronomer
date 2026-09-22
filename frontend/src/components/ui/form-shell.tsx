import type { FormHTMLAttributes } from "react";
import { type AnyFormApi, useStore } from "@tanstack/react-form";
import { useUnsavedGuard } from "@/lib/use-unsaved-guard";

interface FormShellProps extends FormHTMLAttributes<HTMLFormElement> {
  /**
   * A `useAppForm` instance. When given, an unsaved-changes guard blocks
   * in-app navigation and tab close while the form is dirty (skipped once
   * submitted, so a post-submit redirect never blocks).
   */
  form?: AnyFormApi;
  /** Use instead of `form` when dirtiness isn't sourced from a `useAppForm` instance. */
  isDirty?: boolean;
  unsavedMessage?: string;
}

/** Shared semantic form boundary for route-level operator workflows. */
export function FormShell({
  children,
  form,
  isDirty,
  unsavedMessage,
  ...props
}: FormShellProps) {
  return (
    <form {...props}>
      {children}
      {form ? (
        <FormGuard form={form} message={unsavedMessage} />
      ) : (
        isDirty !== undefined && (
          <PlainGuard isDirty={isDirty} message={unsavedMessage} />
        )
      )}
    </form>
  );
}

function FormGuard({ form, message }: { form: AnyFormApi; message?: string }) {
  const dirty = useStore(
    form.store,
    (state) =>
      state.isDirty && !state.isSubmitting && !state.isSubmitSuccessful,
  );
  const { dialog } = useUnsavedGuard(dirty, message);
  return dialog;
}

function PlainGuard({
  isDirty,
  message,
}: {
  isDirty: boolean;
  message?: string;
}) {
  const { dialog } = useUnsavedGuard(isDirty, message);
  return dialog;
}
