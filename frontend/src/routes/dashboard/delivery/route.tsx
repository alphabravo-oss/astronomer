import { usePermissionDecision } from "@/lib/permission-hooks";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import { PageEyebrowProvider, PageShell } from "@/components/ui/page";
import { useLocation } from "@tanstack/react-router";
import { RemoteProjectPicker } from "@/components/projects/remote-project-picker";
import { useDeliveryProjectScope } from "@/components/delivery/shared";

function DeliveryEstateLayout() {
  const inventory = usePermissionDecision("delivery_inventory", "read", {
    type: "global",
  });
  const { projectId, setProjectId } = useDeliveryProjectScope();
  const pathname = useLocation({ select: (location) => location.pathname });
  const estate = pathname.replace(/\/$/, "") === "/dashboard/delivery";
  return (
    <PageShell>
      {(!estate || !inventory.allowed) && (
        <RemoteProjectPicker
          value={projectId}
          onChange={setProjectId}
          ariaLabel="Delivery project"
        />
      )}
      <PageEyebrowProvider
        value={`Continuous Delivery · ${
          estate && inventory.allowed
            ? "All authorized clusters"
            : "Project scope"
        }`}
      >
        <Outlet />
      </PageEyebrowProvider>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/delivery")({
  component: DeliveryEstateLayout,
});
