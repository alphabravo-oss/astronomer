import type { ReactNode } from 'react';
import { CodeBlock } from '@/components/ui/code-block';
import { DrawerShell } from '@/components/ui/drawer-shell';

export type ActivityDetailField = {
  label: string;
  value: ReactNode;
};

export function ActivityDetailsDrawer({
  title,
  subtitle,
  fields,
  detail,
  detailTitle = 'Detail',
  onClose,
}: {
  title: string;
  subtitle?: ReactNode;
  fields: ActivityDetailField[];
  detail?: Record<string, unknown>;
  detailTitle?: string;
  onClose: () => void;
}) {
  return (
    <DrawerShell title={title} subtitle={subtitle} onClose={onClose}>
      <div className="grid gap-x-4 gap-y-3 md:grid-cols-2">
        {fields.map((field) => (
          <div key={String(field.label)} className="min-w-0">
            <div className="text-2xs font-medium uppercase text-muted-foreground">{field.label}</div>
            <div className="mt-1 break-words font-mono text-xs text-foreground">{field.value}</div>
          </div>
        ))}
      </div>

      {detail ? (
        <div className="mt-5">
          <CodeBlock code={JSON.stringify(detail, null, 2)} language="json" title={detailTitle} />
        </div>
      ) : null}
    </DrawerShell>
  );
}
