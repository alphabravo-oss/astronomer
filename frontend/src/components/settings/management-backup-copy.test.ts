import { describe, expect, it } from "vitest";

import { managementBackupSubmittedMessage } from "./hooks";

describe("management backup run copy", () => {
  it("states job submission without claiming backup completion", () => {
    expect(managementBackupSubmittedMessage).toBe(
      "Backup run accepted; track execution in backup history",
    );
    expect(managementBackupSubmittedMessage.toLowerCase()).not.toContain(
      "completed",
    );
  });
});
