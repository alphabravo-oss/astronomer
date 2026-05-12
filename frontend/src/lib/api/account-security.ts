/**
 * Account-security API client — TOTP enrollment / verification, password
 * reset, logout-with-redirect, and admin user actions (unlock, force-logout,
 * disable-TOTP, resync-groups).
 *
 * Re-exported from ../api.ts via `export * from './api/account-security'`.
 *
 * The shared axios interceptor in ../api.ts camelizes snake_case keys, so
 * all response types here use camelCase even though the wire format is
 * snake_case.
 */

import axios, { AxiosError } from 'axios';
import api from '../api';
import type { APIResponse, User } from '@/types';

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || '/api/v1';

// Local camelization for the rare bypass-the-interceptor calls in this file
// (login challenge handling, password-reset endpoints). Mirrors the helper
// in lib/api.ts so callers get the same camelCase keys they get elsewhere.
function snakeToCamel(s: string): string {
  return s.replace(/_([a-z0-9])/g, (_, ch) => ch.toUpperCase());
}
function camelize<T = unknown>(value: T): T {
  if (Array.isArray(value)) return value.map(camelize) as unknown as T;
  if (value && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype) {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[snakeToCamel(k)] = camelize(v);
    }
    return out as unknown as T;
  }
  return value;
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
  qrPngBase64: string;
  sessionToken: string;
}

export interface TotpEnrollConfirm {
  recoveryCodes: string[];
}

export async function getTotpStatus(): Promise<TotpStatus> {
  const res = await api.get<APIResponse<TotpStatus>>('/auth/totp/status');
  return res.data.data;
}

export async function startTotpEnrollment(): Promise<TotpEnrollStart> {
  const res = await api.post<APIResponse<TotpEnrollStart>>('/auth/totp/enroll/start');
  return res.data.data;
}

export async function confirmTotpEnrollment(
  sessionToken: string,
  code: string,
): Promise<TotpEnrollConfirm> {
  const res = await api.post<APIResponse<TotpEnrollConfirm>>('/auth/totp/enroll/confirm', {
    session_token: sessionToken,
    code,
  });
  return res.data.data;
}

export async function disableTotp(password: string, code: string): Promise<void> {
  await api.post('/auth/totp/disable', { password, code });
}

export async function regenerateRecoveryCodes(code: string): Promise<TotpEnrollConfirm> {
  const res = await api.post<APIResponse<TotpEnrollConfirm>>(
    '/auth/totp/recovery-codes/regenerate',
    { code },
  );
  return res.data.data;
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
  error: 'totp_required' | 'totp_enrollment_required';
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
): Promise<VerifiedLogin> {
  // The /verify endpoint returns the same shape as a normal login success:
  // { token, refresh, user } wrapped in APIResponse.
  const res = await api.post<APIResponse<VerifiedLogin>>('/auth/totp/verify', {
    challenge_token: challengeToken,
    code,
  });
  return res.data.data;
}

/**
 * Login wrapper that surfaces the TOTP challenge instead of throwing. The
 * shared axios interceptor in ../api.ts converts non-2xx responses into a
 * plain `Error` whose `.message` is the server's message string — which
 * loses the challenge_token. So we call axios directly here and inspect
 * the raw 423 response.
 */
export type LoginResult =
  | { kind: 'ok'; token: string; refresh?: string; user: User }
  | { kind: 'challenge'; challenge: TotpChallenge };

export async function loginWithCredentialsChallengeAware(
  username: string,
  password: string,
): Promise<LoginResult> {
  // We bypass the shared `api` axios instance because its response-error
  // interceptor flattens errors into plain `Error(message)` and strips the
  // structured payload (and HTTP status) we need for the TOTP challenge.
  // Cookie / camelize behavior we *want* are reapplied below.
  try {
    const res = await axios.post<APIResponse<VerifiedLogin> | VerifiedLogin>(
      `${API_BASE_URL}/auth/login/`,
      { username, password },
      { headers: { 'Content-Type': 'application/json' } },
    );
    const raw = camelize(res.data) as { data?: VerifiedLogin } | VerifiedLogin;
    const body = ('data' in raw && raw.data ? raw.data : (raw as VerifiedLogin));
    return { kind: 'ok', token: body.token, refresh: body.refresh, user: body.user };
  } catch (err) {
    const axiosErr = err as AxiosError<{
      error?: 'totp_required' | 'totp_enrollment_required';
      challenge_token?: string;
      message?: string;
    }>;
    if (axiosErr.response?.status === 423 && axiosErr.response.data) {
      const body = axiosErr.response.data;
      if (
        (body.error === 'totp_required' || body.error === 'totp_enrollment_required') &&
        body.challenge_token
      ) {
        return {
          kind: 'challenge',
          challenge: { error: body.error, challengeToken: body.challenge_token },
        };
      }
    }
    const message = axiosErr.response?.data?.message || axiosErr.message || 'Login failed';
    throw new Error(message);
  }
}

// ============================================================
// Password reset
// ============================================================

export async function requestPasswordReset(email: string): Promise<void> {
  // Always 202 — server never reveals whether the address exists.
  await api.post('/auth/password-reset/request', { email });
}

export async function completePasswordReset(token: string, newPassword: string): Promise<void> {
  await api.post('/auth/password-reset/complete', {
    token,
    new_password: newPassword,
  });
}

// ============================================================
// Logout (with optional Dex single-logout redirect)
// ============================================================

export interface LogoutResult {
  revoked: boolean;
  redirectUrl?: string;
}

export async function logoutCurrentSession(): Promise<LogoutResult> {
  const res = await api.post<APIResponse<LogoutResult>>('/auth/logout');
  // Backend wraps in APIResponse for SSO sessions but may return the bare
  // body for local users; tolerate both.
  const body = (res.data?.data ?? (res.data as unknown)) as LogoutResult;
  return {
    revoked: Boolean(body?.revoked),
    redirectUrl: body?.redirectUrl,
  };
}

// ============================================================
// Admin user actions
// ============================================================

export async function adminUnlockUser(userId: string): Promise<void> {
  await api.post(`/admin/users/${userId}/unlock`);
}

export async function adminForceLogoutUser(userId: string): Promise<void> {
  await api.post(`/admin/users/${userId}/force-logout`);
}

export async function adminDisableUserTotp(userId: string): Promise<void> {
  await api.post(`/admin/users/${userId}/disable-totp`);
}

export async function adminResyncUserGroups(userId: string): Promise<void> {
  await api.post(`/admin/users/${userId}/resync-groups`);
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

export async function getAdminUser(userId: string): Promise<AdminUserDetail> {
  const res = await api.get<APIResponse<AdminUserDetail>>(`/admin/users/${userId}`);
  return res.data.data;
}
