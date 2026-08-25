/**
 * Account-security API client — TOTP enrollment / verification, password
 * reset, logout-with-redirect, and admin user actions (unlock, force-logout,
 * disable-TOTP, resync-groups).
 *
 * Re-exported from ../api.ts via `export * from './api/account-security'`.
 *
 * Generated operations return raw snake_case wire objects; this module maps
 * them explicitly into the camelCase view types consumed by the UI.
 */

import type { User } from "@/types";
import { mapCurrentUser } from "@/lib/api/auth";
import {
  getAuthTotpStatus,
  getUsersById,
  postAdminUsersByIdDisableTotp,
  postAdminUsersByIdForceLogout,
  postAdminUsersByIdResyncGroups,
  postAdminUsersByIdUnlock,
  postAuthLogout,
  postAuthLogin,
  postAuthPasswordResetComplete,
  postAuthPasswordResetRequest,
  postAuthTotpDisable,
  postAuthTotpEnrollConfirm,
  postAuthTotpEnrollStart,
  postAuthTotpRecoveryCodesRegenerate,
  postAuthTotpVerify,
} from "@/lib/api/generated/client";

export interface AccountSecurityRequestOptions {
  signal?: AbortSignal;
}

// ============================================================
// TOTP
// ============================================================

export interface TotpStatus {
  enrolled: boolean;
  lastUsedAt?: string | null;
  recoveryCodesRemaining: number;
}

export interface TotpEnrollStart {
  otpauthUrl: string;
  qrDataUrl: string;
  sessionToken: string;
  challenge: string;
}

export interface TotpEnrollConfirm {
  recoveryCodes: string[];
}

export async function getTotpStatus(
  options?: AccountSecurityRequestOptions,
): Promise<TotpStatus> {
  const wire = await getAuthTotpStatus({ signal: options?.signal });
  return {
    enrolled: wire.enrolled ?? false,
    lastUsedAt: wire.last_used_at,
    recoveryCodesRemaining: wire.recovery_codes_remaining ?? 0,
  };
}

export async function startTotpEnrollment(
  options?: AccountSecurityRequestOptions,
): Promise<TotpEnrollStart> {
  const wire = await postAuthTotpEnrollStart({ signal: options?.signal });
  if (
    !wire.otpauth_url ||
    !wire.qr_data_url ||
    !wire.challenge_token ||
    !wire.challenge
  ) {
    throw new Error("TOTP enrollment response omitted required setup data");
  }
  return {
    otpauthUrl: wire.otpauth_url,
    qrDataUrl: wire.qr_data_url,
    sessionToken: wire.challenge_token,
    challenge: wire.challenge,
  };
}

export async function confirmTotpEnrollment(
  sessionToken: string,
  challenge: string,
  code: string,
  options?: AccountSecurityRequestOptions,
): Promise<TotpEnrollConfirm> {
  const wire = await postAuthTotpEnrollConfirm({
    body: {
      challenge_token: sessionToken,
      challenge,
      code,
    },
    signal: options?.signal,
  });
  return { recoveryCodes: wire.recovery_codes ?? [] };
}

export async function disableTotp(
  password: string,
  code: string,
  options?: AccountSecurityRequestOptions,
): Promise<void> {
  await postAuthTotpDisable({
    body: { password, code },
    signal: options?.signal,
  });
}

export async function regenerateRecoveryCodes(
  code: string,
  options?: AccountSecurityRequestOptions,
): Promise<TotpEnrollConfirm> {
  const wire = await postAuthTotpRecoveryCodesRegenerate({
    body: { code },
    signal: options?.signal,
  });
  return { recoveryCodes: wire.recovery_codes ?? [] };
}

// ============================================================
// TOTP login challenge
// ============================================================

/**
 * Result of POSTing /auth/login. On a fully-authenticated success we get
 * { token, refresh, user }. On TOTP-required we get HTTP 423 with a
 * { error, challenge_token } payload — handled at the call site (see
 * `loginWithCredentialsChallengeAware` below).
 */
export interface TotpChallenge {
  /** 'totp_required' — user already has TOTP enrolled, prompt for code */
  /** 'totp_enrollment_required' — operator policy mandates TOTP; force enrollment */
  error: "totp_required" | "totp_enrollment_required";
  challengeToken: string;
}

export interface VerifiedLogin {
  token: string;
  refresh?: string;
  user: User;
}

export async function verifyTotpChallenge(
  challengeToken: string,
  code: string,
  options?: AccountSecurityRequestOptions,
): Promise<VerifiedLogin> {
  // The /verify endpoint returns the same shape as a normal login success:
  // { token, refresh, user } wrapped in APIResponse.
  const wire = (
    await postAuthTotpVerify({
      body: { challenge_token: challengeToken, code },
      signal: options?.signal,
    })
  ).data;
  if (!wire?.token || !wire.user) {
    throw new Error("TOTP verification response omitted session data");
  }
  return {
    token: wire.token,
    refresh: wire.refresh,
    user: mapCurrentUser(wire.user),
  };
}

/**
 * Login wrapper that surfaces the typed TOTP challenge instead of throwing.
 */
export type LoginResult =
  | { kind: "ok"; token: string; refresh?: string; user: User }
  | { kind: "challenge"; challenge: TotpChallenge };

export async function loginWithCredentialsChallengeAware(
  email: string,
  password: string,
  options?: AccountSecurityRequestOptions,
): Promise<LoginResult> {
  try {
    const body = (
      await postAuthLogin({
        body: { email, password },
        signal: options?.signal,
      })
    ).data;
    if (!body?.token || !body.user) {
      throw new Error("Login response omitted session data");
    }
    return {
      kind: "ok",
      token: body.token,
      refresh: body.refresh,
      user: mapCurrentUser(body.user),
    };
  } catch (err) {
    const requestError = err as Error & {
      status?: number;
      response?: { status?: number; data?: unknown };
    };
    const body = requestError.response?.data as
      | { error?: string; challenge_token?: string; message?: string }
      | undefined;
    if (
      (requestError.status ?? requestError.response?.status) === 423 &&
      body
    ) {
      if (
        (body.error === "totp_required" ||
          body.error === "totp_enrollment_required") &&
        body.challenge_token
      ) {
        return {
          kind: "challenge",
          challenge: {
            error: body.error,
            challengeToken: body.challenge_token,
          },
        };
      }
    }
    const message = body?.message || requestError.message || "Login failed";
    throw new Error(message);
  }
}

// ============================================================
// Password reset
// ============================================================

export async function requestPasswordReset(
  email: string,
  options?: AccountSecurityRequestOptions,
): Promise<void> {
  // Always 202 — server never reveals whether the address exists.
  await postAuthPasswordResetRequest({
    body: { email },
    signal: options?.signal,
  });
}

export async function completePasswordReset(
  token: string,
  newPassword: string,
  options?: AccountSecurityRequestOptions,
): Promise<void> {
  await postAuthPasswordResetComplete({
    body: { token, new_password: newPassword },
    signal: options?.signal,
  });
}

// ============================================================
// Logout (with optional Dex single-logout redirect)
// ============================================================

export interface LogoutResult {
  revoked: boolean;
  redirectUrl?: string;
}

export async function logoutCurrentSession(
  options?: AccountSecurityRequestOptions,
): Promise<LogoutResult> {
  const res = await postAuthLogout({ signal: options?.signal });
  // Backend wraps in APIResponse for SSO sessions but may return the bare
  // body for local users; tolerate both.
  const body = ((res as { data?: unknown }).data ?? res) as {
    revoked?: unknown;
    redirect_url?: unknown;
    redirectUrl?: unknown;
  };
  return {
    revoked: Boolean(body?.revoked),
    redirectUrl:
      typeof (body.redirect_url ?? body.redirectUrl) === "string"
        ? ((body.redirect_url ?? body.redirectUrl) as string)
        : undefined,
  };
}

// ============================================================
// Admin user actions
// ============================================================

export async function adminUnlockUser(
  userId: string,
  options?: AccountSecurityRequestOptions,
): Promise<void> {
  await postAdminUsersByIdUnlock({
    path: { id: userId },
    signal: options?.signal,
  });
}

export async function adminForceLogoutUser(
  userId: string,
  options?: AccountSecurityRequestOptions,
): Promise<void> {
  await postAdminUsersByIdForceLogout({
    path: { id: userId },
    signal: options?.signal,
  });
}

export async function adminDisableUserTotp(
  userId: string,
  options?: AccountSecurityRequestOptions,
): Promise<void> {
  await postAdminUsersByIdDisableTotp({
    path: { id: userId },
    signal: options?.signal,
  });
}

export async function adminResyncUserGroups(
  userId: string,
  options?: AccountSecurityRequestOptions,
): Promise<void> {
  await postAdminUsersByIdResyncGroups({
    path: { id: userId },
    signal: options?.signal,
  });
}

/**
 * Admin-facing detail view of a user. Mirrors the standard `User` shape but
 * includes the security-state fields used by the admin actions UI.
 */
export interface AdminUserDetail extends User {
  isSuperuser?: boolean;
  lockedUntil?: string | null;
  tokensInvalidatedAt?: string | null;
  totpEnrolled?: boolean;
  groups?: string[];
}

export async function getAdminUser(
  userId: string,
  options?: AccountSecurityRequestOptions,
): Promise<AdminUserDetail> {
  const wire = (
    await getUsersById({ path: { id: userId }, signal: options?.signal })
  ).data;
  if (!wire?.id || !wire.username || !wire.email) {
    throw new Error("getAdminUser returned no user data");
  }
  const provider = ["local", "github", "google", "oidc", "saml"].includes(
    wire.provider ?? "",
  )
    ? (wire.provider as User["provider"])
    : "local";
  return {
    id: wire.id,
    username: wire.username,
    email: wire.email,
    displayName: wire.displayName || wire.username,
    provider,
    globalRoles: wire.globalRoles ?? [],
    isSuperuser: wire.is_superuser ?? false,
    is_superuser: wire.is_superuser ?? false,
    enabled: wire.enabled ?? false,
    lastLogin: wire.lastLogin ?? "",
    createdAt: wire.createdAt ?? "",
  };
}
