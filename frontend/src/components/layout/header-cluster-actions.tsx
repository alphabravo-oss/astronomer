import { lazy, Suspense, useState } from "react";
import {
  ChevronDown,
  Copy,
  Download,
  Settings2,
  TerminalSquare,
  Upload,
} from "lucide-react";

import {
  useClusterKubeconfig,
  type ClusterKubeconfigPermission,
} from "@/lib/hooks/kubernetes-proxy";
import { openClusterShellWindow } from "@/lib/window-manager-store";
import type { Cluster } from "@/types";
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
  ScopeChip,
} from "@/ui";

const CreateResourceDialog = lazy(() =>
  import("@/components/resources/create-resource-dialog").then((module) => ({
    default: module.CreateResourceDialog,
  })),
);

/**
 * One "Cluster actions" menu in the header for everything that used to be
 * four icon buttons: cluster shell, kubeconfig (proxy download/copy, direct
 * download) and YAML import.
 */
export function HeaderClusterActions({
  clusterId,
  cluster,
  directPermission,
  shellDisabledReason,
}: {
  clusterId: string;
  cluster: Pick<Cluster, "name" | "apiServerUrl" | "isLocal"> | undefined;
  directPermission: ClusterKubeconfigPermission;
  shellDisabledReason?: string;
}) {
  const kubeconfig = useClusterKubeconfig(clusterId, cluster, directPermission);
  const [importOpen, setImportOpen] = useState(false);
  const clusterName = cluster?.name;

  return (
    <>
      <Menu modal={false}>
        <MenuTrigger asChild>
          <ScopeChip
            aria-label="Cluster actions"
            icon={<Settings2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
            className="max-w-none"
          >
            <span className="flex items-center gap-1">
              <span className="hidden 2xl:inline">Cluster actions</span>
              <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
            </span>
          </ScopeChip>
        </MenuTrigger>
        <MenuContent className="w-64">
          <MenuItem
            disabled={!!shellDisabledReason}
            title={shellDisabledReason}
            onSelect={() => openClusterShellWindow(clusterId, clusterName)}
          >
            <TerminalSquare className="h-4 w-4" />
            Open cluster shell
            <span className="ml-auto font-mono text-micro text-muted-foreground">
              Ctrl+`
            </span>
          </MenuItem>
          <MenuSeparator />
          <MenuLabel>Kubeconfig</MenuLabel>
          <MenuItem
            disabled={kubeconfig.proxyPending}
            onSelect={() => void kubeconfig.downloadProxy()}
          >
            <Download className="h-4 w-4" />
            Download proxy kubeconfig
          </MenuItem>
          <MenuItem
            disabled={kubeconfig.proxyPending}
            onSelect={() => void kubeconfig.copyProxy()}
          >
            <Copy className="h-4 w-4" />
            Copy proxy kubeconfig
          </MenuItem>
          <MenuItem
            disabled={
              kubeconfig.directPending || !!kubeconfig.directDisabledReason
            }
            title={kubeconfig.directDisabledReason}
            onSelect={() => void kubeconfig.downloadDirect()}
          >
            <Download className="h-4 w-4" />
            Download direct kubeconfig
          </MenuItem>
          <MenuSeparator />
          <MenuItem onSelect={() => setImportOpen(true)}>
            <Upload className="h-4 w-4" />
            Import YAML…
          </MenuItem>
        </MenuContent>
      </Menu>
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
