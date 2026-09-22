import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";

import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import {
  KeyValueTable,
  Section,
} from "@/components/resources/resource-overview-primitives";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import { k8sPatch } from "@/lib/api/kubernetes-proxy";
import { extractApiErrorMessage } from "@/lib/api/errors";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { queryKeys } from "@/lib/query-keys";
import { toastSuccess } from "@/lib/toast";

// A DNS-subdomain prefix (e.g. "app.kubernetes.io/") is optional; the name
// part after the slash is what Kubernetes actually bounds at 63 characters.
const NAME_PART_RE = /^[a-zA-Z0-9]([-a-zA-Z0-9_.]*[a-zA-Z0-9])?$/;
const DNS_SUBDOMAIN_RE =
  /^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/i;

export function validateLabelKey(key: string): string | null {
  if (!key) return "Key is required.";
  const slash = key.indexOf("/");
  const prefix = slash === -1 ? undefined : key.slice(0, slash);
  const namePart = slash === -1 ? key : key.slice(slash + 1);
  if (prefix !== undefined) {
    if (prefix.length === 0 || prefix.length > 253 || !DNS_SUBDOMAIN_RE.test(prefix)) {
      return "Key prefix must be a DNS subdomain of at most 253 characters.";
    }
  }
  if (!namePart || namePart.length > 63 || !NAME_PART_RE.test(namePart)) {
    return "Key must be alphanumeric (may include '-', '_', '.'), at most 63 characters.";
  }
  return null;
}

export function validateLabelValue(value: string): string | null {
  if (!value) return null;
  if (value.length > 63 || !NAME_PART_RE.test(value)) {
    return "Value must be alphanumeric (may include '-', '_', '.'), at most 63 characters.";
  }
  return null;
}

interface Row {
  id: number;
  key: string;
  value: string;
}

function toRows(map: Record<string, string>, nextId: () => number): Row[] {
  return Object.entries(map).map(([key, value]) => ({
    id: nextId(),
    key,
    value,
  }));
}

/** JSON merge patch value for one map: current entries plus `null` for every
 * key present in `original` but no longer present in `rows` (removed). */
function buildMergeMap(
  original: Record<string, string>,
  rows: Row[],
): Record<string, string | null> {
  const next: Record<string, string | null> = {};
  const keptKeys = new Set(rows.filter((row) => row.key).map((row) => row.key));
  for (const row of rows) {
    if (row.key) next[row.key] = row.value;
  }
  for (const key of Object.keys(original)) {
    if (!keptKeys.has(key)) next[key] = null;
  }
  return next;
}

function RowEditor({
  title,
  rows,
  onChange,
}: {
  title: string;
  rows: Row[];
  onChange: (rows: Row[]) => void;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-xs font-semibold text-muted-foreground">
          {title}
        </h3>
        <ActionButton
          size="sm"
          intent="ghost"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => onChange([...rows, { id: Date.now() + rows.length, key: "", value: "" }])}
        >
          Add
        </ActionButton>
      </div>
      {rows.length === 0 ? (
        <p className="text-xs text-muted-foreground">None</p>
      ) : (
        <div className="space-y-1.5">
          {rows.map((row) => (
            <div key={row.id} className="flex items-center gap-2">
              <Input
                aria-label={`${title} key`}
                value={row.key}
                placeholder="key"
                className="font-mono text-xs"
                onChange={(e) =>
                  onChange(
                    rows.map((r) =>
                      r.id === row.id ? { ...r, key: e.target.value } : r,
                    ),
                  )
                }
              />
              <Input
                aria-label={`${title} value`}
                value={row.value}
                placeholder="value"
                className="font-mono text-xs"
                onChange={(e) =>
                  onChange(
                    rows.map((r) =>
                      r.id === row.id ? { ...r, value: e.target.value } : r,
                    ),
                  )
                }
              />
              <ActionButton
                size="icon"
                intent="ghost"
                aria-label={`Remove ${row.key || "row"}`}
                onClick={() => onChange(rows.filter((r) => r.id !== row.id))}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </ActionButton>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function LabelsAnnotationsEditor({
  clusterId,
  resourceType,
  namespace,
  name,
  labels,
  annotations,
}: {
  clusterId: string;
  resourceType: string;
  namespace?: string;
  name: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
}) {
  const permissions = useClusterResourcePermissions(clusterId, resourceType);
  const queryClient = useQueryClient();
  const nextId = useRef(0);
  const [editing, setEditing] = useState(false);
  const [labelRows, setLabelRows] = useState<Row[]>([]);
  const [annotationRows, setAnnotationRows] = useState<Row[]>([]);
  const [validationError, setValidationError] = useState<string | null>(null);

  const patchMutation = useMutation({
    mutationFn: (body: unknown) =>
      k8sPatch(
        clusterId,
        k8sResourcePath(resourceType, name, namespace),
        body,
        "merge",
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all });
      toastSuccess("Labels and annotations updated");
      setEditing(false);
      setValidationError(null);
    },
    onError: (error: unknown) => {
      setValidationError(
        extractApiErrorMessage(error) ?? "Failed to update labels/annotations",
      );
    },
  });

  const canEdit = permissions.update.allowed;

  function startEdit() {
    setLabelRows(toRows(labels, () => nextId.current++));
    setAnnotationRows(toRows(annotations, () => nextId.current++));
    setValidationError(null);
    setEditing(true);
  }

  function cancelEdit() {
    setEditing(false);
    setValidationError(null);
  }

  function submit() {
    for (const row of [...labelRows, ...annotationRows]) {
      if (!row.key && !row.value) continue;
      const keyError = validateLabelKey(row.key);
      if (keyError) {
        setValidationError(keyError);
        return;
      }
      const valueError = validateLabelValue(row.value);
      if (valueError) {
        setValidationError(valueError);
        return;
      }
    }
    patchMutation.mutate({
      metadata: {
        labels: buildMergeMap(labels, labelRows),
        annotations: buildMergeMap(annotations, annotationRows),
      },
    });
  }

  return (
    <Section title="Labels & Annotations">
      <div className="mb-3 flex justify-end">
        {editing ? (
          <div className="flex gap-2">
            <ActionButton size="sm" onClick={cancelEdit}>
              Cancel
            </ActionButton>
            <ActionButton
              size="sm"
              intent="primary"
              onClick={submit}
              loading={patchMutation.isPending}
            >
              Save
            </ActionButton>
          </div>
        ) : (
          <ActionButton
            size="sm"
            icon={<Pencil className="h-3.5 w-3.5" />}
            onClick={startEdit}
            disabled={!canEdit}
            disabledReason={permissionDeniedReason(permissions.update)}
          >
            Edit
          </ActionButton>
        )}
      </div>

      {validationError && (
        <p role="alert" className="mb-3 text-xs text-status-error">
          {validationError}
        </p>
      )}

      {editing ? (
        <div className="space-y-4">
          <RowEditor title="Labels" rows={labelRows} onChange={setLabelRows} />
          <RowEditor
            title="Annotations"
            rows={annotationRows}
            onChange={setAnnotationRows}
          />
        </div>
      ) : (
        <div className="space-y-4">
          <div>
            <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">
              Labels
            </h3>
            <KeyValueTable entries={Object.entries(labels)} />
          </div>
          <div>
            <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">
              Annotations
            </h3>
            <KeyValueTable entries={Object.entries(annotations)} />
          </div>
        </div>
      )}
    </Section>
  );
}
