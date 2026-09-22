import { useQuery } from "@tanstack/react-query";
import { getLatestBackupDrill } from "@/lib/api/settings-backup-drill";
import { settingsKeys } from "./query-keys";

export function useLatestBackupDrill() {
  return useQuery({
    queryKey: settingsKeys.backupDrill,
    queryFn: ({ signal }) => getLatestBackupDrill({ signal }),
  });
}
