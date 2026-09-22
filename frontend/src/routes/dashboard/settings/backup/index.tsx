import { createFileRoute } from "@tanstack/react-router";
import { AstronomerBackupPage } from "./-page";

export const Route = createFileRoute("/dashboard/settings/backup/")({
  component: AstronomerBackupPage,
});
