import { useEffect, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { WifiOff } from "lucide-react";
import { useCurrentUser } from "@/lib/hooks/auth";
import { useAuthStore } from "@/lib/store";
import { apiErrorStatus } from "@/lib/api/errors";
import { StatePanel } from "@/components/ui/empty-state";

export function DashboardSessionGate({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const updateUser = useAuthStore((s) => s.updateUser);
  const { data: user, isFetched, isError, error, refetch } = useCurrentUser();
  const status = apiErrorStatus(error);
  const rejected = status === 401 || status === 403;
  const mustChangePassword =
    user?.mustChangePassword || user?.must_change_password;

  useEffect(() => {
    if (user && !rejected) updateUser(user);
  }, [user, rejected, updateUser]);
  useEffect(() => {
    if (rejected) void navigate({ to: "/auth/login", replace: true });
    else if (mustChangePassword)
      void navigate({ to: "/auth/change-password", replace: true });
  }, [rejected, mustChangePassword, navigate]);

  // A network failure is not a rejected session. Keep authenticated content
  // during a transient refetch failure; never mount it before the first
  // successful identity check, after rejection, or during password rotation.
  if (!user && isError && !rejected) {
    return (
      <StatePanel
        icon={WifiOff}
        title="Unable to check your session"
        description="Check your connection and try again."
        actionLabel="Try again"
        onAction={() => void refetch()}
      />
    );
  }
  if (!isFetched || !user || rejected || mustChangePassword) {
    return (
      <div className="flex h-screen items-center justify-center" role="status">
        Checking session…
      </div>
    );
  }
  return children;
}
