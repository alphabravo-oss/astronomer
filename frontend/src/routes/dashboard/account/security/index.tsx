import { createFileRoute } from "@tanstack/react-router";
import { AccountSecurityPage } from "./-page";

export const Route = createFileRoute("/dashboard/account/security/")({
  component: AccountSecurityPage,
});
