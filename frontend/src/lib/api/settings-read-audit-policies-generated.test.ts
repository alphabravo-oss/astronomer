import { beforeEach, describe, expect, it, vi } from "vitest";

const generated = vi.hoisted(() => ({
  adminReadAuditPoliciesList: vi.fn(),
  adminReadAuditPolicyCreate: vi.fn(),
  adminReadAuditPolicyDelete: vi.fn(),
  adminReadAuditPolicyGet: vi.fn(),
  adminReadAuditPolicyUpdate: vi.fn(),
}));

vi.mock("@/lib/api/generated/client", () => generated);

import {
  createReadAuditPolicy,
  deleteReadAuditPolicy,
  listReadAuditPolicies,
  updateReadAuditPolicy,
} from "./settings-read-audit-policies";

const policyWire = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "credential-reads",
  description: "Audit credential reads",
  path_pattern: "/api/v1/admin/credentials/*",
  verbs: "GET",
  sample_rate: 1,
  enabled: true,
  created_by: "00000000-0000-4000-8000-000000000002",
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T00:00:00Z",
};

describe("generated read-audit policy API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps the exact list envelope and forwards cancellation", async () => {
    const signal = new AbortController().signal;
    generated.adminReadAuditPoliciesList.mockResolvedValue({
      data: { items: [policyWire], total: 1 },
    });

    await expect(listReadAuditPolicies({ signal })).resolves.toEqual([
      policyWire,
    ]);
    expect(generated.adminReadAuditPoliciesList).toHaveBeenCalledWith({
      signal,
    });
  });

  it("uses generated create and update request bodies", async () => {
    generated.adminReadAuditPolicyCreate.mockResolvedValue({
      data: policyWire,
    });
    generated.adminReadAuditPolicyUpdate.mockResolvedValue({
      data: { ...policyWire, enabled: false },
    });

    await createReadAuditPolicy({
      name: policyWire.name,
      path_pattern: policyWire.path_pattern,
    });
    await updateReadAuditPolicy(policyWire.id, { enabled: false });

    expect(generated.adminReadAuditPolicyCreate).toHaveBeenCalledWith({
      body: { name: policyWire.name, path_pattern: policyWire.path_pattern },
      signal: undefined,
    });
    expect(generated.adminReadAuditPolicyUpdate).toHaveBeenCalledWith({
      path: { id: policyWire.id },
      body: { enabled: false },
      signal: undefined,
    });
  });

  it("preserves the server's truthful 204 delete contract", async () => {
    generated.adminReadAuditPolicyDelete.mockResolvedValue(undefined);
    await expect(deleteReadAuditPolicy(policyWire.id)).resolves.toBeUndefined();
    expect(generated.adminReadAuditPolicyDelete).toHaveBeenCalledWith({
      path: { id: policyWire.id },
      signal: undefined,
    });
  });
});
