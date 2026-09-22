import { PageHeader } from "@/components/ui/page";

// The delivery/route.tsx layout already renders the "Continuous Delivery"
// eyebrow above the tab strip; this is just the Estate tab's own title +
// description, split out so EstateDeliveryOverview stays under its
// complexity-budget line ceiling.
export function EstateHeader() {
  return (
    <PageHeader
      title="Estate"
      description="All environments. Click a cluster to open its Flux workspace — Sources, Bundles, Targets, Rollouts, and Deployments live there."
    />
  );
}
