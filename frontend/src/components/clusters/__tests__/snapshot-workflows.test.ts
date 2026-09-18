import { parseCommaSeparated } from "@/components/clusters/snapshot-dialogs";
import {
  formatSnapshotBytes,
  isManagedControlPlane,
} from "@/components/clusters/control-plane-snapshot-utils";

describe("snapshot workflow helpers", () => {
  it("normalizes optional comma-separated resource and namespace lists", () => {
    expect(parseCommaSeparated(" deployments, configmaps ,, secrets ")).toEqual(
      ["deployments", "configmaps", "secrets"],
    );
    expect(parseCommaSeparated(" , ")).toBeUndefined();
  });

  it("only hides control-plane snapshots for managed distributions", () => {
    expect(isManagedControlPlane("eks")).toBe(true);
    expect(isManagedControlPlane("aks")).toBe(true);
    expect(isManagedControlPlane("gke")).toBe(true);
    expect(isManagedControlPlane("rke2")).toBe(false);
    expect(isManagedControlPlane()).toBe(false);
  });

  it("formats snapshot sizes across binary units", () => {
    expect(formatSnapshotBytes()).toBe("—");
    expect(formatSnapshotBytes(1023)).toBe("1023 B");
    expect(formatSnapshotBytes(1024)).toBe("1.0 KB");
    expect(formatSnapshotBytes(1024 * 1024 * 3.5)).toBe("3.5 MB");
  });
});
