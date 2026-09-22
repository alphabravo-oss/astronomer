import { createElement, type ReactNode } from "react";
import { useBlocker } from "@tanstack/react-router";
import {
  ConfirmDialog,
  type DestructiveImpactPreview,
} from "@/components/ui/confirm-dialog";

const DEFAULT_MESSAGE = "Leaving now will discard your edits.";

/**
 * Guards in-app navigation (TanStack Router `useBlocker`) and browser
 * tab-close (the blocker's built-in `beforeunload` listener) while
 * `isDirty` is true. Render the returned `dialog` node next to the guarded
 * form/editor so the Stay/Discard prompt can appear over it.
 *
 * `isDirty` should already account for "just submitted" (e.g.
 * `form.state.isDirty && !form.state.isSubmitting && !form.state.isSubmitSuccessful`)
 * so a post-submit redirect never blocks.
 */
export function useUnsavedGuard(
  isDirty: boolean,
  message: string = DEFAULT_MESSAGE,
): { dialog: ReactNode; isBlocked: boolean } {
  const blocker = useBlocker({
    shouldBlockFn: () => isDirty,
    enableBeforeUnload: isDirty,
    withResolver: true,
  });

  const impact: DestructiveImpactPreview = {
    scope: "This page",
    consequences: [message],
    recovery: "None — the changes are not saved anywhere.",
  };

  const dialog = createElement(ConfirmDialog, {
    open: blocker.status === "blocked",
    onClose: () => blocker.reset?.(),
    onConfirm: () => blocker.proceed?.(),
    title: "Unsaved changes",
    description: message,
    confirmText: "Discard",
    variant: "destructive",
    impact,
  });

  return { dialog, isBlocked: blocker.status === "blocked" };
}
