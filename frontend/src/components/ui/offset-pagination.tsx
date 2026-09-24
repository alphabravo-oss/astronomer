import { useState } from "react";
import { ActionButton } from "./action-button";
import type { PaginationMetadata } from "@/types";

export function useOffsetPagination(scope: string, limit = 25) {
  const [state, setState] = useState({ scope, offsets: [0] });
  const offsets = state.scope === scope ? state.offsets : [0];
  return {
    params: { limit, offset: offsets[offsets.length - 1] },
    page: offsets.length,
    previous: () => setState({ scope, offsets: offsets.slice(0, -1) }),
    next: (offset: number) =>
      setState({ scope, offsets: [...offsets, offset] }),
  };
}

export function OffsetPagination({
  control,
  query,
  label,
}: {
  control: ReturnType<typeof useOffsetPagination>;
  query: {
    data?: { pagination: PaginationMetadata };
    isError: boolean;
    isFetching: boolean;
  };
  label: string;
}) {
  const page = query.isError ? undefined : query.data?.pagination;
  const next = page?.next_offset;
  const canNext =
    page?.has_more && next != null && next > control.params.offset;
  return (
    <nav
      aria-label={`${label} pagination`}
      className="flex items-center justify-end gap-2 text-sm text-muted-foreground"
    >
      <ActionButton
        size="sm"
        aria-label={`Previous ${label} page`}
        disabled={control.page === 1}
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
          if (canNext && next != null) control.next(next);
        }}
      >
        Next
      </ActionButton>
    </nav>
  );
}
