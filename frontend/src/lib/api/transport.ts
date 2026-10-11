import axios, {
  AxiosError,
  AxiosInstance,
  InternalAxiosRequestConfig,
} from "axios";

import {
  clearLegacyTokenStorage,
  browserCookieName,
  CSRF_COOKIE,
} from "@/lib/auth/session";
import { API_BASE } from "@/lib/env";
import { refreshSession, waitForRefresh } from "./authentication-refresh";

const CSRF_HEADER = "X-CSRF-Token";

type AstronomerRequestConfig = InternalAxiosRequestConfig & {
  _retry?: boolean;
};

function readCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const prefix = `${name}=`;
  const found = document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix));
  return found ? decodeURIComponent(found.slice(prefix.length)) : null;
}

function isUnsafeMethod(method?: string): boolean {
  const normalized = (method || "get").toUpperCase();
  return !["GET", "HEAD", "OPTIONS", "TRACE"].includes(normalized);
}

function csrfHeaders(): Record<string, string> {
  const token = readCookie(browserCookieName(CSRF_COOKIE));
  return token ? { [CSRF_HEADER]: token } : {};
}

/**
 * Single authenticated HTTP transport used by the generated OpenAPI client
 * and the isolated Kubernetes adapters. Responses remain byte-for-byte wire
 * shaped; domain clients own any deliberate wire-to-view mapping.
 */
const api: AxiosInstance = axios.create({
  baseURL: API_BASE,
  timeout: 30000,
  withCredentials: true,
  // OpenAPI form/explode arrays use repeated keys, without Axios brackets.
  paramsSerializer: { indexes: null },
  headers: { "Content-Type": "application/json" },
});

api.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    clearLegacyTokenStorage();
    if (isUnsafeMethod(config.method) && config.headers) {
      const token = readCookie(browserCookieName(CSRF_COOKIE));
      if (token) config.headers.set(CSRF_HEADER, token);
    }
    // Kubernetes proxy suffixes are opaque upstream paths. Every management
    // API route otherwise uses the canonical trailing slash contract.
    if (
      config.url &&
      !config.url.endsWith("/") &&
      !config.url.includes("?") &&
      !config.url.includes("/k8s/")
    ) {
      config.url += "/";
    }
    return config;
  },
  (error) => Promise.reject(error),
);

api.interceptors.response.use(
  (response) => response,
  async (
    error: AxiosError<{
      error?: { message?: string; code?: string };
      message?: string;
      code?: string;
    }>,
  ) => {
    if (axios.isCancel(error)) return Promise.reject(error);
    const originalRequest = error.config as AstronomerRequestConfig;
    const isSessionEndpoint =
      originalRequest?.url?.includes("/auth/login") ||
      originalRequest?.url?.includes("/auth/refresh") ||
      originalRequest?.url?.includes("/auth/logout");
    if (
      error.response?.status === 401 &&
      !originalRequest?._retry &&
      typeof window !== "undefined" &&
      !isSessionEndpoint
    ) {
      // Mark queued followers as well as the refresh owner. A second 401 must
      // surface to the caller instead of re-entering the refresh flow.
      originalRequest._retry = true;
      const refresh = refreshSession(
        `${API_BASE}/auth/refresh/`,
        { "Content-Type": "application/json", ...csrfHeaders() },
        () => {
          clearLegacyTokenStorage();
          if (!window.location.pathname.startsWith("/auth")) {
            window.location.href = `/auth/login?returnTo=${encodeURIComponent(window.location.pathname + window.location.search)}`;
          }
        },
      );
      await waitForRefresh(refresh, originalRequest.signal);
      return api(originalRequest);
    }

    const message =
      error.response?.data?.error?.message ||
      error.response?.data?.message ||
      error.message ||
      "An unexpected error occurred";
    const enriched = new Error(message) as Error & {
      status?: number;
      code?: string;
      response?: typeof error.response;
    };
    // A request that never received an HTTP response is a transport failure,
    // not an API error with an unknown shape. Preserve that distinction for
    // query surfaces so they can render an offline/reconnect state while
    // ordinary application errors remain error states.
    enriched.status = error.response?.status ?? 0;
    enriched.code =
      error.response?.data?.error?.code ?? error.response?.data?.code;
    enriched.response = error.response;
    return Promise.reject(enriched);
  },
);

export default api;
