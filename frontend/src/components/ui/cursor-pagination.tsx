import { useState } from "react";
import { ActionButton } from "./action-button";
import type { PaginationMetadata } from "@/types";

export function useCursorPagination(scope: string, limit = 25) {
  const [state, setState] = useState({ scope, limit, tokens: [""] });
  const matches = state.scope === scope && state.limit === limit;
  const tokens = matches ? state.tokens : [""];
  // Persist a scope reset before children commit a request. Deriving initial
  // params alone would revive an old continuation when the scope changes back.
  if (!matches) setState({ scope, limit, tokens });
  const accepts = (token: string) =>
    token.length > 0 && token.length <= 4096 && !tokens.includes(token);
  return {
    params: { limit, cursor: tokens[tokens.length - 1] },
    page: tokens.length,
    accepts,
    previous: () => {
      if (tokens.length > 1)
        setState({ scope, limit, tokens: tokens.slice(0, -1) });
    },
    next: (token: string) => {
      if (accepts(token))
        setState({ scope, limit, tokens: [...tokens, token] });
    },
  };
}

export function CursorPagination({
  control,
  query,
  label,
}: {
  control: ReturnType<typeof useCursorPagination>;
  query: {
    data?: { pagination: PaginationMetadata };
    isError: boolean;
    isFetching: boolean;
  };
  label: string;
}) {
  const page = query.isError ? undefined : query.data?.pagination;
  const next = page?.next_cursor;
  const canNext =
    !query.isFetching &&
    page?.has_more &&
    typeof next === "string" &&
    control.accepts(next);
  return (
    <nav
      aria-label={`${label} pagination`}
      className="flex items-center justify-end gap-2 text-sm text-muted-foreground"
    >
      <ActionButton
        size="sm"
        aria-label={`Previous ${label} page`}
        disabled={query.isFetching || control.page === 1}
        onClick={control.previous}
      >
        Previous
      </ActionButton>
      <span>Page {control.page}</span>
      <ActionButton
        size="sm"
        aria-label={`Next ${label} page`}
        disabled={!canNext}
        onClick={() => {
          if (canNext && typeof next === "string") control.next(next);
        }}
      >
        Next
      </ActionButton>
    </nav>
  );
}
