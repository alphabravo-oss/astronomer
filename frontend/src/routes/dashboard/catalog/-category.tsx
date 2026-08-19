import { cn } from '@/lib/utils';
import type { HelmChartCategory } from '@/types';

export const categories: { key: HelmChartCategory | 'all'; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'monitoring', label: 'Monitoring' },
  { key: 'logging', label: 'Logging' },
  { key: 'security', label: 'Security' },
  { key: 'database', label: 'Database' },
  { key: 'networking', label: 'Networking' },
  { key: 'storage', label: 'Storage' },
  { key: 'messaging', label: 'Messaging' },
  { key: 'ci-cd', label: 'CI/CD' },
  { key: 'other', label: 'Other' },
];

const categoryColors: Record<string, string> = {
  monitoring: 'bg-blue-500/10 text-blue-500',
  logging: 'bg-green-500/10 text-green-500',
  security: 'bg-red-500/10 text-red-500',
  database: 'bg-purple-500/10 text-purple-500',
  networking: 'bg-orange-500/10 text-orange-500',
  storage: 'bg-cyan-500/10 text-cyan-500',
  messaging: 'bg-yellow-500/10 text-yellow-500',
  'ci-cd': 'bg-indigo-500/10 text-indigo-500',
  other: 'bg-muted text-muted-foreground',
};

export function CategoryChip({
  category,
  className,
}: {
  category: string;
  className?: string;
}) {
  return (
    <span className={cn('rounded font-medium', categoryColors[category] || categoryColors.other, className)}>
      {category}
    </span>
  );
}
