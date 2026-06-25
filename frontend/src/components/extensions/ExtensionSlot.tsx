'use client';

// §HostMounts — ExtensionSlot: the ONE integration point a host page adds per
// mount location. `<ExtensionSlot point="clusterTab" context={{clusterId}} />`
// reads the registry (via useExtensionMounts), and for every enabled mount at
// that point renders it — each wrapped in a per-extension error boundary so a
// broken/hostile extension degrades to a placeholder and never crashes the
// host shell (design doc §HostMounts).
//
// Tier selection is derived, not authored: tier 1 (render.declarative) renders
// with first-party React; tier 2 (render.bundle) renders inside a sandboxed
// iframe. The concrete renderers (DeclarativeWidget / SandboxedExtension) ship
// in later items; until they are wired this slot mounts a render-only
// placeholder/loader that shows the mount exists and which tier it is. The
// placeholder is intentionally text-only (no third-party HTML) — the same
// fail-closed posture the real renderers must keep.

import type { ReactNode } from 'react';
import { Loader2 } from 'lucide-react';
import { useExtensionRuntime, useExtensionMounts } from './ExtensionProvider';
import { ExtensionErrorBoundary } from './ExtensionErrorBoundary';
import { DeclarativeWidget } from './DeclarativeWidget';
import { SandboxedExtension } from './SandboxedExtension';
import type {
  ExtensionContext,
  ExtensionMount,
  ExtensionPointKind,
} from '@/lib/api/extensions';

// A host page may inject the real renderer once it ships (item 3/4). When no
// renderer is supplied the slot falls back to the placeholder below, so wiring
// the 4 mount points (this item) is decoupled from rendering their content.
export type ExtensionRenderer = (
  mount: ExtensionMount,
  context: ExtensionContext | undefined,
) => ReactNode;

export interface ExtensionSlotProps {
  point: ExtensionPointKind;
  // Route-derived ids handed to each mount's renderer / Tier-2 handshake.
  context?: ExtensionContext;
  // Optional first-party renderer (DeclarativeWidget / SandboxedExtension).
  render?: ExtensionRenderer;
  // Optional wrapper className for the slot container.
  className?: string;
}

// Derive the tier off the mount's render block. Never authored here — read from
// what the manifest projection shipped (design doc: "Tier is derived").
function tierOf(mount: ExtensionMount): 1 | 2 {
  if (mount.render?.bundle) return 2;
  return 1;
}

// One mount's content, evaluated inside the error boundary's subtree. The
// renderer (or the placeholder fallback) is invoked here during *this*
// component's render — not eagerly in the parent's map — so a throw is caught
// by the enclosing ExtensionErrorBoundary instead of escaping to the host page.
function MountContent({
  mount,
  context,
  render,
}: {
  mount: ExtensionMount;
  context: ExtensionContext | undefined;
  render?: ExtensionRenderer;
}) {
  if (render) return <>{render(mount, context)}</>;
  // Default rendering, tier derived from the mount's render block:
  //   Tier 1 (render.declarative) -> first-party React via the data proxy.
  //   Tier 2 (render.bundle)      -> sandboxed iframe + §BridgeProtocol.
  // A point with neither falls back to the text-only placeholder (legacy entry).
  const declarative = mount.render?.declarative;
  if (declarative) {
    return (
      <DeclarativeWidget extensionName={mount.extension} spec={declarative} context={context} />
    );
  }
  if (mount.render?.bundle) {
    return <SandboxedExtension mount={mount} context={context} />;
  }
  return <MountPlaceholder mount={mount} />;
}

// Render-only placeholder for a single mount until the concrete renderers land.
// Text nodes only; no dangerouslySetInnerHTML.
function MountPlaceholder({ mount }: { mount: ExtensionMount }) {
  const tier = tierOf(mount);
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium text-foreground">
          {mount.label || mount.displayName || mount.extension}
        </span>
        <span className="rounded border border-border px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
          {tier === 2 ? 'iframe' : 'widget'}
        </span>
      </div>
      <div className="mt-3 flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        <span>Loading extension…</span>
      </div>
    </div>
  );
}

export function ExtensionSlot({ point, context, render, className }: ExtensionSlotProps) {
  const { isLoading } = useExtensionRuntime();
  const mounts = useExtensionMounts(point);

  // Nothing to mount: render nothing (and never a stray heading/wrapper) so the
  // host page collapses cleanly on installs with no extensions. While the
  // registry is still loading we also render nothing — the host page's own
  // content is the priority; extension mounts are additive.
  if (isLoading || mounts.length === 0) return null;

  return (
    <div className={className} data-extension-slot={point}>
      {mounts.map((mount) => (
        <ExtensionErrorBoundary key={`${mount.extension}:${mount.pointId}`} extensionName={mount.extension}>
          <MountContent mount={mount} context={context} render={render} />
        </ExtensionErrorBoundary>
      ))}
    </div>
  );
}
