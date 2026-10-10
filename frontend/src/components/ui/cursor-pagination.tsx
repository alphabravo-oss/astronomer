import { useState } from "react";
import { ActionButton } from "./action-button";
import type { PaginationMetadata } from "@/types";

const PREVIOUS_PAGE_WINDOW = 128;

export function useCursorPagination(scope: string, limit = 25) {
  const [state, setState] = useState({
    scope,
    limit,
    tokens: [""],
    firstPage: 1,
  });
  const matches = state.scope === scope && state.limit === limit;
  const current = matches
    ? state
    : { scope, limit, tokens: [""], firstPage: 1 };
  const { tokens, firstPage } = current;
  // Persist a scope reset before children commit a request. Deriving initial
  // params alone would revive an old continuation when the scope changes back.
  if (!matches) setState(current);
  const accepts = (token: string) =>
    token.length > 0 && token.length <= 4096 && !tokens.includes(token);
  return {
    params: { limit, cursor: tokens[tokens.length - 1] },
    page: firstPage + tokens.length - 1,
    canPrevious: tokens.length > 1,
    accepts,
    previous: () => {
      if (tokens.length > 1)
        setState({ scope, limit, tokens: tokens.slice(0, -1), firstPage });
    },
    next: (token: string) => {
      if (accepts(token)) {
        const nextTokens = [...tokens, token];
        const evicted = nextTokens.length > PREVIOUS_PAGE_WINDOW + 1;
        setState({
          scope,
          limit,
          tokens: evicted ? nextTokens.slice(1) : nextTokens,
          firstPage: firstPage + (evicted ? 1 : 0),
        });
      }
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
        disabled={query.isFetching || !control.canPrevious}
        onClick={control.previous}
      >
        Previous
      </ActionButton>
      <span>Page {control.page}</span>
      {!control.canPrevious && control.page > 1 && (
        <span>
          Earlier pages are outside the {PREVIOUS_PAGE_WINDOW}-page history.
        </span>
      )}
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
