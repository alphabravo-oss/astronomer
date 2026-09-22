import { createFileRoute } from "@tanstack/react-router";
import { RegisterClusterWizardRoute } from "./-page";
import { parseRegistrationSearch } from "@/components/clusters/registration-flow";

export const Route = createFileRoute("/dashboard/clusters/register/")({
  validateSearch: parseRegistrationSearch,
  component: RegisterClusterWizardRoute,
});
