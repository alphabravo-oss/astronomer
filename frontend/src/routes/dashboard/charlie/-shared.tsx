import type { EmptyStateActionProps } from "@/components/ui/empty-state";
import { QueryStates, type QueryState } from "@/components/ui/query-states";
import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";

export function filterEmptyAction(
  active: boolean,
  onClear: () => void,
): EmptyStateActionProps | { terminal: true } {
  return active
    ? { actionLabel: "Clear filters", onAction: onClear }
    : { terminal: true };
} // filtered-out: "Clear filters"; else terminal

export function FilterField({
  label,
  value,
  onChange,
  options,
  type = "text",
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options?: string[];
  type?: "text" | "date";
}) {
  return (
    <label className="min-w-36 space-y-1 text-xs">
      <span className="text-foreground/70">{label}</span>
      {options ? (
        <Select
          aria-label={label}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="h-(--control-h) w-full rounded-sm border bg-background px-2"
        >
          <option value="">All</option>
          {options.map((option) => (
            <option key={option} value={option}>
              {option.replaceAll("_", " ")}
            </option>
          ))}
        </Select>
      ) : (
        <Input
          type={type}
          aria-label={label}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="h-(--control-h) w-full rounded-sm border bg-background px-2"
        />
      )}
    </label>
  );
}

export function resourceHref(type: string, id: string): string {
  switch (type) {
    case "installation":
      return `/dashboard/clusters/${encodeURIComponent(id)}`;
    case "agent_connection_record":
      return `/dashboard/agents?connection=${encodeURIComponent(id)}`;
    case "alert":
      return `/dashboard/alerting?alert=${encodeURIComponent(id)}`;
    case "backup":
      return `/dashboard/settings/backup`;
    case "self_management_application":
      return "/dashboard/delivery";
    default:
      return `/dashboard/search?q=${encodeURIComponent(id)}`;
  }
}

export function QueryFailure<T>({
  label,
  query,
}: {
  label: string;
  query: QueryState<T>;
}) {
  return (
    <QueryStates
      query={query}
      permission="charlie:read"
      errorTitle={`${label} unavailable`}
      errorDescription="This Charlie gateway capability is unavailable. No action was taken."
    >
      {() => null}
    </QueryStates>
  );
}
