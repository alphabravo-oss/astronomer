import { lazy, Suspense, useState } from "react";
import { ChevronDown, Download, Upload } from "lucide-react";

import { ActionButton } from "@/components/ui/action-button";
import { useDismissable } from "@/components/layout/cluster-scope-controls";
import {
  useClusterKubeconfig,
  type ClusterKubeconfigPermission,
} from "@/lib/hooks/kubernetes-proxy";
import { cn } from "@/lib/utils";
import type { Cluster } from "@/types";

const CreateResourceDialog = lazy(() =>
  import("@/components/resources/create-resource-dialog").then((module) => ({
    default: module.CreateResourceDialog,
  })),
);

/**
 * Header-wide cluster actions rendered next to the ClusterShellLauncher in
 * the topbar whenever a cluster is in scope. Previously "download a
 * kubeconfig" and "apply a manifest" only existed on the cluster overview
 * page, which meant navigating away from whatever resource page you were on
 * (and, for import, first to the specific kind's list page) just to reach
 * them. Both are now one click from anywhere in the cluster.
 */
export function HeaderClusterActions({
  clusterId,
  cluster,
  directPermission,
}: {
  clusterId: string;
  cluster: Pick<Cluster, "name" | "apiServerUrl" | "isLocal"> | undefined;
  directPermission: ClusterKubeconfigPermission;
}) {
  const kubeconfig = useClusterKubeconfig(clusterId, cluster, directPermission);
  const [menuOpen, setMenuOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const closeMenu = () => setMenuOpen(false);
  const menuRef = useDismissable(menuOpen, closeMenu);

  const items: Array<{
    label: string;
    onClick: () => void;
    disabled?: boolean;
    disabledReason?: string;
  }> = [
    {
      label: "Download proxy",
      onClick: () => void kubeconfig.downloadProxy(),
      disabled: kubeconfig.proxyPending,
    },
    {
      label: "Copy proxy",
      onClick: () => void kubeconfig.copyProxy(),
      disabled: kubeconfig.proxyPending,
    },
    {
      label: "Download direct",
      onClick: () => void kubeconfig.downloadDirect(),
      disabled: kubeconfig.directPending || !!kubeconfig.directDisabledReason,
      disabledReason: kubeconfig.directDisabledReason,
    },
  ];

  return (
    <>
      <div ref={menuRef} className="relative">
        <ActionButton
          size="sm"
          icon={<Download className="h-3.5 w-3.5" />}
          onClick={() => setMenuOpen((value) => !value)}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
        >
          Kubeconfig
          <ChevronDown className="h-3 w-3" />
        </ActionButton>
        {menuOpen && (
          <div
            role="menu"
            className="absolute right-0 top-full z-50 mt-1 w-56 rounded-md border border-border bg-popover p-1 shadow-lg"
          >
            {items.map((item) => (
              <button
                key={item.label}
                type="button"
                role="menuitem"
                disabled={item.disabled}
                title={item.disabledReason}
                onClick={() => {
                  if (item.disabled) return;
                  item.onClick();
                  closeMenu();
                }}
                className={cn(
                  "flex w-full items-center gap-2 rounded-sm px-2.5 py-1.5 text-left text-xs text-popover-foreground transition-colors hover:bg-accent",
                  item.disabled && "cursor-not-allowed opacity-50",
                )}
              >
                {item.label}
              </button>
            ))}
          </div>
        )}
      </div>
      <ActionButton
        size="sm"
        icon={<Upload className="h-3.5 w-3.5" />}
        onClick={() => setImportOpen(true)}
        title="Apply one or more Kubernetes manifests to this cluster"
      >
        Import
      </ActionButton>
      {importOpen && (
        <Suspense fallback={<span role="status">Loading YAML importer…</span>}>
          <CreateResourceDialog
            open
            onClose={() => setImportOpen(false)}
            clusterId={clusterId}
            title="Import YAML"
          />
        </Suspense>
      )}
    </>
  );
}
