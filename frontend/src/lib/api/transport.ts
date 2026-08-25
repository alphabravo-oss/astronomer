import axios, {
  AxiosError,
  AxiosInstance,
  InternalAxiosRequestConfig,
} from "axios";

import { clearLegacyTokenStorage } from "@/lib/auth/session";
import { API_BASE } from "@/lib/env";

const CSRF_COOKIE = "astronomer_csrf";
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
  const token = readCookie(CSRF_COOKIE);
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
  headers: { "Content-Type": "application/json" },
});

api.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    clearLegacyTokenStorage();
    if (isUnsafeMethod(config.method) && config.headers) {
      const token = readCookie(CSRF_COOKIE);
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

let isRefreshing = false;
let failedQueue: Array<{
  resolve: () => void;
  reject: (error: unknown) => void;
}> = [];

function processQueue(error: unknown, ok: boolean) {
  for (const pending of failedQueue) {
    if (ok) pending.resolve();
    else pending.reject(error);
  }
  failedQueue = [];
}

api.interceptors.response.use(
  (response) => response,
  async (
    error: AxiosError<{
      error?: { message?: string; code?: string };
      message?: string;
      code?: string;
    }>,
  ) => {
    const originalRequest = error.config as AstronomerRequestConfig;
    if (
      error.response?.status === 401 &&
      !originalRequest?._retry &&
      typeof window !== "undefined"
    ) {
      if (
        originalRequest.url?.includes("/auth/login") ||
        originalRequest.url?.includes("/auth/refresh")
      ) {
        return Promise.reject(error);
      }
      if (isRefreshing) {
        return new Promise<void>((resolve, reject) =>
          failedQueue.push({ resolve, reject }),
        ).then(() => api(originalRequest));
      }
      originalRequest._retry = true;
      isRefreshing = true;
      try {
        const response = await axios.post(
          `${API_BASE}/auth/refresh/`,
          {},
          {
            headers: { "Content-Type": "application/json", ...csrfHeaders() },
            withCredentials: true,
          },
        );
        if (!(response.data?.data || response.data)?.token)
          throw new Error("Refresh did not return an access token");
        processQueue(null, true);
        return api(originalRequest);
      } catch (refreshError) {
        processQueue(refreshError, false);
        clearLegacyTokenStorage();
        if (!window.location.pathname.startsWith("/auth")) {
          window.location.href = `/auth/login?returnTo=${encodeURIComponent(window.location.pathname + window.location.search)}`;
        }
        return Promise.reject(refreshError);
      } finally {
        isRefreshing = false;
      }
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
    enriched.status = error.response?.status;
    enriched.code =
      error.response?.data?.error?.code ?? error.response?.data?.code;
    enriched.response = error.response;
    return Promise.reject(enriched);
  },
);

export default api;
