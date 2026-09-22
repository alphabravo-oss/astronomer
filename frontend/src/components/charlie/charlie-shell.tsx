import { lazy, Suspense, type ReactNode } from "react";

export { useCharlie } from "./charlie-context";
export { productModeCopy, productModePresentation } from "./charlie-mode";

const CharlieCapability = lazy(() =>
  import("./charlie-capability").then((module) => ({
    default: module.CharlieCapability,
  })),
);

/** Inactive installations do not load the optional assistant capability. */
export function CharlieShell({
  children,
  enabled = true,
}: {
  children: ReactNode;
  enabled?: boolean;
}) {
  return (
    <>
      {children}
      {enabled && (
        <Suspense fallback={null}>
          <CharlieCapability />
        </Suspense>
      )}
    </>
  );
}
