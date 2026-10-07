import type { ReactElement, ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";

/** Full column name: `ariaLabel` when the visible `header` is abbreviated. */
export function columnFullName(col: {
  header: string;
  ariaLabel?: string;
}): string {
  return col.ariaLabel ?? col.header;
}

/**
 * Wraps a column header control in a Tooltip when the column supplies a
 * `headerTooltip` or `ariaLabel`; otherwise renders the child untouched.
 */
export function HeaderTooltip({
  col,
  children,
}: {
  col: { ariaLabel?: string; headerTooltip?: ReactNode };
  children: ReactElement;
}) {
  const content = col.headerTooltip ?? col.ariaLabel;
  return content ? <Tooltip content={content}>{children}</Tooltip> : children;
}
