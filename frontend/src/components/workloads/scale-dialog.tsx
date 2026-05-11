'use client';

import { useState, useEffect } from 'react';
import { Loader2, Minus, Plus } from 'lucide-react';

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

  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />

      <div className="relative bg-card border border-border rounded-lg shadow-xl max-w-sm w-full mx-4 animate-fade-in">
        <div className="p-6">
          <h3 className="text-base font-semibold text-foreground">Scale Workload</h3>
          <p className="mt-1 text-sm text-muted-foreground">
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
      </div>
    </div>
  );
}
