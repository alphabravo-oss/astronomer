'use client';

/**
 * Inline provider badge for cloud-credential rows / cards. Lucide doesn't
 * ship cloud-vendor brand marks so we use a neutral icon + a coloured
 * background; this keeps things accessible without pulling in a brand-asset
 * SDK just for three icons.
 */
import { Cloud, type LucideIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { CloudProvider } from '@/lib/api/project-detail';

const providerMeta: Record<
  CloudProvider,
  { label: string; tint: string; icon: LucideIcon }
> = {
  aws: { label: 'AWS', tint: 'bg-orange-500/10 text-orange-600 dark:text-orange-400', icon: Cloud },
  gcp: { label: 'GCP', tint: 'bg-blue-500/10 text-blue-600 dark:text-blue-400', icon: Cloud },
  azure: { label: 'Azure', tint: 'bg-sky-500/10 text-sky-600 dark:text-sky-400', icon: Cloud },
  generic: { label: 'Generic', tint: 'bg-muted text-muted-foreground', icon: Cloud },
};

export function ProviderBadge({ provider }: { provider: CloudProvider }) {
  const meta = providerMeta[provider] ?? providerMeta.generic;
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium',
        meta.tint,
      )}
    >
      <Icon className="h-3 w-3" />
      {meta.label}
    </span>
  );
}
