import { useEffect } from "react";
import { useUserPreferences } from "@/lib/user-preferences";
import { toastApiError } from "@/lib/toast";
import type { StarredNavControls } from "./sidebar-nav-items";

export function useStarredNavControls(): StarredNavControls {
  const {
    preferences,
    isServerOwned,
    isLoading,
    isSaving,
    saveError,
    updatePreferences,
  } = useUserPreferences();
  useEffect(() => {
    if (saveError)
      toastApiError("Failed to save navigation preferences", saveError);
  }, [saveError]);
  const types = preferences.starred_types ?? [];
  return {
    types,
    disabled: !isServerOwned || isLoading || isSaving,
    toggle: (type) => {
      if (!isServerOwned || isLoading || isSaving) return;
      if (!types.includes(type) && types.length >= 20) return;
      updatePreferences({
        starred_types: types.includes(type)
          ? types.filter((value) => value !== type)
          : [...types, type],
      });
    },
  };
}
