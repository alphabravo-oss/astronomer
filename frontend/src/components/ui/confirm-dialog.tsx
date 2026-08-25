"use client";

import { useState, useEffect, type ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";

interface ConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  description: string;
  confirmText?: string;
  confirmValue?: string;
  confirmDisabledReason?: string;
  variant?: "destructive";
  loading?: boolean;
  impact?: DestructiveImpactPreview;
  // Extra content rendered below the description (e.g. a "force" checkbox).
  children?: ReactNode;
}

/**
 * A concise, operator-facing preview of what a destructive action affects.
 * Keep consequences factual and avoid claiming that a Kubernetes controller,
 * storage provider, or backup can recover an object unless that is guaranteed.
 */
export interface DestructiveImpactPreview {
  scope: string;
  consequences: readonly string[];
  recovery: string;
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmText = "Delete",
  confirmValue,
  confirmDisabledReason,
  variant,
  loading,
  impact,
  children,
}: ConfirmDialogProps) {
  const [inputValue, setInputValue] = useState("");

  // Reset input when dialog opens/closes
  useEffect(() => {
    if (!open) setInputValue("");
  }, [open]);

  if (!open) return null;

  const canConfirm = confirmValue ? inputValue === confirmValue : true;

  return (
    <ModalShell
      title={title}
      onClose={onClose}
      size="sm"
      subtitle={description}
      titleIcon={
        variant === "destructive" ? (
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-status-error/10">
            <AlertTriangle className="h-5 w-5 text-status-error" />
          </div>
        ) : undefined
      }
      footer={
        <>
          <ActionButton
            onClick={onClose}
            disabled={loading}
            intent="ghost"
            size="sm"
          >
            Cancel
          </ActionButton>
          <ActionButton
            onClick={onConfirm}
            disabled={!canConfirm || loading || !!confirmDisabledReason}
            disabledReason={
              confirmDisabledReason ||
              (!canConfirm
                ? "Type the confirmation value to continue"
                : undefined)
            }
            intent={variant === "destructive" ? "destructive" : "primary"}
            loading={loading}
            loadingLabel={confirmText}
            size="sm"
          >
            {confirmText}
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      {impact && (
        <section
          aria-label="Impact preview"
          className="rounded-md border border-status-error/20 bg-status-error/5 p-3"
        >
          <h3 className="text-xs font-semibold text-foreground">
            Impact preview
          </h3>
          <dl className="mt-2 space-y-2 text-xs">
            <div>
              <dt className="font-medium text-muted-foreground">Scope</dt>
              <dd className="mt-0.5 break-words font-mono text-foreground">
                {impact.scope}
              </dd>
            </div>
            <div>
              <dt className="font-medium text-muted-foreground">
                Expected effects
              </dt>
              <dd>
                <ul className="mt-1 list-disc space-y-1 pl-4 text-foreground">
                  {impact.consequences.map((consequence) => (
                    <li key={consequence}>{consequence}</li>
                  ))}
                </ul>
              </dd>
            </div>
            <div>
              <dt className="font-medium text-muted-foreground">Recovery</dt>
              <dd className="mt-0.5 text-foreground">{impact.recovery}</dd>
            </div>
          </dl>
        </section>
      )}
      {confirmValue && (
        <div>
          <label className="mb-1.5 block text-xs text-muted-foreground">
            Type{" "}
            <span className="font-mono font-medium text-foreground">
              {confirmValue}
            </span>{" "}
            to confirm
          </label>
          <Input
            type="text"
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            className="h-8 font-mono"
            placeholder={confirmValue}
            autoComplete="off"
            spellCheck={false}
            data-initial-focus
          />
        </div>
      )}
      {children}
    </ModalShell>
  );
}
