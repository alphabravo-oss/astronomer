import { createFileRoute } from "@tanstack/react-router";
import { WidgetsAdminPage } from "./-page";

export const Route = createFileRoute("/dashboard/settings/widgets/")({
  component: WidgetsAdminPage,
});
