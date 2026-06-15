'use client';

import { useState, useEffect } from 'react';
import { Loader2, Minus, Plus } from 'lucide-react';
import { ModalShell } from '@/components/ui/modal-shell';

interface ScaleDialogProps {
  open: boolean;
  onClose: () => void;
  onScale: (replicas: number) => void;
  workloadName: string;
  currentReplicas: number;
  loading?: boolean;
}

export function ScaleDialog({
  open,
  onClose,
  onScale,
  workloadName,
  currentReplicas,
  loading,
}: ScaleDialogProps) {
  const [replicas, setReplicas] = useState(currentReplicas);

  useEffect(() => {
    if (open) setReplicas(currentReplicas);
  }, [open, currentReplicas]);

  if (!open) return null;

  return (
    <ModalShell
      title="Scale Workload"
      onClose={onClose}
      size="sm"
      panelClassName="max-w-sm"
      bodyClassName="space-y-0"
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            disabled={loading}
            className="inline-flex items-center h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={() => onScale(replicas)}
            disabled={loading || replicas === currentReplicas}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90
              disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Scale
          </button>
        </div>
      )}
    >
      <p className="text-sm text-muted-foreground">
        Set the desired number of replicas for{' '}
        <span className="font-mono text-foreground">{workloadName}</span>
      </p>

      <div className="mt-5 flex items-center justify-center gap-4">
        <button
          onClick={() => setReplicas(Math.max(0, replicas - 1))}
          className="inline-flex items-center justify-center h-9 w-9 rounded-md border border-border
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <Minus className="h-4 w-4" />
        </button>

        <input
          type="number"
          min={0}
          max={100}
          value={replicas}
          onChange={(e) => setReplicas(Math.max(0, Math.min(100, Number(e.target.value) || 0)))}
          className="h-10 w-20 text-center text-lg font-medium tabular-nums rounded border border-border
            bg-background focus:outline-none focus:ring-1 focus:ring-ring"
        />

        <button
          onClick={() => setReplicas(Math.min(100, replicas + 1))}
          className="inline-flex items-center justify-center h-9 w-9 rounded-md border border-border
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <Plus className="h-4 w-4" />
        </button>
      </div>

      <p className="mt-2 text-center text-xs text-muted-foreground">
        Current: {currentReplicas} replica{currentReplicas !== 1 ? 's' : ''}
      </p>
    </ModalShell>
  );
}
