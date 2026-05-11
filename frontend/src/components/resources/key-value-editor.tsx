'use client';

import { useState } from 'react';
import { Plus, Trash2, Eye, EyeOff } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface KeyValuePair {
  key: string;
  value: string;
}

interface KeyValueEditorProps {
  pairs: KeyValuePair[];
  onChange: (pairs: KeyValuePair[]) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  /** If true, values are shown masked with a show/hide toggle */
  masked?: boolean;
  readOnly?: boolean;
}

export function KeyValueEditor({
  pairs,
  onChange,
  keyPlaceholder = 'Key',
  valuePlaceholder = 'Value',
  masked = false,
  readOnly = false,
}: KeyValueEditorProps) {
  const [revealedIndexes, setRevealedIndexes] = useState<Set<number>>(new Set());

  const addPair = () => {
    onChange([...pairs, { key: '', value: '' }]);
  };

  const removePair = (index: number) => {
    onChange(pairs.filter((_, i) => i !== index));
    setRevealedIndexes((prev) => {
      const next = new Set(prev);
      next.delete(index);
      return next;
    });
  };

  const updatePair = (index: number, field: 'key' | 'value', val: string) => {
    const updated = pairs.map((p, i) => (i === index ? { ...p, [field]: val } : p));
    onChange(updated);
  };

  const toggleReveal = (index: number) => {
    setRevealedIndexes((prev) => {
      const next = new Set(prev);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  };

  return (
    <div className="space-y-2">
      {pairs.map((pair, i) => (
        <div key={i} className="flex items-center gap-2">
          <input
            type="text"
            value={pair.key}
            onChange={(e) => updatePair(i, 'key', e.target.value)}
            placeholder={keyPlaceholder}
            readOnly={readOnly}
            className="flex-1 h-8 px-2.5 rounded border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-1 focus:ring-ring"
          />
          <div className="relative flex-1">
            <input
              type={masked && !revealedIndexes.has(i) ? 'password' : 'text'}
              value={pair.value}
              onChange={(e) => updatePair(i, 'value', e.target.value)}
              placeholder={valuePlaceholder}
              readOnly={readOnly}
              className={cn(
                'w-full h-8 px-2.5 rounded border border-border bg-background text-sm font-mono',
                'focus:outline-none focus:ring-1 focus:ring-ring',
                masked && 'pr-8'
              )}
            />
            {masked && (
              <button
                type="button"
                onClick={() => toggleReveal(i)}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              >
                {revealedIndexes.has(i) ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
              </button>
            )}
          </div>
          {!readOnly && (
            <button
              type="button"
              onClick={() => removePair(i)}
              className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      ))}
      {!readOnly && (
        <button
          type="button"
          onClick={addPair}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium
            text-muted-foreground hover:text-foreground hover:bg-accent border border-dashed border-border transition-colors"
        >
          <Plus className="h-3.5 w-3.5" /> Add Entry
        </button>
      )}
    </div>
  );
}
