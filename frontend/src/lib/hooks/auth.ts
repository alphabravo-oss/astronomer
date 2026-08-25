import { useQuery } from "@tanstack/react-query";
import { getCurrentUser } from "@/lib/api/auth";
import { queryKeys } from "@/lib/query-keys";

export function useCurrentUser() {
  return useQuery({
    queryKey: queryKeys.users.current,
    queryFn: getCurrentUser,
    retry: false,
    staleTime: 5 * 60 * 1000,
  });
}
