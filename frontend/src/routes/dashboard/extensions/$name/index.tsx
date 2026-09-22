import { createFileRoute } from "@tanstack/react-router";
import { ExtensionPage } from "./-page";

export const Route = createFileRoute("/dashboard/extensions/$name/")({
  component: ExtensionPage,
});
