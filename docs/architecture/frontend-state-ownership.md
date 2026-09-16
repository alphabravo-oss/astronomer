# Frontend state ownership

Status: accepted architecture decision (2026-09-10)

Astronomer uses one state mechanism for each kind of data:

| Data                              | Owner                                    | Reason                                                                       |
| --------------------------------- | ---------------------------------------- | ---------------------------------------------------------------------------- |
| HTTP request/response data        | TanStack Query                           | Server state, caching, retries, invalidation, and request cancellation       |
| Kubernetes change events          | TanStack Query + shared SSE invalidation | One cache, event-routed freshness, and polling only while SSE is unavailable |
| Small persistent UI preferences   | App-owned React browser state            | Synchronous local UI state with explicit, scoped persistence                 |
| Component-local interaction state | React state/reducer                      | No cross-screen lifetime                                                     |

TanStack Query is the sole frontend server-state cache. The shared SSE
dispatcher maps each event family to its canonical Query keys; `liveFallback`
restores bounded polling while that stream is unavailable. No secondary
collection cache may fold watch frames beside Query.

`lib/browser-state.ts` is reserved for app-wide browser chrome (for example,
the sidebar and window manager) and must never cache HTTP responses. It uses
React's `useSyncExternalStore` primitive and persists only fields named by the
owning state module.
