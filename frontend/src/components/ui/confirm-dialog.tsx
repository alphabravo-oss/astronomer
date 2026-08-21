'use client';

import { useState, useEffect, type ReactNode } from 'react';
import { AlertTriangle } from 'lucide-react';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';

interface ConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  description: string;
  confirmText?: string;
  confirmValue?: string;
  confirmDisabledReason?: string;
  variant?: 'destructive';
  loading?: boolean;
  // Extra content rendered below the description (e.g. a "force" checkbox).
  children?: ReactNode;
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmText = 'Delete',
  confirmValue,
  confirmDisabledReason,
  variant,
  loading,
  children,
}: ConfirmDialogProps) {
  const [inputValue, setInputValue] = useState('');

  // Reset input when dialog opens/closes
  useEffect(() => {
    if (!open) setInputValue('');
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
        variant === 'destructive' ? (
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-status-error/10">
            <AlertTriangle className="h-5 w-5 text-status-error" />
          </div>
        ) : undefined
      }
      footer={
        <>
          <ActionButton onClick={onClose} disabled={loading} intent="ghost" size="sm">
            Cancel
          </ActionButton>
          <ActionButton
            onClick={onConfirm}
            disabled={!canConfirm || loading || !!confirmDisabledReason}
            disabledReason={
              confirmDisabledReason ||
              (!canConfirm ? 'Type the confirmation value to continue' : undefined)
            }
            intent={variant === 'destructive' ? 'destructive' : 'primary'}
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
      {confirmValue && (
        <div>
          <label className="mb-1.5 block text-xs text-muted-foreground">
            Type <span className="font-mono font-medium text-foreground">{confirmValue}</span> to confirm
          </label>
          <Input
            type="text"
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            className="h-8 font-mono"
            placeholder={confirmValue}
            autoFocus
          />
        </div>
      )}
      {children}
    </ModalShell>
  );
}
