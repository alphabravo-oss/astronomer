import type { LoggingOutput } from "@/types";

export function outputsForCluster(
  outputs: LoggingOutput[] | undefined,
  clusterId: string,
): LoggingOutput[] {
  return clusterId
    ? (outputs ?? []).filter((output) => output.clusterId === clusterId)
    : [];
}

export function validOutputSelection(
  ids: string[],
  outputs: LoggingOutput[],
): boolean {
  return (
    ids.length > 0 &&
    ids.every((id) => outputs.some((output) => output.id === id))
  );
}
