import { cn } from "@/lib/utils";

/** Guided/YAML switch shown above the resource editor while editing. */
export function YamlEditorModeTabs({
  mode,
  onChange,
  status,
}: {
  mode: "guided" | "yaml";
  onChange: (next: "guided" | "yaml") => void;
  status: string;
}) {
  return (
    <div
      className="flex shrink-0 items-center gap-1 border-b border-border px-3 py-1.5"
      role="tablist"
      aria-label="Resource edit mode"
    >
      {(["guided", "yaml"] as const).map((item) => (
        <button
          key={item}
          type="button"
          role="tab"
          aria-selected={mode === item}
          onClick={() => onChange(item)}
          className={cn(
            "rounded-sm px-2.5 py-1 text-xs font-medium capitalize",
            mode === item
              ? "bg-muted text-foreground"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {item}
        </button>
      ))}
      <span className="ml-auto text-xs text-muted-foreground">{status}</span>
    </div>
  );
}
