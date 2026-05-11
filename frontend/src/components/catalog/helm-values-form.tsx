'use client';

import { cn } from '@/lib/utils';
import {
  appendArrayItem,
  defaultValueForSchema,
  getValueAtPath,
  removeArrayItem,
  setValueAtPath,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from '@/lib/helm-values-schema';
import { Plus, Trash2 } from 'lucide-react';

interface HelmValuesFormProps {
  schema: HelmValuesSchemaNode;
  value: HelmValuesObject;
  onChange: (next: HelmValuesObject) => void;
  path?: string[];
}

function labelFor(schema: HelmValuesSchemaNode, key: string): string {
  return schema.title || key;
}

function helpText(schema: HelmValuesSchemaNode) {
  return schema.description ? (
    <p className="text-xs text-muted-foreground mt-1 leading-relaxed">{schema.description}</p>
  ) : null;
}

function schemaType(schema: HelmValuesSchemaNode): string {
  if (Array.isArray(schema.type)) return schema.type.find((t) => t !== 'null') || schema.type[0] || 'string';
  if (schema.type) return schema.type;
  if (schema.properties) return 'object';
  if (schema.items) return 'array';
  return 'string';
}

export function HelmValuesForm({
  schema,
  value,
  onChange,
  path = [],
}: HelmValuesFormProps) {
  const type = schemaType(schema);

  if (type === 'object') {
    const objectValue = (getValueAtPath(value, path) as Record<string, unknown> | undefined) || {};
    return (
      <div className={cn('space-y-4', path.length > 0 && 'rounded-lg border border-border/60 p-4 bg-muted/20')}>
        {path.length > 0 && (
          <div>
            <h4 className="text-sm font-medium text-foreground">{schema.title || path[path.length - 1]}</h4>
            {helpText(schema)}
          </div>
        )}
        {Object.entries(schema.properties || {}).map(([key, childSchema]) => {
          const childPath = [...path, key];
          const childType = schemaType(childSchema);
          const rawValue = objectValue[key] ?? defaultValueForSchema(childSchema);

          if (childType === 'object' || childType === 'array') {
            return (
              <div key={childPath.join('.')} className="space-y-2">
                {childType === 'array' ? (
                  <ArrayField schema={childSchema} path={childPath} rootValue={value} onChange={onChange} />
                ) : (
                  <HelmValuesForm schema={childSchema} value={value} onChange={onChange} path={childPath} />
                )}
              </div>
            );
          }

          return (
            <ScalarField
              key={childPath.join('.')}
              schema={childSchema}
              label={labelFor(childSchema, key)}
              value={rawValue}
              onChange={(nextScalar) => onChange(setValueAtPath(value, childPath, nextScalar))}
            />
          );
        })}
      </div>
    );
  }

  return null;
}

function ScalarField({
  schema,
  label,
  value,
  onChange,
}: {
  schema: HelmValuesSchemaNode;
  label: string;
  value: unknown;
  onChange: (next: unknown) => void;
}) {
  const type = schemaType(schema);
  const baseCls =
    'w-full rounded-md border border-border bg-background text-sm text-foreground ' +
    'placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring';

  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">{label}</label>
      {type === 'boolean' ? (
        <label className="inline-flex items-center gap-2 text-sm text-foreground">
          <input
            type="checkbox"
            checked={Boolean(value)}
            onChange={(e) => onChange(e.target.checked)}
            className="h-4 w-4 rounded border-border"
          />
          Enabled
        </label>
      ) : schema.enum && schema.enum.length > 0 ? (
        <select
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          className={cn(baseCls, 'h-9 px-3')}
        >
          {schema.enum.map((item) => (
            <option key={String(item)} value={String(item)}>
              {String(item)}
            </option>
          ))}
        </select>
      ) : type === 'integer' || type === 'number' ? (
        <input
          type="number"
          value={typeof value === 'number' ? value : Number(value ?? 0)}
          onChange={(e) => onChange(type === 'integer' ? parseInt(e.target.value || '0', 10) : parseFloat(e.target.value || '0'))}
          className={cn(baseCls, 'h-9 px-3')}
        />
      ) : (
        <textarea
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          rows={String(value ?? '').includes('\n') ? 4 : 2}
          className={cn(baseCls, 'px-3 py-2 resize-y font-mono')}
        />
      )}
      {helpText(schema)}
    </div>
  );
}

function ArrayField({
  schema,
  path,
  rootValue,
  onChange,
}: {
  schema: HelmValuesSchemaNode;
  path: string[];
  rootValue: HelmValuesObject;
  onChange: (next: HelmValuesObject) => void;
}) {
  const itemsSchema = schema.items || { type: 'string' };
  const itemType = schemaType(itemsSchema);
  const items = (getValueAtPath(rootValue, path) as unknown[]) || [];

  return (
    <div className="space-y-2 rounded-lg border border-border/60 p-4 bg-muted/20">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h4 className="text-sm font-medium text-foreground">{schema.title || path[path.length - 1]}</h4>
          {helpText(schema)}
        </div>
        <button
          type="button"
          onClick={() => onChange(appendArrayItem(rootValue, path, schema))}
          className="inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <Plus className="h-3.5 w-3.5" />
          Add item
        </button>
      </div>

      {items.length === 0 ? (
        <p className="text-xs text-muted-foreground">No items yet.</p>
      ) : (
        <div className="space-y-3">
          {items.map((item, index) => {
            const itemPath = [...path, String(index)];
            return (
              <div key={itemPath.join('.')} className="rounded-md border border-border/50 bg-background/70 p-3 space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Item {index + 1}</span>
                  <button
                    type="button"
                    onClick={() => onChange(removeArrayItem(rootValue, path, index))}
                    className="rounded-md p-1 text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
                    aria-label={`Remove item ${index + 1}`}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
                {itemType === 'object' ? (
                  <HelmValuesForm schema={itemsSchema} value={rootValue} onChange={onChange} path={itemPath} />
                ) : (
                  <ScalarField
                    schema={itemsSchema}
                    label={itemsSchema.title || `Item ${index + 1}`}
                    value={item}
                    onChange={(nextScalar) => onChange(setValueAtPath(rootValue, itemPath, nextScalar))}
                  />
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
