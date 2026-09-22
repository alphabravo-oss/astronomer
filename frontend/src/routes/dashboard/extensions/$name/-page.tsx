import { getRouteApi } from "@tanstack/react-router";
import { Puzzle } from "lucide-react";

import { PageHeader, PageShell } from "@/components/ui/page";
import { EmptyState, LoadingState } from "@/components/ui/empty-state";
import {
  useExtensionMounts,
  useExtensionRuntime,
} from "@/components/extensions/ExtensionProvider";
import { ExtensionErrorBoundary } from "@/components/extensions/ExtensionErrorBoundary";
import { DeclarativeWidget } from "@/components/extensions/DeclarativeWidget";
import { SandboxedExtension } from "@/components/extensions/SandboxedExtension";

/**
 * Host-fixed full-page mount for an extension's sidebar entry
 * (§HostMounts — see components/extensions/ExtensionNavItems.tsx, which
 * derives the sidebar `href` from the extension name and expects this exact
 * route to exist). Renders the same mount data the sidebar/slot machinery
 * already indexes, using the two first-party renderers (DeclarativeWidget
 * for tier 1, SandboxedExtension for tier 2) wrapped in the shared
 * ExtensionErrorBoundary, so a broken/hostile extension degrades to a
 * boundary fallback rather than crashing the page.
 */
const routeApi = getRouteApi("/dashboard/extensions/$name/");

export function ExtensionPage() {
  const { name } = routeApi.useParams();
  const { isLoading } = useExtensionRuntime();
  const mounts = useExtensionMounts("sidebar");
  const mount = mounts.find((item) => item.extension === name);

  if (isLoading) {
    return (
      <PageShell>
        <LoadingState title="Loading extension" />
      </PageShell>
    );
  }
  if (!mount) {
    // Not a router notFound(): that bubbles through the dashboard error
    // boundary as a logged error even though it's an expected case (an
    // extension name that isn't installed, isn't enabled, or has no
    // sidebar mount) — this inline panel keeps the same outcome silent.
    return (
      <PageShell>
        <EmptyState
          icon={Puzzle}
          title="Extension not available"
          description={`"${name}" isn't installed, isn't enabled, or has no page to show.`}
          actionLabel="Back to extensions"
          actionHref="/dashboard/extensions"
        />
      </PageShell>
    );
  }

  const title = mount.label || mount.displayName || mount.extension;

  return (
    <PageShell>
      <PageHeader title={title} />
      <ExtensionErrorBoundary extensionName={mount.extension}>
        {mount.render?.declarative ? (
          <DeclarativeWidget
            extensionName={mount.extension}
            spec={mount.render.declarative}
          />
        ) : mount.render?.bundle ? (
          <SandboxedExtension mount={mount} />
        ) : (
          <p className="text-sm text-muted-foreground">
            This extension has no page content configured.
          </p>
        )}
      </ExtensionErrorBoundary>
    </PageShell>
  );
}
