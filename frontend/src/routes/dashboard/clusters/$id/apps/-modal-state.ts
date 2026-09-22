export type Section = "installed" | "browse" | "recommended" | "repositories";
export const SECTIONS: Section[] = [
  "installed",
  "browse",
  "recommended",
  "repositories",
];

// Modal control state hoisted into the page so any of the three sections
// (Installed row Upgrade/Uninstall, Browse / Recommended card Install) can
// open the right modal without prop-drilling onClose/onSuccess handlers
// everywhere.
export type ModalState =
  | { kind: "none" }
  | { kind: "install"; chartId: string; chartName: string }
  | {
      kind: "upgrade";
      installedChartId: string;
      chartId: string;
      chartName: string;
      currentVersionId: string;
      currentValues: string;
      releaseName: string;
      namespace: string;
    }
  | {
      kind: "uninstall";
      installedChartId: string;
      releaseName: string;
      chartName: string;
      namespace: string;
    };
