import { useNavigate } from "@tanstack/react-router";
import {
  ChevronDown,
  User,
  SlidersHorizontal,
  Settings,
  Shield,
  LogOut,
} from "lucide-react";
import { useAuthStore } from "@/lib/store";
import { logoutCurrentSession } from "@/lib/api/account-security";
import { useHeaderPopover } from "./use-header-popover";
export function TopbarAccountMenu() {
  const navigate = useNavigate();
  const { user, logout } = useAuthStore();
  const {
    open: userMenuOpen,
    setOpen: setUserMenuOpen,
    root: userRef,
  } = useHeaderPopover();
  return (
    <div ref={userRef} className="relative">
      <button
        onClick={() => setUserMenuOpen(!userMenuOpen)}
        aria-label="User menu"
        aria-expanded={userMenuOpen}
        className="flex items-center gap-2 h-8 pl-1 pr-2 rounded-md hover:bg-accent transition-colors"
      >
        <div className="w-6 h-6 rounded-full bg-linear-to-br from-zinc-600 to-zinc-800 flex items-center justify-center">
          <User className="h-3 w-3 text-primary-foreground" />
        </div>
        <ChevronDown className="h-3 w-3 text-muted-foreground" />
      </button>

      {userMenuOpen && (
        <div
          data-header-popover
          className="absolute right-0 top-full mt-1 w-56 rounded-lg border border-border bg-popover shadow-xl z-50 overflow-hidden"
        >
          <div className="px-3 py-2.5 border-b border-border">
            <p className="text-sm font-medium text-foreground">
              {user?.displayName || user?.username}
            </p>
            <p className="text-xs text-muted-foreground">{user?.email}</p>
          </div>
          <div className="p-1">
            <button
              onClick={() => {
                void navigate({ to: "/dashboard/account/preferences" });
                setUserMenuOpen(false);
              }}
              className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              <SlidersHorizontal className="h-4 w-4" />
              Preferences
            </button>
            <button
              onClick={() => {
                void navigate({ to: "/dashboard/settings" });
                setUserMenuOpen(false);
              }}
              className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              <Settings className="h-4 w-4" />
              Settings
            </button>
            <button
              onClick={() => {
                void navigate({ to: "/dashboard/account/security" });
                setUserMenuOpen(false);
              }}
              className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              <Shield className="h-4 w-4" />
              Security
            </button>
            <button
              onClick={async () => {
                // POST /auth/logout first so the backend can revoke the
                // session and (for SSO users) hand us a Dex end_session
                // URL to bounce through. We clear local state regardless
                // — even if the call fails the user has clicked "sign
                // out" and shouldn't be left looking authenticated.
                let redirectUrl: string | undefined;
                try {
                  const res = await logoutCurrentSession();
                  redirectUrl = res.redirectUrl;
                } catch {
                  // Network failure / 401 — still clear local state.
                }
                logout();
                if (redirectUrl) {
                  // Top-level navigation to Dex's end_session endpoint.
                  // Dex eventually redirects back to /api/v1/auth/logout-done/
                  // which lands the SPA back on /auth/login.
                  window.location.href = redirectUrl;
                } else {
                  void navigate({ to: "/auth/login" });
                }
              }}
              className="w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              <LogOut className="h-4 w-4" />
              Sign out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
