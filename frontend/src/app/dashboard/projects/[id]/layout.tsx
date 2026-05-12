'use client';

/**
 * Project detail layout — adds the tab strip shared by every sub-route under
 * `/dashboard/projects/[id]/`. Each tab is its own page so deep-links keep
 * working; the layout just renders the project header + nav and slots in
 * the active page below.
 *
 * Tabs:
 *   Overview · Policy · Cloud Credentials · Quota
 *
 * The Overview tab is the historical default. The three new tabs (Policy /
 * Cloud Credentials / Quota) ship as part of the project-detail-tabs sprint.
 * Adding more is a one-line change to the `tabs` array.
 */
import { use } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { ArrowLeft, FolderKanban, Shield, KeyRound, Gauge, Loader2, LayoutDashboard } from 'lucide-react';
import { useProject } from '@/lib/hooks';
import { cn } from '@/lib/utils';

interface ProjectLayoutProps {
  children: React.ReactNode;
  // Next.js 16: route params arrive as a Promise we unwrap with `use`.
  params: Promise<{ id: string }>;
}

const tabs = [
  { key: 'overview', label: 'Overview', icon: LayoutDashboard, segment: '' },
  { key: 'policy', label: 'Policy', icon: Shield, segment: '/policy' },
  { key: 'cloud-credentials', label: 'Cloud Credentials', icon: KeyRound, segment: '/cloud-credentials' },
  { key: 'quota', label: 'Quota', icon: Gauge, segment: '/quota' },
] as const;

export default function ProjectDetailLayout({ children, params }: ProjectLayoutProps) {
  const { id } = use(params);
  const pathname = usePathname();
  const { data: project, isLoading } = useProject(id);

  const base = `/dashboard/projects/${id}`;

  // Match the tab whose full path is the longest prefix of the current URL.
  // Overview matches when nothing else does (i.e. we're sitting on the bare
  // /projects/[id] route or any unknown nested path).
  const activeKey = (() => {
    const remaining = pathname.startsWith(base) ? pathname.slice(base.length) : '';
    const match = tabs
      .filter((t) => t.segment && (remaining === t.segment || remaining.startsWith(`${t.segment}/`)))
      .sort((a, b) => b.segment.length - a.segment.length)[0];
    return match?.key ?? 'overview';
  })();

  return (
    <div className="space-y-6">
      <Link
        href="/dashboard/projects"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Projects
      </Link>

      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Project</p>
          <div className="flex items-center gap-2 mt-1">
            <FolderKanban className="h-5 w-5 text-muted-foreground" />
            {isLoading ? (
              <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
            ) : (
              <h1 className="text-2xl font-semibold text-foreground tracking-tight truncate">
                {project?.displayName || project?.name || 'Project'}
              </h1>
            )}
          </div>
          {project?.description && (
            <p className="text-sm text-muted-foreground mt-1 max-w-3xl">{project.description}</p>
          )}
        </div>
      </div>

      <div className="border-b border-border">
        <nav className="flex gap-6">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            const href = `${base}${tab.segment}`;
            const active = activeKey === tab.key;
            return (
              <Link
                key={tab.key}
                href={href}
                className={cn(
                  'flex items-center gap-2 pb-3 text-sm font-medium border-b-2 transition-colors',
                  active
                    ? 'border-foreground text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground',
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </Link>
            );
          })}
        </nav>
      </div>

      <div className="animate-fade-in">{children}</div>
    </div>
  );
}
