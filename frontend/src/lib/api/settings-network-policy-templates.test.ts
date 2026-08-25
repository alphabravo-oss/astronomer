import transport from "@/lib/api/transport";
import {
  applyNetworkPolicy,
  createNetworkPolicyTemplate,
  deleteNetworkPolicyApplication,
  deleteNetworkPolicyTemplate,
  getNetworkPolicyTemplate,
  listNetworkPolicyApplications,
  listNetworkPolicyTemplates,
  reapplyNetworkPolicyApplication,
  updateNetworkPolicyTemplate,
} from "./settings-network-policy-templates";

vi.mock("@/lib/api/transport", () => ({
  default: { request: vi.fn() },
}));

const request = vi.mocked(transport.request);
const templateWire = {
  id: "template-1",
  slug: "deny-all",
  name: "Deny all",
  description: "Default deny",
  kind: "builtin",
  spec_template: "kind: NetworkPolicy",
  enabled: true,
  created_by: "admin",
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T00:00:00Z",
};
const applicationWire = {
  id: "application-1",
  template_id: "template-1",
  template_slug: "deny-all",
  cluster_id: "cluster-1",
  namespace: "production",
  policy_name: "deny-all",
  status: "applied",
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T00:00:00Z",
};

beforeEach(() => {
  request.mockReset();
});

it("uses generated template operations and preserves snake-case wire fields", async () => {
  request.mockResolvedValue({ data: { data: [templateWire] } } as never);
  await expect(listNetworkPolicyTemplates()).resolves.toEqual([templateWire]);
  expect(request).toHaveBeenLastCalledWith(
    expect.objectContaining({
      method: "GET",
      url: "/api/v1/admin/network-policy-templates",
    }),
  );

  request.mockResolvedValue({ data: { data: templateWire } } as never);
  await getNetworkPolicyTemplate("template/1");
  await createNetworkPolicyTemplate({
    name: "Deny all",
    spec_template: "kind: NetworkPolicy",
  });
  await updateNetworkPolicyTemplate("template-1", {
    name: "Deny all",
    spec_template: "kind: NetworkPolicy",
  });
  await deleteNetworkPolicyTemplate("template-1");

  expect(request.mock.calls.slice(-4).map(([config]) => config.url)).toEqual([
    "/api/v1/admin/network-policy-templates/template%2F1",
    "/api/v1/admin/network-policy-templates",
    "/api/v1/admin/network-policy-templates/template-1",
    "/api/v1/admin/network-policy-templates/template-1",
  ]);
  expect(request.mock.calls.at(-3)?.[0].data).toEqual({
    name: "Deny all",
    spec_template: "kind: NetworkPolicy",
  });
});

it("uses generated application operations and maps the raw response", async () => {
  request.mockResolvedValue({ data: { data: [applicationWire] } } as never);
  await expect(listNetworkPolicyApplications("cluster-1")).resolves.toEqual([
    applicationWire,
  ]);
  await expect(
    applyNetworkPolicy("cluster-1", {
      template_id: "template-1",
      namespace: "production",
    }),
  ).resolves.toEqual([applicationWire]);

  request.mockResolvedValue({ data: { data: applicationWire } } as never);
  await expect(
    reapplyNetworkPolicyApplication("cluster-1", "application-1"),
  ).resolves.toEqual(applicationWire);
  await deleteNetworkPolicyApplication("cluster-1", "application-1");

  expect(request.mock.calls.map(([config]) => config.method)).toEqual([
    "GET",
    "POST",
    "POST",
    "DELETE",
  ]);
  expect(request.mock.calls.at(1)?.[0].data).toEqual({
    template_id: "template-1",
    namespace: "production",
  });
});
