import { useState } from "react";
import type { PaginationState } from "@tanstack/react-table";

export function useOperationsPage(scope: string) {
  const [state, setState] = useState({ scope, pageIndex: 0 });
  const matches = state.scope === scope;
  const current = matches ? state : { scope, pageIndex: 0 };
  // Persist before the next request, including A→B→A with no pager click in B.
  if (!matches) setState(current);
  return {
    pagination: { pageIndex: current.pageIndex, pageSize: 100 },
    change: (page: PaginationState) => {
      if (
        !Number.isSafeInteger(page.pageIndex) ||
        page.pageIndex < 0 ||
        page.pageSize !== 100
      ) {
        throw new Error("Unsupported operations page");
      }
      // The server rejects unsupported deep offsets/pages explicitly; there
      // is no client clamp that could silently fetch an earlier page again.
      setState({ scope, pageIndex: page.pageIndex });
    },
  };
}
