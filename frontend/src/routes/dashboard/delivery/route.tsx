import { createFileRoute, Outlet } from "@tanstack/react-router";
import { PageShell } from "@/components/ui/page";
import { useLocation } from "@tanstack/react-router";
import { RemoteProjectPicker } from "@/components/projects/remote-project-picker";
import { useDeliveryProjectScope } from "@/components/delivery/shared";

function DeliveryEstateLayout() {
  const { projectId, setProjectId } = useDeliveryProjectScope();
  const pathname = useLocation({ select: (location) => location.pathname });
  const estate = pathname.replace(/\/$/, "") === "/dashboard/delivery";
  return (
    <PageShell>
      <p className="text-xs text-muted-foreground">
        Continuous Delivery ·{" "}
        {estate ? "All authorized clusters" : "Project scope"}
      </p>
      {!estate && (
        <RemoteProjectPicker
          value={projectId}
          onChange={setProjectId}
          ariaLabel="Delivery project"
        />
      )}
      <Outlet />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/delivery")({
  component: DeliveryEstateLayout,
});
