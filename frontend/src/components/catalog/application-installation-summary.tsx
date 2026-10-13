import type { HelmChart } from "@/types";
import { InstallationDestination } from "./installation-destination";
import {
  ApplicationOfferingSupport,
  CatalogOfferingSupport,
} from "./catalog-offering-support";

interface Destination {
  clusterId: string;
  projectId: string;
  namespace: string;
  releaseName: string;
}
export function ApplicationInstallationSummary({
  destination,
  version,
  chart,
  chartId,
}: {
  destination: Destination;
  version?: string;
  chart?: HelmChart;
  chartId?: string;
}) {
  return (
    <>
      <InstallationDestination
        clusterId={destination.clusterId}
        projectId={destination.projectId}
        manager="Applications"
        releases={[
          {
            namespace: destination.namespace,
            releaseName: destination.releaseName,
            version,
          },
        ]}
      />
      {chart ? (
        <CatalogOfferingSupport
          chart={chart.name}
          repositoryId={chart.repositoryId}
          version={version}
          detail
        />
      ) : (
        <ApplicationOfferingSupport
          projectId={destination.projectId}
          chartId={chartId ?? ""}
          version={version}
        />
      )}
    </>
  );
}
