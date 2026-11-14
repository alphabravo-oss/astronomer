import { useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { Check, LoaderCircle, Search, UserRound } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { usePrincipalSearch } from "@/lib/hooks/rbac";
import type { PrincipalSearchItem } from "@/lib/api/rbac";
import { cn } from "@/lib/utils";
import { BareButton } from "@/components/form/bare-button";

interface PrincipalPickerProps {
  value: string;
  onChange: (userId: string) => void;
  id?: string;
}

export function PrincipalPicker({ value, onChange, id }: PrincipalPickerProps) {
  const [input, setInput] = useState("");
  const [selected, setSelected] = useState<PrincipalSearchItem | null>(null);
  const [debouncedQuery] = useDebouncedValue(input.trim(), { wait: 250 });
  const search = usePrincipalSearch(debouncedQuery);
  const queryReady = input.trim().length >= 3;

  const choose = (principal: PrincipalSearchItem) => {
    if (!principal.user_id) return;
    setSelected(principal);
    onChange(principal.user_id);
  };

  return (
    <div className="space-y-2">
      <div className="relative">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          id={id}
          type="search"
          value={input}
          onChange={(event) => setInput(event.target.value)}
          placeholder="Search people by name, username, or email…"
          aria-label="Search local accounts"
          className="pl-9"
        />
      </div>

      {selected && (
        <div className="flex items-center gap-3 rounded-lg border border-primary/40 bg-primary/5 px-3 py-2">
          <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
            <Check className="size-4" aria-hidden="true" />
          </span>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium">
              {selected.display_name}
            </p>
            <p className="truncate text-xs text-muted-foreground">
              {selected.email}
            </p>
          </div>
          <Badge variant="secondary">Local</Badge>
        </div>
      )}

      <div
        className="max-h-56 overflow-y-auto rounded-lg border border-border bg-card"
        aria-live="polite"
      >
        {!queryReady ? (
          <p className="px-3 py-4 text-sm text-muted-foreground">
            Enter at least 3 characters to search local accounts.
          </p>
        ) : search.isFetching ? (
          <p className="flex items-center gap-2 px-3 py-4 text-sm text-muted-foreground">
            <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
            Searching local accounts…
          </p>
        ) : search.isError ? (
          <p className="px-3 py-4 text-sm text-status-error">
            Identity search failed. Check your access and try again.
          </p>
        ) : (search.data?.principals.length ?? 0) === 0 ? (
          <p className="px-3 py-4 text-sm text-muted-foreground">
            No identities match “{debouncedQuery}”.
          </p>
        ) : (
          <ul className="divide-y divide-border">
            {search.data?.principals.map((principal) => {
              const key =
                principal.user_id ||
                `${principal.connector_id}:${principal.subject}`;
              const isSelected = Boolean(
                principal.user_id && principal.user_id === value,
              );
              return (
                <li key={key}>
                  <BareButton
                    onClick={() => void choose(principal)}
                    className={cn(
                      "flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors hover:bg-muted/60 focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:opacity-60",
                      isSelected && "bg-primary/5",
                    )}
                  >
                    <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
                      <UserRound className="size-4" aria-hidden="true" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium text-foreground">
                        {principal.display_name}
                      </span>
                      <span className="block truncate text-xs text-muted-foreground">
                        {principal.email}
                        {principal.connector_name
                          ? ` · ${principal.connector_name}`
                          : ""}
                      </span>
                    </span>
                    <Badge variant="secondary">Local</Badge>
                  </BareButton>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
