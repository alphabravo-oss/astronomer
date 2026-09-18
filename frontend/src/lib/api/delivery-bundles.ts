/** Component bundle contracts and operations. */

import { camelizeKeys } from "@/lib/camelize";
import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import {
  mapPage,
  optionalIdempotencyHeader,
  type DeliveryContracts,
  type PageParams,
} from "@/lib/api/delivery-common";

export type RendererKind = "helm" | "kustomize";
export type BundleScope = "namespace" | "platform";
export type DriftPolicy = "ignore" | "detect" | "repair";

export interface ComponentBundle {
  id: string;
  projectId: string;
  name: string;
  description?: string;
  createdAt: string;
  updatedAt: string;
}

export interface CapabilityRequirement {
  name: string;
  constraint?: string;
}

export interface ReconciliationPolicy {
  interval: string;
  retryInterval: string;
  timeout: string;
  prune: boolean;
  wait: boolean;
  drift: DriftPolicy;
}

export interface KustomizeRenderer {
  path: string;
  targetNamespace: string;
  patches?: string[];
}

export interface HelmRenderer {
  chart: string;
  chartVersion: string;
  releaseName: string;
  targetNamespace: string;
  values?: Record<string, unknown>;
  installRetries: number;
  upgradeRetries: number;
  test: boolean;
}

export interface RendererSpec {
  kind: RendererKind;
  kustomize?: KustomizeRenderer;
  helm?: HelmRenderer;
}

export interface ComponentBundleVersion {
  id: string;
  bundleId: string;
  sourceId: string;
  version: string;
  renderer: RendererKind;
  scope: BundleScope;
  requestedRevision: string;
  resolvedRevision?: string;
  artifactDigest?: string;
  rendererSpec: RendererSpec;
  reconciliationPolicy: ReconciliationPolicy;
  requiredCapabilities: CapabilityRequirement[];
  dependencyBundleIds: string[];
  specDigest: string;
  verificationStatus: string;
  verificationIdentity?: string;
  state: string;
  lastErrorCode?: string;
  createdAt: string;
}

export interface RendererSpecRequest {
  kind: RendererKind;
  kustomize?: {
    path: string;
    target_namespace: string;
    patches?: string[];
  };
  helm?: {
    chart: string;
    chart_version: string;
    release_name: string;
    target_namespace: string;
    values?: Record<string, unknown>;
    install_retries: number;
    upgrade_retries: number;
    test: boolean;
  };
}

export interface CreateBundleVersionRequest {
  project_id: string;
  version: string;
  spec: {
    source_id: string;
    requested_revision: string;
    renderer: RendererSpecRequest;
    scope: BundleScope;
    reconciliation_policy: {
      interval: string;
      retry_interval: string;
      timeout: string;
      prune: boolean;
      wait: boolean;
      drift: DriftPolicy;
    };
    required_capabilities?: Array<{ name: string; constraint?: string }>;
  };
  dependency_bundle_ids?: string[];
}

function mapBundle(wire: DeliveryContracts["DeliveryBundle"]): ComponentBundle {
  return camelizeKeys(wire) as unknown as ComponentBundle;
}
function mapBundleVersion(
  wire: DeliveryContracts["DeliveryBundleVersion"],
): ComponentBundleVersion {
  return camelizeKeys(wire) as unknown as ComponentBundleVersion;
}

export async function listComponentBundles(
  projectId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundles({
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapBundle);
}

export async function createComponentBundle(
  projectId: string,
  body: { name: string; description?: string },
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryBundles({
    body: { project_id: projectId, ...body },
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapBundle(requireEnvelopeData(wire, "delivery response"));
}

export async function getComponentBundle(
  projectId: string,
  id: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundlesById({
    path: { id },
    query: { project_id: projectId },
    signal,
  });
  return mapBundle(requireEnvelopeData(wire, "delivery response"));
}

export async function listComponentBundleVersions(
  projectId: string,
  bundleId: string,
  params: PageParams = {},
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundlesByIdVersions({
    path: { id: bundleId },
    query: { project_id: projectId, ...params },
    signal,
  });
  return mapPage(wire, mapBundleVersion);
}

export async function getComponentBundleVersion(
  projectId: string,
  bundleId: string,
  versionId: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getDeliveryBundlesByIdVersionsByVersionId({
    path: { id: bundleId, versionId },
    query: { project_id: projectId },
    signal,
  });
  return mapBundleVersion(requireEnvelopeData(wire, "delivery response"));
}

export async function createComponentBundleVersion(
  bundleId: string,
  body: CreateBundleVersionRequest,
  key?: string,
  signal?: AbortSignal,
) {
  const wire = await generated.postDeliveryBundlesByIdVersions({
    path: { id: bundleId },
    query: { project_id: body.project_id },
    body,
    headerParams: optionalIdempotencyHeader(key),
    signal,
  });
  return mapBundleVersion(requireEnvelopeData(wire, "delivery response"));
}
