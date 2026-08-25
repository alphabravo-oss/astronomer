export interface OwnershipTransferResult {
  id: string;
  managedBy: "api" | "ui" | "crd" | "system";
  transferred: boolean;
}
