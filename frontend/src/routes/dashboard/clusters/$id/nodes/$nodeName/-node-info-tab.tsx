import { Table, TableBody, TableCell, TableRow } from "@/components/ui/operator-table";
import type { NodeInfo } from "@/types";

/**
 * Compact key/value matrix of kubelet-reported node metadata. Kept on
 * operator-table (not DataTable) — a fixed set of label/value rows with no
 * sort/filter/search need, which is exactly the "compact detail matrix"
 * case operator-table is reserved for.
 */
export function InfoTab({ nodeInfo }: { nodeInfo: NodeInfo }) {
  return (
    <div className="bg-card border border-border rounded-lg overflow-hidden">
      <Table className="w-full">
        <TableBody className="divide-y divide-border">
          {(
            [
              ["Machine ID", nodeInfo.machineId],
              ["System UUID", nodeInfo.systemUuid],
              ["Boot ID", nodeInfo.bootId],
              ["Kernel Version", nodeInfo.kernelVersion],
              ["OS Image", nodeInfo.osImage],
              ["Container Runtime", nodeInfo.containerRuntimeVersion],
              ["Kubelet Version", nodeInfo.kubeletVersion],
              ["Kube-Proxy Version", nodeInfo.kubeProxyVersion],
              ["Operating System", nodeInfo.operatingSystem],
              ["Architecture", nodeInfo.architecture],
            ] as const
          ).map(([label, value]) => (
            <TableRow key={label}>
              <TableCell className="px-4 py-2.5 text-xs font-medium text-muted-foreground w-48">
                {label}
              </TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-foreground font-mono">
                {value || "-"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
