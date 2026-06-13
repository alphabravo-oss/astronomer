import api from '@/lib/api';
import type { APIResponse } from '@/types';

export interface ExtensionCSP {
  scriptSrc?: string[];
  connectSrc?: string[];
  frameSrc?: string[];
  imageSrc?: string[];
}

export interface ExtensionManifest {
  apiVersion: string;
  name: string;
  displayName?: string;
  version: string;
  compatibleAstronomer: string;
  entry: string;
  permissions: string[];
  backendApiScopes?: string[];
  csp?: ExtensionCSP;
  extensionPoints: {
    sidebar?: Array<{ label: string; path: string }>;
    widgets?: Array<{ id: string; title: string }>;
    clusterTabs?: Array<{ label: string; component: string }>;
    settings?: Array<{ label: string; component: string }>;
  };
}

export interface ExtensionFinding {
  field?: string;
  severity: 'error' | 'warning' | string;
  message: string;
}

export interface ExtensionValidationResponse {
  valid: boolean;
  compatibilityStatus: 'compatible' | 'incompatible' | 'unknown' | string;
  checksum: string;
  manifest: ExtensionManifest;
  warnings: ExtensionFinding[];
  errors: ExtensionFinding[];
}

export interface ExtensionRecord {
  id: string;
  name: string;
  displayName: string;
  version: string;
  source: string;
  checksum: string;
  enabled: boolean;
  compatibilityStatus: 'compatible' | 'incompatible' | 'unknown' | string;
  manifest: ExtensionManifest;
  installedAt: string;
  updatedAt: string;
}

export interface ExtensionListResponse {
  items: ExtensionRecord[];
  sampleManifest: ExtensionManifest;
}

export async function listExtensions(): Promise<ExtensionListResponse> {
  const res = await api.get<APIResponse<ExtensionListResponse>>('/extensions/');
  return res.data.data;
}

export async function getSampleExtensionManifest(): Promise<ExtensionManifest> {
  const res = await api.get<APIResponse<ExtensionManifest>>('/extensions/sample-manifest/');
  return res.data.data;
}

export async function validateExtensionManifest(
  manifest: ExtensionManifest,
): Promise<ExtensionValidationResponse> {
  const res = await api.post<APIResponse<ExtensionValidationResponse>>('/extensions/validate/', {
    manifest,
  });
  return res.data.data;
}

export async function installExtension(
  manifest: ExtensionManifest,
  opts?: { source?: string; enable?: boolean },
): Promise<ExtensionRecord> {
  const res = await api.post<APIResponse<ExtensionRecord>>('/extensions/', {
    manifest,
    source: opts?.source,
    enable: opts?.enable ?? false,
  });
  return res.data.data;
}

export async function enableExtension(name: string): Promise<ExtensionRecord> {
  const res = await api.post<APIResponse<ExtensionRecord>>(
    `/extensions/${encodeURIComponent(name)}/enable/`,
  );
  return res.data.data;
}

export async function disableExtension(name: string): Promise<ExtensionRecord> {
  const res = await api.post<APIResponse<ExtensionRecord>>(
    `/extensions/${encodeURIComponent(name)}/disable/`,
  );
  return res.data.data;
}
