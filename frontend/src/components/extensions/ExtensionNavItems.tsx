'use client';

// §HostMounts mount point 1 — sidebar nav items.
//
// The sidebar appends one nav link per enabled `sidebar` mount. The route is
// host-fixed under /dashboard/extensions/{name} (the manifest validator already
// forces this prefix), so the path the extension ships is advisory only — we
// derive the href from the extension name to keep navigation inside the host
// allowlist. Render-only: a label + a Link, no third-party JS, no error surface
// large enough to crash the nav (a missing label degrades to the name).

import { Link } from '@/lib/link';
import { Puzzle } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useExtensionMounts } from './ExtensionProvider';

// Host-fixed sidebar route for an extension's full-page mount. Kept here so the
// sidebar and any future deep-link share one definition.
export function extensionSidebarHref(extension: string): string {
  return `/dashboard/extensions/${encodeURIComponent(extension)}`;
}

export function ExtensionNavItems({
  pathname,
  collapsed,
}: {
  pathname: string;
  collapsed: boolean;
}) {
  const mounts = useExtensionMounts('sidebar');
  // Collapse the whole section (header included) when no extension declares a
  // sidebar mount, so the nav is unchanged on installs with no extensions.
  if (mounts.length === 0) return null;

  return (
    <div className="mt-1">
      {!collapsed && (
        <div className="px-3 py-2 text-sm font-semibold text-muted-foreground">Extensions</div>
      )}
      <div className={collapsed ? 'space-y-0.5' : 'space-y-px'}>
        {mounts.map((mount) => {
        const href = extensionSidebarHref(mount.extension);
        const active = pathname === href || pathname.startsWith(`${href}/`);
        const label = mount.label || mount.displayName || mount.extension;
        if (collapsed) {
          return (
            <Link
              key={mount.extension}
              href={href}
              className={cn('nav-item group justify-center px-0', active && 'active')}
              title={label}
            >
              <Puzzle
                className={cn(
                  'h-4 w-4 flex-shrink-0',
                  active ? 'text-foreground' : 'text-muted-foreground group-hover:text-foreground',
                )}
              />
            </Link>
          );
        }
        return (
          <Link
            key={mount.extension}
            href={href}
            className={cn(
              'flex items-center gap-2 px-3 py-1.5 mx-1 rounded-md text-sm transition-colors',
              active
                ? 'bg-accent text-foreground font-medium'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent/50',
            )}
          >
            <Puzzle className={cn('h-4 w-4 flex-shrink-0', active ? 'text-foreground' : 'text-muted-foreground')} />
            <span className="truncate flex-1">{label}</span>
          </Link>
        );
        })}
      </div>
    </div>
  );
}
