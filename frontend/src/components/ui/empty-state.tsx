'use client';

import Link from 'next/link';
import type { ElementType, ReactNode } from 'react';
import { cn } from '@/lib/utils';

interface EmptyStateProps {
  icon: ElementType;
  title: string;
  description: ReactNode;
  actionLabel?: string;
  actionHref?: string;
  actionIcon?: ElementType;
  onAction?: () => void;
  disabled?: boolean;
  className?: string;
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  actionLabel,
  actionHref,
  actionIcon: ActionIcon,
  onAction,
  disabled = false,
  className,
}: EmptyStateProps) {
  const hasAction = !!actionLabel && (!!actionHref || !!onAction);

  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center py-16 text-center space-y-3',
        className,
      )}
    >
      <div className="flex h-12 w-12 items-center justify-center rounded-lg bg-muted">
        <Icon className="h-6 w-6 text-muted-foreground" />
      </div>
      <div className="space-y-1">
        <p className="text-base font-medium text-foreground">{title}</p>
        <p className="max-w-md text-sm text-muted-foreground">{description}</p>
      </div>
      {hasAction && (
        actionHref && !disabled ? (
          <Link
            href={actionHref}
            className="inline-flex h-9 items-center justify-center gap-2 rounded-lg border border-border px-4 text-sm font-medium transition-colors hover:bg-accent"
          >
            {ActionIcon && <ActionIcon className="h-4 w-4" />}
            {actionLabel}
          </Link>
        ) : (
          <button
            type="button"
            onClick={onAction}
            disabled={disabled}
            className="inline-flex h-9 items-center justify-center gap-2 rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {ActionIcon && <ActionIcon className="h-4 w-4" />}
            {actionLabel}
          </button>
        )
      )}
    </div>
  );
}
