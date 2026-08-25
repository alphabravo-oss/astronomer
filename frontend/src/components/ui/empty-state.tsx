"use client";

import { Link } from "@/lib/link";
import type { ElementType, ReactNode } from "react";
import {
  AlertCircle,
  Clock3,
  Loader2,
  Lock,
  RefreshCw,
  TriangleAlert,
  WifiOff,
  XCircle,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";

type StateTone = "neutral" | "danger" | "warning" | "info";

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

interface StatePanelProps {
  title: string;
  description?: ReactNode;
  icon?: ElementType;
  tone?: StateTone;
  actionLabel?: string;
  actionHref?: string;
  actionIcon?: ElementType;
  onAction?: () => void;
  disabled?: boolean;
  className?: string;
  iconClassName?: string;
  role?: string;
}

const toneClass: Record<StateTone, string> = {
  neutral: "bg-muted text-muted-foreground",
  danger: "bg-status-error/10 text-status-error",
  warning: "bg-status-warning/10 text-status-warning",
  info: "bg-status-info/10 text-status-info",
};

export function StatePanel({
  icon: Icon,
  title,
  description,
  tone = "neutral",
  actionLabel,
  actionHref,
  actionIcon: ActionIcon,
  onAction,
  disabled = false,
  className,
  iconClassName,
  role,
}: StatePanelProps) {
  const hasAction = !!actionLabel && (!!actionHref || !!onAction);

  return (
    <div
      role={role}
      className={cn(
        "flex flex-col items-center justify-center py-16 text-center space-y-3",
        className,
      )}
    >
      {Icon && (
        <div
          className={cn(
            "flex h-12 w-12 items-center justify-center rounded-lg",
            toneClass[tone],
          )}
        >
          <Icon className={cn("h-6 w-6", iconClassName)} />
        </div>
      )}
      <div className="space-y-1">
        <p className="text-base font-medium text-foreground">{title}</p>
        {description && (
          <p className="max-w-md text-sm text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      {hasAction &&
        (actionHref && !disabled ? (
          <Link
            href={actionHref}
            className="inline-flex h-9 items-center justify-center gap-2 rounded-lg border border-border px-4 text-sm font-medium transition-colors hover:bg-accent"
          >
            {ActionIcon && <ActionIcon className="h-4 w-4" />}
            {actionLabel}
          </Link>
        ) : (
          <ActionButton
            type="button"
            onClick={onAction}
            disabled={disabled}
            intent="primary"
            icon={ActionIcon ? <ActionIcon className="h-4 w-4" /> : undefined}
          >
            {actionLabel}
          </ActionButton>
        ))}
    </div>
  );
}

export function EmptyState(props: EmptyStateProps) {
  return <StatePanel {...props} />;
}

export function LoadingState({
  title = "Loading",
  description,
  className,
}: {
  title?: string;
  description?: ReactNode;
  className?: string;
}) {
  return (
    <StatePanel
      icon={Loader2}
      title={title}
      description={description}
      tone="info"
      iconClassName="animate-spin"
      className={className}
    />
  );
}

export function ErrorState({
  title = "Failed to load",
  description,
  actionLabel = "Retry",
  onRetry,
  className,
}: {
  title?: string;
  description?: ReactNode;
  actionLabel?: string;
  onRetry?: () => void;
  className?: string;
}) {
  return (
    <StatePanel
      icon={AlertCircle}
      title={title}
      description={description}
      tone="danger"
      actionLabel={onRetry ? actionLabel : undefined}
      onAction={onRetry}
      className={className}
      role="alert"
    />
  );
}

export function PermissionState({
  title = "Permission required",
  permission,
  description,
  className,
}: {
  title?: string;
  permission?: string;
  description?: ReactNode;
  className?: string;
}) {
  return (
    <StatePanel
      icon={Lock}
      title={title}
      description={
        description ??
        (permission ? (
          <>
            You need <code className="font-mono">{permission}</code> to use this
            surface.
          </>
        ) : undefined)
      }
      tone="warning"
      className={className}
    />
  );
}

export function PartialState({
  title = "Partial results",
  description,
  actionLabel = "Retry missing data",
  onRetry,
  className,
}: {
  title?: string;
  description?: ReactNode;
  actionLabel?: string;
  onRetry?: () => void;
  className?: string;
}) {
  return (
    <StatePanel
      icon={TriangleAlert}
      title={title}
      description={description}
      tone="warning"
      actionLabel={onRetry ? actionLabel : undefined}
      actionIcon={RefreshCw}
      onAction={onRetry}
      className={className}
      role="status"
    />
  );
}

export function OfflineState({
  title = "Connection unavailable",
  description = "Astronomer cannot reach the service. Check your connection and try again.",
  onRetry,
  className,
}: {
  title?: string;
  description?: ReactNode;
  onRetry?: () => void;
  className?: string;
}) {
  return (
    <StatePanel
      icon={WifiOff}
      title={title}
      description={description}
      tone="warning"
      actionLabel={onRetry ? "Reconnect" : undefined}
      actionIcon={RefreshCw}
      onAction={onRetry}
      className={className}
      role="alert"
    />
  );
}

export function StaleState({
  title = "Showing stale data",
  description = "Live updates are delayed. The last successfully loaded data remains visible.",
  onRefresh,
  className,
}: {
  title?: string;
  description?: ReactNode;
  onRefresh?: () => void;
  className?: string;
}) {
  return (
    <StatePanel
      icon={Clock3}
      title={title}
      description={description}
      tone="warning"
      actionLabel={onRefresh ? "Refresh now" : undefined}
      actionIcon={RefreshCw}
      onAction={onRefresh}
      className={className}
      role="status"
    />
  );
}

export function RetryingState({
  title = "Retrying",
  description = "The operation did not complete yet. Astronomer is retrying safely.",
  className,
}: {
  title?: string;
  description?: ReactNode;
  className?: string;
}) {
  return (
    <StatePanel
      icon={Loader2}
      title={title}
      description={description}
      tone="info"
      iconClassName="animate-spin"
      className={className}
      role="status"
    />
  );
}

export function TerminalFailureState({
  title = "Operation failed",
  description,
  actionLabel = "Try again",
  onRetry,
  className,
}: {
  title?: string;
  description?: ReactNode;
  actionLabel?: string;
  onRetry?: () => void;
  className?: string;
}) {
  return (
    <StatePanel
      icon={XCircle}
      title={title}
      description={description}
      tone="danger"
      actionLabel={onRetry ? actionLabel : undefined}
      actionIcon={RefreshCw}
      onAction={onRetry}
      className={className}
      role="alert"
    />
  );
}
