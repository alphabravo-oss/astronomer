'use client';

import { useEffect, useId, useMemo, useState } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { FleetSelector, FleetSelectorOperator } from '@/lib/api/fleet-operations';

interface LabelRow {
  key: string;
  value: string;
}

interface ExprRow {
  key: string;
  operator: FleetSelectorOperator;
  values: string; // comma-separated in the input, split on build
}

const OPERATORS: FleetSelectorOperator[] = ['In', 'NotIn', 'Exists', 'DoesNotExist'];

function operatorNeedsValues(op: FleetSelectorOperator): boolean {
  return op === 'In' || op === 'NotIn';
}

/**
 * Build the wire-shape FleetSelector from the row state. Empty rows are
 * dropped; only non-empty keys contribute. This is the exact shape the
 * backend validator expects (matchLabels / matchExpressions / matchGroupIDs).
 */
export function buildSelector(
  labels: LabelRow[],
  exprs: ExprRow[],
  groupIds: string[],
): FleetSelector {
  const selector: FleetSelector = {};

  const matchLabels: Record<string, string> = {};
  for (const row of labels) {
    const key = row.key.trim();
    if (key) matchLabels[key] = row.value;
  }
  if (Object.keys(matchLabels).length > 0) selector.matchLabels = matchLabels;

  const matchExpressions = exprs
    .filter((row) => row.key.trim())
    .map((row) => {
      const expr: { key: string; operator: FleetSelectorOperator; values?: string[] } = {
        key: row.key.trim(),
        operator: row.operator,
      };
      if (operatorNeedsValues(row.operator)) {
        expr.values = row.values
          .split(',')
          .map((v) => v.trim())
          .filter(Boolean);
      }
      return expr;
    });
  if (matchExpressions.length > 0) selector.matchExpressions = matchExpressions;

  if (groupIds.length > 0) selector.matchGroupIDs = groupIds;

  return selector;
}

interface SelectorBuilderProps {
  onChange: (selector: FleetSelector) => void;
  /** Aggregated label key -> distinct values, for autocomplete. */
  labelSuggestions?: Record<string, string[]>;
  groups?: { id: string; name: string }[];
  className?: string;
}

export function SelectorBuilder({
  onChange,
  labelSuggestions = {},
  groups = [],
  className,
}: SelectorBuilderProps) {
  const [labels, setLabels] = useState<LabelRow[]>([{ key: '', value: '' }]);
  const [exprs, setExprs] = useState<ExprRow[]>([]);
  const [groupIds, setGroupIds] = useState<string[]>([]);

  const keyListId = useId();

  const labelKeys = useMemo(() => Object.keys(labelSuggestions).sort(), [labelSuggestions]);

  // Emit whenever any row changes. buildSelector drops empty rows so the
  // parent always sees the canonical wire shape.
  useEffect(() => {
    onChange(buildSelector(labels, exprs, groupIds));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [labels, exprs, groupIds]);

  return (
    <div className={cn('space-y-5', className)}>
      {/* datalist of known label keys */}
      <datalist id={keyListId}>
        {labelKeys.map((k) => (
          <option key={k} value={k} />
        ))}
      </datalist>

      {/* matchLabels */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-foreground">Match labels</label>
          <button
            type="button"
            onClick={() => setLabels((rows) => [...rows, { key: '', value: '' }])}
            className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
          >
            <Plus className="h-3 w-3" /> Add label
          </button>
        </div>
        {labels.length === 0 && (
          <p className="text-xs text-muted-foreground">No label matchers.</p>
        )}
        {labels.map((row, i) => {
          const valueListId = `${keyListId}-vals-${i}`;
          const suggestions = labelSuggestions[row.key.trim()] ?? [];
          return (
            <div key={i} className="flex items-center gap-2">
              <input
                aria-label={`label key ${i + 1}`}
                list={keyListId}
                value={row.key}
                onChange={(e) =>
                  setLabels((rows) => rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)))
                }
                placeholder="tier"
                className="h-8 flex-1 rounded-md border border-border bg-background px-2 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <span className="text-muted-foreground">=</span>
              <datalist id={valueListId}>
                {suggestions.map((v) => (
                  <option key={v} value={v} />
                ))}
              </datalist>
              <input
                aria-label={`label value ${i + 1}`}
                list={valueListId}
                value={row.value}
                onChange={(e) =>
                  setLabels((rows) => rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))
                }
                placeholder="prod"
                className="h-8 flex-1 rounded-md border border-border bg-background px-2 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <button
                type="button"
                aria-label={`remove label ${i + 1}`}
                onClick={() => setLabels((rows) => rows.filter((_, j) => j !== i))}
                className="text-muted-foreground hover:text-status-error"
              >
                <Trash2 className="h-4 w-4" />
              </button>
            </div>
          );
        })}
      </div>

      {/* matchExpressions */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-foreground">Match expressions</label>
          <button
            type="button"
            onClick={() => setExprs((rows) => [...rows, { key: '', operator: 'In', values: '' }])}
            className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
          >
            <Plus className="h-3 w-3" /> Add expression
          </button>
        </div>
        {exprs.length === 0 && (
          <p className="text-xs text-muted-foreground">No set-based matchers.</p>
        )}
        {exprs.map((row, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              aria-label={`expression key ${i + 1}`}
              list={keyListId}
              value={row.key}
              onChange={(e) =>
                setExprs((rows) => rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)))
              }
              placeholder="env"
              className="h-8 flex-1 rounded-md border border-border bg-background px-2 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <select
              aria-label={`expression operator ${i + 1}`}
              value={row.operator}
              onChange={(e) =>
                setExprs((rows) =>
                  rows.map((r, j) =>
                    j === i ? { ...r, operator: e.target.value as FleetSelectorOperator } : r,
                  ),
                )
              }
              className="h-8 rounded-md border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
            >
              {OPERATORS.map((op) => (
                <option key={op} value={op}>
                  {op}
                </option>
              ))}
            </select>
            <input
              aria-label={`expression values ${i + 1}`}
              value={row.values}
              disabled={!operatorNeedsValues(row.operator)}
              onChange={(e) =>
                setExprs((rows) => rows.map((r, j) => (j === i ? { ...r, values: e.target.value } : r)))
              }
              placeholder={operatorNeedsValues(row.operator) ? 'staging, canary' : '—'}
              className="h-8 flex-1 rounded-md border border-border bg-background px-2 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-40"
            />
            <button
              type="button"
              aria-label={`remove expression ${i + 1}`}
              onClick={() => setExprs((rows) => rows.filter((_, j) => j !== i))}
              className="text-muted-foreground hover:text-status-error"
            >
              <Trash2 className="h-4 w-4" />
            </button>
          </div>
        ))}
      </div>

      {/* matchGroupIDs */}
      {groups.length > 0 && (
        <div className="space-y-2">
          <label className="text-sm font-medium text-foreground">Cluster groups</label>
          <div className="flex flex-wrap gap-2">
            {groups.map((g) => {
              const selected = groupIds.includes(g.id);
              return (
                <button
                  key={g.id}
                  type="button"
                  aria-pressed={selected}
                  onClick={() =>
                    setGroupIds((ids) =>
                      selected ? ids.filter((x) => x !== g.id) : [...ids, g.id],
                    )
                  }
                  className={cn(
                    'rounded-full border px-3 py-1 text-xs transition-colors',
                    selected
                      ? 'border-primary bg-primary/10 text-primary'
                      : 'border-border text-muted-foreground hover:bg-accent',
                  )}
                >
                  {g.name}
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
