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
import { BareButton } from "@/components/form/bare-button";
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
} from "@/ui";

export function TopbarAccountMenu() {
  const navigate = useNavigate();
  const { user, logout } = useAuthStore();
  const signOut = async () => {
    // POST /auth/logout first so the backend can revoke the session and (for
    // SSO users) hand us a Dex end_session URL. Local state is cleared
    // regardless.
    let redirectUrl: string | undefined;
    try {
      const res = await logoutCurrentSession();
      redirectUrl = res.redirectUrl;
    } catch {
      // Network failure / 401 — still clear local state.
    }
    logout();
    if (redirectUrl) {
      window.location.href = redirectUrl;
    } else {
      void navigate({ to: "/auth/login" });
    }
  };
  return (
    <Menu modal={false}>
      <MenuTrigger asChild>
        <BareButton
          aria-label="User menu"
          className="flex h-8 items-center gap-1.5 rounded-md pl-1 pr-1.5 transition-colors hover:bg-accent focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
        >
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-muted text-muted-foreground">
            <User className="h-3.5 w-3.5" />
          </span>
          <ChevronDown className="h-3 w-3 text-muted-foreground" />
        </BareButton>
      </MenuTrigger>
      <MenuContent data-header-popover className="w-60">
        <MenuLabel className="space-y-0.5">
          <span className="block truncate text-sm font-medium text-foreground">
            {user?.displayName || user?.username}
          </span>
          <span className="block truncate text-meta font-normal">
            {user?.email}
          </span>
        </MenuLabel>
        <MenuSeparator />
        <MenuItem
          onSelect={() => void navigate({ to: "/dashboard/account/preferences" })}
        >
          <SlidersHorizontal className="h-4 w-4" />
          Preferences
        </MenuItem>
        <MenuItem onSelect={() => void navigate({ to: "/dashboard/settings" })}>
          <Settings className="h-4 w-4" />
          Settings
        </MenuItem>
        <MenuItem
          onSelect={() => void navigate({ to: "/dashboard/account/security" })}
        >
          <Shield className="h-4 w-4" />
          Security
        </MenuItem>
        <MenuSeparator />
        <MenuItem onSelect={() => void signOut()}>
          <LogOut className="h-4 w-4" />
          Sign out
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}
