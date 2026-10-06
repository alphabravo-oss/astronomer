import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { ActionButton } from "@/components/ui/action-button";
import {
  CheckCircle2,
  Loader2,
  Pencil,
  Plug,
  Trash2,
  XCircle,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { BARE_BUTTON } from "@/lib/bare-button";
import type { ClusterRegistry } from "@/lib/api/cluster-registries";

export type RegistryTestState = "ok" | "fail" | "pending";

function fmt(iso?: string) {
  if (!iso) return "—";
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

export function RegistriesTable({
  registries,
  canWrite,
  reason,
  testStatus,
  testPending,
  onTest,
  onEdit,
  onDelete,
}: {
  registries: ClusterRegistry[];
  canWrite: boolean;
  reason?: string;
  testStatus: Record<string, RegistryTestState>;
  testPending: boolean;
  onTest: (id: string) => void;
  onEdit: (registry: ClusterRegistry) => void;
  onDelete: (registry: ClusterRegistry) => void;
}) {
  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/30 text-xs text-muted-foreground">
          <TableRow>
            <TableHead className="text-left font-medium px-4 py-2.5">
              Registry
            </TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">
              User
            </TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">
              Namespaces
            </TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">
              Default SA
            </TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">
              Last applied
            </TableHead>
            <TableHead className="text-right font-medium px-4 py-2.5">
              Actions
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className="divide-y divide-border">
          {registries.map((r) => (
            <TableRow key={r.id} className="hover:bg-accent/30 align-top">
              <TableCell className="px-4 py-2.5">
                <div className="font-mono text-xs text-foreground break-all">
                  {r.registryUrl}
                </div>
                {r.lastApplyError ? (
                  <div className="text-xs text-status-error mt-1">
                    {r.lastApplyError}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-muted-foreground font-mono">
                {r.username}
              </TableCell>
              <TableCell className="px-4 py-2.5">
                <div className="flex flex-wrap gap-1">
                  {r.namespaces.length === 0 ? (
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded-sm text-xs bg-muted text-muted-foreground border border-border">
                      (all project namespaces)
                    </span>
                  ) : (
                    r.namespaces.map((ns) => (
                      <span
                        key={ns}
                        className="inline-flex items-center px-1.5 py-0.5 rounded-sm text-xs bg-muted text-muted-foreground border border-border"
                      >
                        {ns}
                      </span>
                    ))
                  )}
                </div>
              </TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">
                {r.injectDefaultSa ? "Yes" : "No"}
              </TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">
                <div className="flex items-center gap-2">
                  <span>{fmt(r.lastAppliedAt)}</span>
                  <TestStatusPill state={testStatus[r.id]} />
                </div>
              </TableCell>
              <TableCell className="px-4 py-2.5">
                <div className="flex items-center justify-end gap-1.5">
                  <RegistryRowActions
                    canWrite={canWrite}
                    reason={reason}
                    testDisabled={testPending && testStatus[r.id] === "pending"}
                    onTest={() => onTest(r.id)}
                    onEdit={() => onEdit(r)}
                    onDelete={() => onDelete(r)}
                  />
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

const rowActionClass =
  "inline-flex h-7 items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50 font-normal";

export function RegistryRowActions({
  canWrite,
  reason,
  testDisabled,
  onTest,
  onEdit,
  onDelete,
}: {
  canWrite: boolean;
  reason?: string;
  testDisabled: boolean;
  onTest: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const gated = {
    disabled: !canWrite,
    disabledReason: canWrite ? undefined : reason,
  };
  return (
    <>
      <ActionButton
        {...BARE_BUTTON}
        tooltip="Test reachability"
        onClick={onTest}
        disabled={testDisabled}
        className={cn(rowActionClass, "gap-1 px-2 text-xs")}
      >
        <Plug className="h-3.5 w-3.5" />
        Test
      </ActionButton>
      <ActionButton
        {...BARE_BUTTON}
        tooltip={canWrite ? "Edit" : undefined}
        aria-label="Edit"
        onClick={onEdit}
        className={cn(rowActionClass, "w-7")}
        {...gated}
      >
        <Pencil className="h-3.5 w-3.5" />
      </ActionButton>
      <ActionButton
        {...BARE_BUTTON}
        tooltip={canWrite ? "Delete" : undefined}
        aria-label="Delete"
        onClick={onDelete}
        className={cn(
          rowActionClass,
          "w-7 hover:bg-status-error/10 hover:text-status-error",
        )}
        {...gated}
      >
        <Trash2 className="h-3.5 w-3.5" />
      </ActionButton>
    </>
  );
}

export function TestStatusPill({
  state,
}: {
  state?: "ok" | "fail" | "pending";
}) {
  if (!state) return null;
  if (state === "pending") {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-[10px] border bg-status-info/10 text-status-info border-status-info/20">
        <Loader2 className="h-3 w-3 animate-spin" /> Testing
      </span>
    );
  }
  if (state === "ok") {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-[10px] border bg-status-success/10 text-status-success border-status-success/20">
        <CheckCircle2 className="h-3 w-3" /> Reachable
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-[10px] border bg-status-error/10 text-status-error border-status-error/20">
      <XCircle className="h-3 w-3" /> Failed
    </span>
  );
}
