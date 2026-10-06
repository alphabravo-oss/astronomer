import { Plus, Save, Trash2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { ActionButton } from "@/components/ui/action-button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { LoadingSkeleton } from "@/components/form/loading-skeleton";
import type { WidgetScope, WidgetType } from "@/lib/api/dashboards";
import { DEFAULT_SPEC_BY_TYPE } from "./-widget-defaults";
import type { WidgetsAdmin } from "./-use-widgets-admin";

export function WidgetsSection({ admin }: { admin: WidgetsAdmin }) {
  const {
    widgets,
    loading,
    error,
    editing,
    form,
    saving,
    startCreate,
    startEdit,
    cancel,
    setDeleteTarget,
  } = admin;
  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-medium">Widgets</h2>
        {!editing ? (
          <ActionButton
            onClick={startCreate}
            intent="primary"
            size="sm"
            icon={<Plus className="h-4 w-4" />}
          >
            New widget
          </ActionButton>
        ) : null}
      </div>

      {editing ? (
        <div className="border border-border rounded-lg p-4 bg-card space-y-3">
          <form.AppForm>
            <form.FormErrorSummary serverError={error} />
          </form.AppForm>
          <div className="grid grid-cols-2 gap-3">
            <label className="text-sm">
              <div className="text-muted-foreground mb-1">Name</div>
              <form.Field name="name">
                {(field) => (
                  <Input
                    className="w-full bg-background border border-border rounded-sm px-2 py-1"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                )}
              </form.Field>
            </label>
            <label className="text-sm">
              <div className="text-muted-foreground mb-1">Widget type</div>
              <form.Field name="widgetType">
                {(field) => (
                  <Select
                    className="w-full bg-background border border-border rounded-sm px-2 py-1"
                    value={field.state.value}
                    onChange={(e) => {
                      const t = e.target.value as WidgetType;
                      field.handleChange(t);
                      // Switching types re-seeds the spec text, exactly like
                      // the old setSpecText(DEFAULT_SPEC_BY_TYPE[t]).
                      form.setFieldValue("specText", DEFAULT_SPEC_BY_TYPE[t]);
                    }}
                    onBlur={field.handleBlur}
                  >
                    <option value="prom_sparkline">Prometheus sparkline</option>
                    <option value="prom_stat">Prometheus stat</option>
                    <option value="grafana_panel">Grafana panel</option>
                    <option value="url_iframe">URL iframe</option>
                  </Select>
                )}
              </form.Field>
            </label>
            <label className="text-sm">
              <div className="text-muted-foreground mb-1">Scope</div>
              <form.Field name="scope">
                {(field) => (
                  <Select
                    className="w-full bg-background border border-border rounded-sm px-2 py-1"
                    value={field.state.value}
                    onChange={(e) =>
                      field.handleChange(e.target.value as WidgetScope)
                    }
                    onBlur={field.handleBlur}
                  >
                    <option value="global">Global</option>
                    <option value="cluster">Cluster</option>
                    <option value="project">Project</option>
                  </Select>
                )}
              </form.Field>
            </label>
            <label className="text-sm">
              <div className="text-muted-foreground mb-1">Refresh seconds</div>
              <form.Field name="refreshSeconds">
                {(field) => (
                  <Input
                    type="number"
                    className="w-full bg-background border border-border rounded-sm px-2 py-1"
                    value={field.state.value}
                    onChange={(e) =>
                      field.handleChange(parseInt(e.target.value, 10) || 60)
                    }
                    onBlur={field.handleBlur}
                  />
                )}
              </form.Field>
            </label>
            <label className="text-sm col-span-2">
              <div className="text-muted-foreground mb-1">
                Grid (x, y, w, h)
              </div>
              <form.Field name="grid">
                {(field) => (
                  <div className="flex gap-2">
                    {(["x", "y", "w", "h"] as const).map((k) => (
                      <Input
                        key={k}
                        type="number"
                        className="w-20 bg-background border border-border rounded-sm px-2 py-1"
                        value={field.state.value[k] ?? 0}
                        onChange={(e) =>
                          field.handleChange({
                            ...field.state.value,
                            [k]: parseInt(e.target.value, 10) || 0,
                          })
                        }
                        onBlur={field.handleBlur}
                      />
                    ))}
                  </div>
                )}
              </form.Field>
            </label>
          </div>
          <label className="text-sm block">
            <div className="text-muted-foreground mb-1">Spec (JSON)</div>
            <form.Field name="specText">
              {(field) => (
                <Textarea
                  rows={10}
                  className="w-full font-mono text-xs bg-background border border-border rounded-sm p-2"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                />
              )}
            </form.Field>
          </label>
          <div className="flex gap-2">
            <ActionButton
              onClick={() => void form.handleSubmit()}
              disabled={saving}
              loading={saving}
              intent="primary"
              size="sm"
              icon={<Save className="h-4 w-4" />}
            >
              Save
            </ActionButton>
            <ActionButton onClick={cancel} size="sm">
              Cancel
            </ActionButton>
          </div>
        </div>
      ) : null}

      {loading ? (
        <LoadingSkeleton label="Loading widgets" lines={3} />
      ) : widgets.length === 0 ? (
        <div className="text-sm text-muted-foreground">
          No widgets defined. Click "New widget" to add one.
        </div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted/50">
              <TableRow>
                <TableHead className="text-left px-3 py-2">Name</TableHead>
                <TableHead className="text-left px-3 py-2">Type</TableHead>
                <TableHead className="text-left px-3 py-2">Scope</TableHead>
                <TableHead className="text-left px-3 py-2">Refresh</TableHead>
                <TableHead className="text-left px-3 py-2">Enabled</TableHead>
                <TableHead className="text-right px-3 py-2">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {widgets.map((w) => (
                <TableRow key={w.id} className="border-t border-border">
                  <TableCell className="px-3 py-2">{w.name}</TableCell>
                  <TableCell className="px-3 py-2 text-muted-foreground">
                    {w.widgetType}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-muted-foreground">
                    {w.scope}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-muted-foreground">
                    {w.refreshSeconds}s
                  </TableCell>
                  <TableCell className="px-3 py-2">
                    {w.enabled ? "yes" : "no"}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-right">
                    <ActionButton
                      onClick={() => startEdit(w)}
                      intent="ghost"
                      size="sm"
                      className="mr-2"
                    >
                      Edit
                    </ActionButton>
                    <ActionButton
                      onClick={() => setDeleteTarget(w)}
                      intent="ghost"
                      size="sm"
                      className="text-status-error"
                      icon={<Trash2 className="h-3 w-3" />}
                    >
                      Delete
                    </ActionButton>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  );
}
