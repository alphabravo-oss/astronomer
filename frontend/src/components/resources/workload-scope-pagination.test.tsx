import { collectionScope } from "@/lib/cluster-scope-collection";
import { describe, it, expect } from "vitest";

describe("server collection scope", () => {
  it("sends the selected namespace before a server page is requested", () => {
    expect(collectionScope(["team-b"])).toEqual({
      enabled: true,
      namespace: "team-b",
    });
  });
  it("allows the server's authorized all-namespace collection", () => {
    expect(collectionScope(null)).toEqual({
      enabled: true,
      namespace: undefined,
    });
  });
  it.each([undefined, []])(
    "blocks incomplete or unsupported combined scope %s",
    (scope) => {
      expect(collectionScope(scope).enabled).toBe(false);
      expect(collectionScope(scope).message).toBeTruthy();
    },
  );
});

it("passes explicit multi-namespace selection to server pagination", () => {
  expect(collectionScope(["a", "b"])).toEqual({
    enabled: true,
    namespaces: ["a", "b"],
  });
});
