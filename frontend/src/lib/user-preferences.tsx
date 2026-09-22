import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useEffect,
  type ReactNode,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@/lib/store";
import { queryKeys } from "@/lib/query-keys";
import {
  defaultUserPreferences,
  getUserPreferences,
  putUserPreferences,
  type UserPreferences,
} from "@/lib/api/user-preferences";
import { setDateFormatPreference, setTimeFormatPreference } from "@/lib/utils";

interface UserPreferencesContextValue {
  preferences: UserPreferences;
  isLoading: boolean;
  isSaving: boolean;
  saveError: Error | null;
  isServerOwned: boolean;
  updatePreferences: (patch: Partial<UserPreferences>) => void;
}

const UserPreferencesContext = createContext<UserPreferencesContextValue>({
  preferences: defaultUserPreferences,
  isLoading: false,
  isSaving: false,
  saveError: null,
  isServerOwned: false,
  updatePreferences: () => {},
});

export function UserPreferencesProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const userID = useAuthStore((state) => state.user?.id);
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const queryKey = queryKeys.users.preferences(userID ?? "anonymous");
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getUserPreferences(signal),
    enabled: isAuthenticated && !!userID,
    staleTime: 5 * 60_000,
    retry: false,
    throwOnError: false,
  });
  const preferences = query.data ?? defaultUserPreferences;

  useEffect(() => {
    setTimeFormatPreference(preferences.time_format);
  }, [preferences.time_format]);

  useEffect(() => {
    setDateFormatPreference(preferences.date_format ?? "locale");
  }, [preferences.date_format]);

  const mutation = useMutation({
    // Full-document PUTs are serialized so rapid control changes cannot reach
    // the server out of order and resurrect an older preference document.
    scope: { id: `user-preferences:${userID ?? "anonymous"}` },
    mutationFn: putUserPreferences,
    onMutate: async (next) => {
      await queryClient.cancelQueries({ queryKey });
      const previous = queryClient.getQueryData<UserPreferences>(queryKey);
      queryClient.setQueryData(queryKey, next);
      return { previous };
    },
    onError: (_error, _next, context) => {
      queryClient.setQueryData(
        queryKey,
        context?.previous ?? defaultUserPreferences,
      );
    },
    onSuccess: (stored) => queryClient.setQueryData(queryKey, stored),
  });
  const { mutate, isPending, error: saveError } = mutation;

  const updatePreferences = useCallback(
    (patch: Partial<UserPreferences>) => {
      if (!isAuthenticated || !userID) return;
      const current =
        queryClient.getQueryData<UserPreferences>(queryKey) ?? preferences;
      mutate({ ...current, ...patch });
    },
    [isAuthenticated, mutate, preferences, queryClient, queryKey, userID],
  );

  const value = useMemo<UserPreferencesContextValue>(
    () => ({
      preferences,
      isLoading: isAuthenticated && !!userID && query.isLoading,
      isSaving: isPending,
      saveError,
      isServerOwned: isAuthenticated && !!userID && query.isSuccess,
      updatePreferences,
    }),
    [isAuthenticated, isPending, preferences, query.isLoading, query.isSuccess, saveError, updatePreferences, userID],
  );
  return (
    <UserPreferencesContext.Provider value={value}>
      {children}
    </UserPreferencesContext.Provider>
  );
}

export function useUserPreferences(): UserPreferencesContextValue {
  return useContext(UserPreferencesContext);
}
