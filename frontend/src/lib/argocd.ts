// Shared ArgoCD instance URL helpers.

// isBrowserReachable returns false for cluster-internal URLs (`*.svc.cluster.local`,
// `localhost`, RFC1918 IPs) — valid for the in-cluster Astronomer server to reach
// but a browser can't follow them. Used to decide whether an instance's apiUrl
// can be opened directly or must go through the same-origin /argocd/ proxy.
export function isBrowserReachable(url: string): boolean {
  if (!url) return false;
  try {
    const u = new URL(url);
    const h = u.hostname;
    if (h.endsWith('.svc.cluster.local') || h.endsWith('.svc')) return false;
    if (h === 'localhost' || h === '127.0.0.1' || h === '::1') return false;
    if (/^10\./.test(h) || /^192\.168\./.test(h)) return false;
    if (/^172\.(1[6-9]|2\d|3[0-1])\./.test(h)) return false;
    return true;
  } catch {
    return false;
  }
}

// argoInstanceWebHref is the URL that opens the ArgoCD UI in a browser.
// A browser-reachable apiUrl opens directly; an in-cluster instance (the local
// self-managed ArgoCD, whose apiUrl is a *.svc.cluster.local address) is served
// on the same origin through the /argocd/ reverse proxy.
export function argoInstanceWebHref(apiUrl: string): string {
  return isBrowserReachable(apiUrl) ? apiUrl : '/argocd/applications';
}
