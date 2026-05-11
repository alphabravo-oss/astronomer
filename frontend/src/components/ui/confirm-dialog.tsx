'use client';

import { useState, useEffect } from 'react';
import { cn } from '@/lib/utils';
import { Loader2, AlertTriangle } from 'lucide-react';

interface ConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  description: string;
  confirmText?: string;
  confirmValue?: string;
  variant?: 'destructive';
  loading?: boolean;
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmText = 'Delete',
  confirmValue,
  variant,
  loading,
}: ConfirmDialogProps) {
  const [inputValue, setInputValue] = useState('');

  // Reset input when dialog opens/closes
  useEffect(() => {
    if (!open) setInputValue('');
  }, [open]);

  // Close on Escape
  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  if (!open) return null;

  const canConfirm = confirmValue ? inputValue === confirmValue : true;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/50 backdrop-blur-sm"
        onClick={onClose}
      />

      {/* Dialog */}
      <div className="relative bg-card border border-border rounded-lg shadow-xl max-w-md w-full mx-4 animate-fade-in">
        <div className="p-6">
          <div className="flex items-start gap-3">
            {variant === 'destructive' && (
              <div className="flex-shrink-0 h-9 w-9 rounded-full bg-status-error/10 flex items-center justify-center">
                <AlertTriangle className="h-5 w-5 text-status-error" />
              </div>
            )}
            <div className="min-w-0 flex-1">
              <h3 className="text-base font-semibold text-foreground">{title}</h3>
              <p className="mt-1 text-sm text-muted-foreground">{description}</p>

              {confirmValue && (
                <div className="mt-4">
                  <label className="block text-xs text-muted-foreground mb-1.5">
                    Type <span className="font-mono font-medium text-foreground">{confirmValue}</span> to confirm
                  </label>
                  <input
                    type="text"
                    value={inputValue}
                    onChange={(e) => setInputValue(e.target.value)}
                    className="w-full h-8 px-3 rounded border border-border bg-background text-sm
                      focus:outline-none focus:ring-1 focus:ring-ring font-mono"
                    placeholder={confirmValue}
                    autoFocus
                  />
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border">
          <button
            onClick={onClose}
            disabled={loading}
            className="inline-flex items-center h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={!canConfirm || loading}
            className={cn(
              'inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium transition-colors',
              variant === 'destructive'
                ? 'bg-status-error text-white hover:bg-status-error/90 disabled:opacity-50 disabled:cursor-not-allowed'
                : 'bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50 disabled:cursor-not-allowed',
            )}
          >
            {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {confirmText}
          </button>
        </div>
      </div>
    </div>
  );
}
