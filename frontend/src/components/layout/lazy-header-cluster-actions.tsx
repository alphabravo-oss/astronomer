import { lazy, Suspense, type ComponentProps } from "react";

const HeaderClusterActions = lazy(() =>
  import("./header-cluster-actions").then((module) => ({
    default: module.HeaderClusterActions,
  })),
);

export function LazyHeaderClusterActions(
  props: ComponentProps<typeof HeaderClusterActions>,
) {
  return (
    <Suspense fallback={null}>
      <HeaderClusterActions {...props} />
    </Suspense>
  );
}
