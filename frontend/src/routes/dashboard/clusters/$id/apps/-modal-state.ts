export type Section = "installed" | "browse" | "recommended" | "repositories";
export const SECTIONS: Section[] = [
  "installed",
  "browse",
  "recommended",
  "repositories",
];

// Only confirmation state remains here; installation and upgrade use routes.
export type ModalState =
  | { kind: "none" }
  | {
      kind: "uninstall";
      installedChartId: string;
      releaseName: string;
      chartName: string;
      namespace: string;
    };
