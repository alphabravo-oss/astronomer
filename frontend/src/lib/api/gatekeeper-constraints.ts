/**
 * Gatekeeper / OPA constraint authoring API client (P-04).
 *
 * Backend (per-cluster desired state reconciled through the agent tunnel;
 * create/delete are RBAC-gated + transactionally audited server-side):
 *   GET    /api/v1/clusters/{id}/gatekeeper/constraints/          -> list bundle + custom
 *   POST   /api/v1/clusters/{id}/gatekeeper/constraints/validate/ -> validate YAML only
 *   POST   /api/v1/clusters/{id}/gatekeeper/constraints/          -> validate + queue desired state
 *   DELETE /api/v1/clusters/{id}/gatekeeper/constraints/{name}/   -> queue absent desired state
 *
 * Validate/create body: { yaml }. Create returns pending status + task ID;
 * list polling exposes the eventual reconciliation outcome.
 *
 * Re-exported from ../api.ts via `export * from './api/gatekeeper-constraints'`.
 */
import {
  deleteClustersByIdGatekeeperConstraintsByName,
  getClustersByIdGatekeeperConstraints,
  postClustersByIdGatekeeperConstraints,
  postClustersByIdGatekeeperConstraintsValidate,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type { GatekeeperConstraint, ConstraintValidateResult } from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type ConstraintWire =
  OpenAPIComponents["schemas"]["GatekeeperConstraintSummary"];
type ValidationWire =
  OpenAPIComponents["schemas"]["ConstraintValidationResponse"];

function mapConstraint(
  wire: ConstraintWire,
  source: GatekeeperConstraint["source"],
): GatekeeperConstraint {
  return {
    name: wire.name,
    kind: wire.kind,
    apiVersion: wire.api_version,
    source,
    enforcementAction: wire.enforcement_action ?? "",
    violationCount: wire.violation_count ?? 0,
    yaml: wire.yaml,
    createdBy: wire.created_by,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
    desiredState: wire.desired_state ?? "present",
    syncStatus: wire.sync_status ?? "synced",
    generation: wire.generation ?? 0,
    observedGeneration: wire.observed_generation ?? 0,
    lastError: wire.last_error ?? "",
    lastReconciledAt: wire.last_reconciled_at,
  };
}

function mapValidation(wire: ValidationWire): ConstraintValidateResult {
  return {
    valid: wire.valid,
    errors: wire.errors,
    applied: wire.applied,
    name: wire.name ?? "",
    kind: wire.kind ?? "",
    status: wire.status,
    taskId: wire.task_id,
  };
}

export async function listGatekeeperConstraints(
  clusterId: string,
): Promise<GatekeeperConstraint[]> {
  const response = await getClustersByIdGatekeeperConstraints({
    path: { id: clusterId },
  });
  return [
    ...response.data.bundle.map((item) => mapConstraint(item, "bundle")),
    ...response.data.custom.map((item) => mapConstraint(item, "custom")),
  ];
}

export async function validateGatekeeperConstraint(
  clusterId: string,
  yaml: string,
): Promise<ConstraintValidateResult> {
  const response = await postClustersByIdGatekeeperConstraintsValidate({
    path: { id: clusterId },
    body: { yaml },
  });
  return mapValidation(response.data);
}

export async function applyGatekeeperConstraint(
  clusterId: string,
  yaml: string,
): Promise<ConstraintValidateResult> {
  const response = await postClustersByIdGatekeeperConstraints({
    path: { id: clusterId },
    headerParams: idempotencyHeaderParams(),
    body: { yaml },
  });
  return mapValidation(response.data);
}

export async function deleteGatekeeperConstraint(
  clusterId: string,
  name: string,
): Promise<
  OpenAPIComponents["schemas"]["GatekeeperConstraintMutationResponse"]
> {
  const response = await deleteClustersByIdGatekeeperConstraintsByName({
    path: { id: clusterId, name },
    headerParams: idempotencyHeaderParams(),
  });
  return response.data;
}
