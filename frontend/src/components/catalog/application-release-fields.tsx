import { CatalogNamespaceField } from "./catalog-namespace-field";
import type { ReactNode } from "react";
import { Input } from "@/components/ui/input";

export function ApplicationReleaseFields({
  clusterId,
  version,
  releaseName,
  namespace,
  readOnly,
  onReleaseName,
  onNamespace,
}: {
  clusterId: string;
  version: ReactNode;
  releaseName: string;
  namespace: string;
  readOnly: boolean;
  onReleaseName: (value: string) => void;
  onNamespace: (value: string) => void;
}) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <div className="space-y-1.5">
        <label
          className="text-xs font-medium text-muted-foreground"
          htmlFor="application-version"
        >
          Version
        </label>
        {version}
      </div>
      <div className="space-y-1.5">
        <label
          className="text-xs font-medium text-muted-foreground"
          htmlFor="application-release"
        >
          Release name
        </label>
        <Input
          id="application-release"
          value={releaseName}
          onChange={(event) => onReleaseName(event.target.value)}
          disabled={readOnly}
        />
      </div>
      <div className="space-y-1.5">
        <label
          className="text-xs font-medium text-muted-foreground"
          htmlFor="application-namespace"
        >
          Namespace
        </label>
        <CatalogNamespaceField
          clusterId={clusterId}
          id="application-namespace"
          value={namespace}
          onChange={onNamespace}
          disabled={readOnly}
        />
      </div>
    </div>
  );
}
