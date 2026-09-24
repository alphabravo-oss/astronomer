import { lazy, Suspense, type ComponentProps } from "react";
import type { GuidedResourceForm as Form } from "./guided-resource-form";

const GuidedForm = lazy(() =>
  import("./guided-resource-form").then((module) => ({
    default: module.GuidedResourceForm,
  })),
);

/** Keep indexed manifest editors out of the dashboard's eager YAML-view path. */
export function LazyGuidedResourceForm(props: ComponentProps<typeof Form>) {
  return (
    <Suspense
      fallback={
        <p role="status" className="p-5">
          Loading resource editor…
        </p>
      }
    >
      <GuidedForm {...props} />
    </Suspense>
  );
}
