'use client';

import type { ReactNode } from 'react';
import { Check, Circle, Loader2, X } from 'lucide-react';
import { cn } from '@/lib/utils';

export type OperationTimelineStepStatus = 'pending' | 'running' | 'success' | 'failed' | 'skipped';

export interface OperationTimelineStep {
  id: string;
  label: ReactNode;
  status: OperationTimelineStepStatus;
  detail?: ReactNode;
  error?: ReactNode;
  progressPct?: number;
  action?: ReactNode;
}

interface OperationTimelineProps {
  header: ReactNode;
  headerMeta?: ReactNode;
  steps: OperationTimelineStep[];
  emptyLabel?: ReactNode;
  footer?: ReactNode;
  className?: string;
}

export function OperationTimeline({
  header,
  headerMeta,
  steps,
  emptyLabel = 'No operation steps yet.',
  footer,
  className,
}: OperationTimelineProps) {
  return (
    <div className={cn('rounded-lg border border-border bg-card', className)}>
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        {header}
        {headerMeta && <span className="text-xs text-muted-foreground">{headerMeta}</span>}
      </div>

      <ul className="divide-y divide-border">
        {steps.map((step) => (
          <li key={step.id} className="flex items-start gap-3 px-4 py-3">
            <StepIcon status={step.status} />
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium text-foreground">{step.label}</div>
              {step.detail && (
                <div className="mt-0.5 truncate text-xs text-muted-foreground">{step.detail}</div>
              )}
              {step.error && <div className="mt-1 text-xs text-status-danger">{step.error}</div>}
              {step.status === 'running' && step.progressPct && step.progressPct > 0 && (
                <div className="mt-1 h-1.5 w-full overflow-hidden rounded bg-muted">
                  <div className="h-full bg-primary" style={{ width: `${step.progressPct}%` }} />
                </div>
              )}
            </div>
            {step.action}
          </li>
        ))}
        {steps.length === 0 && (
          <li className="px-4 py-6 text-center text-sm text-muted-foreground">{emptyLabel}</li>
        )}
      </ul>

      {footer}
    </div>
  );
}

function StepIcon({ status }: { status: OperationTimelineStepStatus }) {
  switch (status) {
    case 'success':
      return <Check className="mt-0.5 h-4 w-4 text-status-success" />;
    case 'running':
      return <Loader2 className="mt-0.5 h-4 w-4 animate-spin text-primary" />;
    case 'failed':
      return <X className="mt-0.5 h-4 w-4 text-status-danger" />;
    case 'skipped':
      return <Circle className="mt-0.5 h-4 w-4 text-muted-foreground/40" />;
    default:
      return <Circle className="mt-0.5 h-4 w-4 text-muted-foreground/40" />;
  }
}
