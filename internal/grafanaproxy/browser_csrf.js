// Grafana shares Astronomer's origin and session. Preserve Astronomer's
// double-submit CSRF protection for Grafana's fetch and XHR API requests.
(() => {
  const base = new URL(document.baseURI);
  const tokenFor = (url, method) => {
    const target = new URL(url, location.href);
    if (target.origin !== location.origin || !target.pathname.startsWith(base.pathname) || /^(GET|HEAD|OPTIONS)$/i.test(method)) return '';
    const cookie = document.cookie.split('; ').find(value => value.startsWith('astronomer_csrf='));
    return cookie ? decodeURIComponent(cookie.slice('astronomer_csrf='.length)) : '';
  };
  const fetch = window.fetch;
  window.fetch = function(input, init) {
    const request = input instanceof Request ? input : undefined;
    const token = tokenFor(request ? request.url : input, init?.method || request?.method || 'GET');
    if (token) {
      const headers = new Headers(init?.headers || request?.headers);
      headers.set('X-CSRF-Token', token);
      init = { ...init, headers };
    }
    return fetch.call(this, input, init);
  };
  const open = XMLHttpRequest.prototype.open;
  const send = XMLHttpRequest.prototype.send;
  const targets = new WeakMap();
  XMLHttpRequest.prototype.open = function(method, url, ...rest) {
    targets.set(this, { method, url });
    return open.call(this, method, url, ...rest);
  };
  XMLHttpRequest.prototype.send = function(body) {
    const target = targets.get(this);
    const token = target && tokenFor(target.url, target.method);
    if (token) this.setRequestHeader('X-CSRF-Token', token);
    return send.call(this, body);
  };
})();
