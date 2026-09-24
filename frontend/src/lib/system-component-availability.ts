import type { DeliverySystemComponent } from "@/lib/api/delivery-system";

// Object inventories and version discovery do not measure replica redundancy.
// Even workload replica counts do not prove independent failure domains.
export function replicaRedundancy(
  component: Pick<DeliverySystemComponent, "kind" | "highAvailability">,
): string {
  if (
    ![
      "Deployment",
      "StatefulSet",
      "DaemonSet",
      "LonghornVolumeInventory",
    ].includes(component.kind)
  )
    return "Not observed";
  return component.highAvailability
    ? "Multiple replicas"
    : "No replica redundancy reported";
}
