export const catalogControllerWindowKey = (
  userID: string | undefined,
  isSuperuser: boolean | undefined,
  roles: unknown,
) => ["catalog", "controller-window", userID, isSuperuser, roles] as const;
