import { createFileRoute } from "@tanstack/react-router";
import { PipelinePage } from "../../../-pipeline-editor";
export const Route = createFileRoute(
  "/dashboard/logging/pipelines/$pipelineId/edit/",
)({ component: () => <PipelinePage edit /> });
