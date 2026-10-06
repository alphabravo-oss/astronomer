import { CheckCircle, FlaskConical, Plus, XCircle } from "lucide-react";
import { Input } from "@/components/ui/input";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import type { DatasourcesAdmin } from "./-use-datasources-admin";

export function DatasourcesSection({ admin }: { admin: DatasourcesAdmin }) {
  const {
    datasourcesQuery,
    datasources,
    dsError,
    testStatus,
    showAdd,
    setShowAdd,
    addForm,
    addDS,
    cancelAdd,
    runTest,
    setDeleteTarget,
  } = admin;
  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-medium">Prometheus datasources</h2>
        {!showAdd && (
          <ActionButton
            onClick={() => setShowAdd(true)}
            size="sm"
            icon={<Plus className="h-3.5 w-3.5" />}
          >
            Add datasource
          </ActionButton>
        )}
      </div>
      {dsError || datasourcesQuery.isError ? (
        <div className="text-sm text-status-error">
          {dsError ??
            (datasourcesQuery.error instanceof Error
              ? datasourcesQuery.error.message
              : "Failed to load datasources")}
        </div>
      ) : null}
      <div className="border border-border rounded-lg overflow-hidden">
        <Table className="w-full text-sm">
          <TableHeader className="bg-muted/50">
            <TableRow>
              <TableHead className="text-left px-3 py-2">Name</TableHead>
              <TableHead className="text-left px-3 py-2">URL</TableHead>
              <TableHead className="text-left px-3 py-2">Auth</TableHead>
              <TableHead className="text-left px-3 py-2">Status</TableHead>
              <TableHead className="text-right px-3 py-2">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {datasources.map((d) => (
              <TableRow key={d.id} className="border-t border-border">
                <TableCell className="px-3 py-2">{d.name}</TableCell>
                <TableCell className="px-3 py-2 font-mono text-xs text-muted-foreground">
                  {d.url}
                </TableCell>
                <TableCell className="px-3 py-2 text-muted-foreground">
                  {d.hasAuth ? "yes" : "none"}
                </TableCell>
                <TableCell className="px-3 py-2">
                  {testStatus[d.id] ? (
                    <span
                      className={`inline-flex items-center gap-1 text-xs ${testStatus[d.id].ok ? "text-status-success" : "text-status-error"}`}
                    >
                      {testStatus[d.id].ok ? (
                        <CheckCircle className="h-3 w-3" />
                      ) : (
                        <XCircle className="h-3 w-3" />
                      )}
                      <Tooltip content={testStatus[d.id].msg}>
                        <span className="truncate max-w-48">
                          {testStatus[d.id].msg}
                        </span>
                      </Tooltip>
                    </span>
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      unknown
                    </span>
                  )}
                </TableCell>
                <TableCell className="px-3 py-2 text-right">
                  <ActionButton
                    onClick={() => runTest(d.id)}
                    intent="ghost"
                    size="sm"
                    className="mr-2"
                    icon={<FlaskConical className="h-3 w-3" />}
                  >
                    Test
                  </ActionButton>
                  <ActionButton
                    onClick={() => setDeleteTarget(d)}
                    intent="ghost"
                    size="sm"
                    className="text-status-error"
                  >
                    Delete
                  </ActionButton>
                </TableCell>
              </TableRow>
            ))}
            {showAdd && (
              <TableRow className="border-t border-border bg-muted/20">
                <TableCell className="px-3 py-2">
                  <addForm.Field name="name">
                    {(field) => (
                      <Input
                        className="bg-background border border-border rounded-sm px-2 py-1 w-32"
                        placeholder="name"
                        value={field.state.value}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    )}
                  </addForm.Field>
                </TableCell>
                <TableCell className="px-3 py-2">
                  <addForm.Field name="url">
                    {(field) => (
                      <Input
                        className="bg-background border border-border rounded-sm px-2 py-1 w-full font-mono text-xs"
                        placeholder="https://prom..."
                        value={field.state.value}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    )}
                  </addForm.Field>
                </TableCell>
                <TableCell className="px-3 py-2">
                  <addForm.Field name="bearer">
                    {(field) => (
                      <Input
                        className="bg-background border border-border rounded-sm px-2 py-1 w-32"
                        placeholder="Bearer (optional)"
                        value={field.state.value}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    )}
                  </addForm.Field>
                </TableCell>
                <TableCell className="px-3 py-2 text-muted-foreground text-xs">
                  —
                </TableCell>
                <TableCell className="px-3 py-2 text-right whitespace-nowrap">
                  <ActionButton
                    onClick={addDS}
                    intent="ghost"
                    size="sm"
                    className="mr-2"
                    icon={<Plus className="h-3 w-3" />}
                  >
                    Add
                  </ActionButton>
                  <ActionButton onClick={cancelAdd} intent="ghost" size="sm">
                    Cancel
                  </ActionButton>
                </TableCell>
              </TableRow>
            )}
            {!showAdd && datasources.length === 0 && (
              <TableRow className="border-t border-border">
                <TableCell
                  colSpan={5}
                  className="px-3 py-4 text-center text-xs text-muted-foreground"
                >
                  No datasources yet. Use “Add datasource” to connect a
                  Prometheus endpoint.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
    </section>
  );
}
