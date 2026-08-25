import { genericResourceActionPolicy } from "./resource-action-policy";

describe("genericResourceActionPolicy", () => {
  it("allows ordinary managed objects to be edited and deleted", () => {
    expect(genericResourceActionPolicy("configmaps")).toEqual({
      editable: true,
      deletable: true,
    });
    expect(genericResourceActionPolicy("jobs")).toEqual({
      editable: true,
      deletable: true,
    });
  });

  it("keeps high-blast-radius and controller-owned generic rows read-only", () => {
    expect(genericResourceActionPolicy("crds")).toEqual({
      editable: false,
      deletable: false,
    });
    expect(genericResourceActionPolicy("k8s-clusterroles")).toEqual({
      editable: false,
      deletable: false,
    });
  });
});
