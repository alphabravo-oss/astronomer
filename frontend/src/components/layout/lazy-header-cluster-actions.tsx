import { lazy, Suspense, type ComponentProps } from "react";
import type { HeaderClusterActions as Actions } from "./header-cluster-actions";

const HeaderClusterActions = lazy(() =>
  import("./header-cluster-actions").then((module) => ({
    default: module.HeaderClusterActions,
  })),
);

/** Cluster-only tools do not belong to the global dashboard's eager bundle. */
export function LazyHeaderClusterActions(
  props: ComponentProps<typeof Actions>,
) {
  return (
    <Suspense fallback={<span role="status">Loading cluster actions…</span>}>
      <HeaderClusterActions {...props} />
    </Suspense>
  );
}
