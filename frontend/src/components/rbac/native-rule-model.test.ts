import { nativeRuleError } from "./native-rule-model";
import type { CreateNativeRuleRequest } from "@/lib/api/native-rbac";
const valid: CreateNativeRuleRequest = {
  userId: "00000000-0000-4000-8000-000000000001",
  resource: "certificates",
  apiGroup: "cert-manager.io",
  verbs: ["read"],
};
describe("native grant validation", () => {
  it("accepts explicit resource grants", () =>
    expect(nativeRuleError(valid)).toBeUndefined());
  it.each([
    { userId: "name-not-id" },
    { clusterId: "not-a-uuid" },
    { namespace: "Bad_NS" },
    { apiGroup: "rbac.authorization.k8s.io" },
    { verbs: [] },
    { resource: "" },
  ])("rejects invalid scope or authority: %j", (change) =>
    expect(nativeRuleError({ ...valid, ...change })).toBeTruthy(),
  );
});
