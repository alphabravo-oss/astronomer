import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { RemoteProjectPicker } from "@/components/projects/remote-project-picker";
import { Input } from "@/components/ui/input";
import { usePermissionDecision } from "@/lib/permission-hooks";

/** Known IDs remain filterable even when the operator cannot enumerate targets. */
export function AuditScopeFilter({
  kind,
  value,
  onChange,
}: {
  kind: "cluster" | "project";
  value: string;
  onChange: (id: string) => void;
}) {
  const read = usePermissionDecision(
    kind === "cluster" ? "clusters" : "projects",
    "read",
  );
  const label = kind === "cluster" ? "Cluster" : "Project";
  return (
    <div className="space-y-1">
      <Input
        aria-label={`${label} ID`}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={`Any ${kind} (or enter ID)`}
      />
      {read.allowed &&
        (kind === "cluster" ? (
          <RemoteClusterPicker
            value={value}
            onChange={onChange}
            ariaLabel="Find cluster"
          />
        ) : (
          <RemoteProjectPicker
            value={value}
            onChange={onChange}
            ariaLabel="Find project"
          />
        ))}
    </div>
  );
}
