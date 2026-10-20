import type { ReactNode } from "react";
import { CharlieCapability } from "./charlie-capability";

export { useCharlie, useCharlieOptional } from "./charlie-context";
export { productModeCopy, productModePresentation } from "./charlie-mode";

/**
 * Wraps the dashboard in the assistant context so the header icon (and ⌘J)
 * can open the drawer. Inactive installations render children alone: no
 * context, no icon, no drawer chunk.
 */
export function CharlieShell({
  children,
  enabled = true,
}: {
  children: ReactNode;
  enabled?: boolean;
}) {
  if (!enabled) return <>{children}</>;
  return <CharlieCapability>{children}</CharlieCapability>;
}
