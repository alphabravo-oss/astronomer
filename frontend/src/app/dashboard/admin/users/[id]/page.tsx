'use client';

/**
 * Admin → User detail. Lists the four security-sensitive actions a superuser
 * can perform on another account (unlock, force-logout, disable TOTP, resync
 * groups) and the two state banners the backend surfaces when those actions
 * have been used recently (`locked_until`, `tokens_invalidated_at`).
 *
 * Action policy:
 *   - All actions are POST-only and require a confirmation dialog.
 *   - We only invalidate the user-detail query on success; the audit log
 *     stamp is written by the backend.
 *   - Buttons are gated on the current user's `is_superuser` flag. We still
 *     send the request if a non-superuser somehow gets here — the server is
 *     the source of truth — but the UI hides them by default.
 */

import { use, useMemo, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import {
  ShieldOff,
  Unlock,
  RefreshCcw,
  LogOut,
  AlertTriangle,
  Loader2,
  ArrowLeft,
  Clock,
  Users,
  ShieldCheck,
} from 'lucide-react';
import { formatDate, formatRelativeTime } from '@/lib/utils';
import {
  getAdminUser,
  adminUnlockUser,
  adminForceLogoutUser,
  adminDisableUserTotp,
  adminResyncUserGroups,
  type AdminUserDetail,
} from '@/lib/api/account-security';
import { useCurrentUser } from '@/lib/hooks';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';

const userKey = (id: string) => ['admin', 'users', id] as const;

type ActionKey = 'unlock' | 'force-logout' | 'disable-totp' | 'resync-groups';

interface ActionDef {
  key: ActionKey;
  label: string;
  icon: typeof Unlock;
  description: string;
  confirmText: string;
  variant?: 'destructive';
  /** Only render this button when the predicate matches the current state. */
  available: (u: AdminUserDetail) => boolean;
  run: (id: string) => Promise<void>;
}

const ACTIONS: ActionDef[] = [
  {
    key: 'unlock',
    label: 'Unlock account',
    icon: Unlock,
    description:
      'Clears the active account lockout. The user can sign in again immediately; the failed-login counter resets to zero.',
    confirmText: 'Unlock',
    available: (u) => !!u.lockedUntil,
    run: adminUnlockUser,
  },
  {
    key: 'force-logout',
    label: 'Force logout',
    icon: LogOut,
    description:
      'Invalidates every active JWT for this user. They will be signed out of all sessions immediately and must log in again.',
    confirmText: 'Sign user out',
    variant: 'destructive',
    available: () => true,
    run: adminForceLogoutUser,
  },
  {
    key: 'disable-totp',
    label: 'Disable 2FA',
    icon: ShieldOff,
    description:
      "Force-removes the user's TOTP secret and recovery codes. Use only when the user has lost their authenticator and cannot self-recover.",
    confirmText: 'Disable 2FA',
    variant: 'destructive',
    available: (u) => !!u.totpEnrolled,
    run: adminDisableUserTotp,
  },
  {
    key: 'resync-groups',
    label: 'Re-sync groups',
    icon: RefreshCcw,
    description:
      "Re-evaluates the group-sync rules against the user's last claims snapshot. Use after editing group-mapping rules.",
    confirmText: 'Re-sync',
    available: () => true,
    run: adminResyncUserGroups,
  },
];

export default function AdminUserDetailPage({ params }: { params: Promise<{ id: string }> }) {
  // Next.js 16 hands params as a Promise; unwrap with React.use().
  const { id } = use(params);
  const router = useRouter();
  const qc = useQueryClient();

  const { data: me } = useCurrentUser();
  const isSuperuser = useMemo(
    () =>
      !!(
        // The backend may surface admin status either as an explicit
        // is_superuser flag or as a role in `globalRoles`; handle both.
        (me as unknown as { is_superuser?: boolean; isSuperuser?: boolean })?.is_superuser ||
        (me as unknown as { is_superuser?: boolean; isSuperuser?: boolean })?.isSuperuser ||
        me?.globalRoles?.some((r) => r === 'admin' || r === 'superuser')
      ),
    [me],
  );

  const { data: user, isLoading } = useQuery({
    queryKey: userKey(id),
    queryFn: () => getAdminUser(id),
  });

  const [pending, setPending] = useState<ActionKey | null>(null);

  const mut = useMutation({
    mutationFn: async (action: ActionDef) => action.run(id),
    onSuccess: (_data, action) => {
      qc.invalidateQueries({ queryKey: userKey(id) });
      toastSuccess(`${action.label}: done`);
      setPending(null);
    },
    onError: (err: Error) => {
      toastApiError('', err, 'Action failed');
      setPending(null);
    },
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!user) {
    return (
      <div className="rounded-lg border border-border bg-card p-6">
        <p className="text-sm text-muted-foreground">User not found.</p>
        <button
          onClick={() => router.back()}
          className="mt-3 inline-flex items-center gap-1 text-sm text-foreground hover:underline"
        >
          <ArrowLeft className="h-4 w-4" /> Back
        </button>
      </div>
    );
  }

  const activeAction = ACTIONS.find((a) => a.key === pending) || null;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <Link
            href="/dashboard/rbac"
            className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            <ArrowLeft className="h-3 w-3" /> Back to RBAC
          </Link>
          <h1 className="mt-1 text-2xl font-semibold text-foreground tracking-tight">
            {user.displayName || user.username}
          </h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {user.email} · {user.provider}
          </p>
        </div>
      </div>

      {/* State banners */}
      {user.lockedUntil && (
        <div className="rounded-md border border-status-error/40 bg-status-error/10 p-4 flex items-start gap-3">
          <AlertTriangle className="h-5 w-5 text-status-error flex-shrink-0 mt-0.5" />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">
              Account locked until {formatDate(user.lockedUntil)}
            </p>
            <p className="text-xs text-muted-foreground mt-0.5">
              The user cannot sign in until the lockout clears.
            </p>
          </div>
          {isSuperuser && (
            <button
              onClick={() => setPending('unlock')}
              className="inline-flex items-center gap-2 h-8 px-3 rounded text-sm font-medium bg-status-error text-white hover:bg-status-error/90 flex-shrink-0"
            >
              <Unlock className="h-3.5 w-3.5" />
              Unlock now
            </button>
          )}
        </div>
      )}

      {user.tokensInvalidatedAt && (
        <div className="rounded-md border border-status-warning/40 bg-status-warning/10 p-4 flex items-start gap-3">
          <Clock className="h-5 w-5 text-status-warning flex-shrink-0 mt-0.5" />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">
              Tokens invalidated {formatRelativeTime(user.tokensInvalidatedAt)}
            </p>
            <p className="text-xs text-muted-foreground mt-0.5">
              User must re-login. Any sessions opened before this time have been revoked.
            </p>
          </div>
        </div>
      )}

      {/* Quick facts grid */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <FactCard
          icon={<ShieldCheck className="h-4 w-4" />}
          label="2FA"
          value={user.totpEnrolled ? 'Enrolled' : 'Off'}
          tone={user.totpEnrolled ? 'success' : 'muted'}
        />
        <FactCard
          icon={<Users className="h-4 w-4" />}
          label="Groups"
          value={String(user.groups?.length ?? 0)}
        />
        <FactCard
          icon={<Clock className="h-4 w-4" />}
          label="Last sign-in"
          value={user.lastLogin ? formatRelativeTime(user.lastLogin) : 'Never'}
        />
        <FactCard
          icon={<Users className="h-4 w-4" />}
          label="Enabled"
          value={user.enabled ? 'Yes' : 'No'}
          tone={user.enabled ? 'success' : 'danger'}
        />
      </div>

      {/* Actions */}
      <div className="space-y-2">
        <h2 className="text-sm font-medium text-foreground">Security actions</h2>
        {!isSuperuser ? (
          <p className="text-xs text-muted-foreground">
            Sign in as a superuser to access these actions.
          </p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            {ACTIONS.filter((a) => a.available(user)).map((a) => {
              const Icon = a.icon;
              return (
                <button
                  key={a.key}
                  onClick={() => setPending(a.key)}
                  disabled={mut.isPending}
                  className="text-left rounded-lg border border-border bg-card hover:bg-accent transition-colors p-4 disabled:opacity-50"
                >
                  <div className="flex items-start gap-3">
                    <div className="h-9 w-9 rounded-full bg-muted flex items-center justify-center flex-shrink-0">
                      <Icon className="h-4 w-4 text-foreground" />
                    </div>
                    <div className="min-w-0">
                      <p className="text-sm font-medium text-foreground">{a.label}</p>
                      <p className="text-xs text-muted-foreground mt-0.5 leading-relaxed">
                        {a.description}
                      </p>
                    </div>
                  </div>
                </button>
              );
            })}
          </div>
        )}
      </div>

      <ConfirmDialog
        open={!!activeAction}
        onClose={() => setPending(null)}
        onConfirm={() => activeAction && mut.mutate(activeAction)}
        title={activeAction?.label || ''}
        description={activeAction?.description || ''}
        confirmText={activeAction?.confirmText || 'Confirm'}
        variant={activeAction?.variant}
        loading={mut.isPending}
      />
    </div>
  );
}

function FactCard({
  icon,
  label,
  value,
  tone = 'muted',
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  tone?: 'muted' | 'success' | 'danger';
}) {
  const toneColor =
    tone === 'success'
      ? 'text-status-success'
      : tone === 'danger'
        ? 'text-status-error'
        : 'text-foreground';
  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        {icon}
        {label}
      </div>
      <p className={`mt-1.5 text-base font-semibold ${toneColor}`}>{value}</p>
    </div>
  );
}
