'use strict';

function sameOrigin(raw, approved) {
  try {
    const url = new URL(raw);
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && url.origin === approved;
  } catch { return false; }
}

function pathMatches(pathname, cookiePath) {
  const p = cookiePath || '/';
  return pathname === p || (pathname.startsWith(p) && (p.endsWith('/') || pathname[p.length] === '/'));
}

function scopedHeaders(headers, raw, approved, cookies) {
  const out = { ...headers };
  if (!sameOrigin(raw, approved)) {
    for (const name of Object.keys(out)) {
      if (['cookie', 'authorization', 'proxy-authorization'].includes(name.toLowerCase())) delete out[name];
    }
    out.cookie = '';
    return out;
  }
  if (cookies && cookies.length) {
    for (const name of Object.keys(out)) if (name.toLowerCase() === 'cookie') delete out[name];
    const pathname = new URL(raw).pathname;
    const value = cookies.filter(c => pathMatches(pathname, c.path)).map(c => `${c.name}=${c.value}`).join('; ');
    out.cookie = value;
  }
  return out;
}

// Serialized into each new browser document. Configured credentials stay out
// of the native cookie jar, whose host/scheme/port boundaries are too broad.
function installOriginState(approved, cookies, storage) {
  const authorized = location.origin === approved;
  if (storage && authorized) {
    try { for (const [key, value] of Object.entries(storage)) localStorage.setItem(key, value); } catch {}
  }
  if (!cookies || !cookies.length) return;
  const descriptor = Object.getOwnPropertyDescriptor(Document.prototype, 'cookie');
  if (!descriptor || !descriptor.get || !descriptor.set) return;
  const values = authorized ? cookies.map(c => ({ ...c })) : [];
  const names = new Set(cookies.map(c => c.name));
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get() {
      const native = descriptor.get.call(document).split(';').map(x => x.trim()).filter(x => x && !names.has(x.split('=')[0]));
      const pathname = location.pathname;
      for (const c of values) {
        const p = c.path || '/';
        if (c.value !== null && (pathname === p || pathname.startsWith(p) && (p.endsWith('/') || pathname[p.length] === '/'))) native.push(`${c.name}=${c.value}`);
      }
      return native.join('; ');
    },
    set(raw) {
      const parts = String(raw).split(';');
      const equals = parts[0].indexOf('=');
      const name = parts[0].slice(0, equals).trim();
      if (equals < 0 || !names.has(name)) return descriptor.set.call(document, raw);
      // A target-side update to an injected name must also stay document-local.
      const expired = parts.slice(1).some(part => {
        const [key, ...value] = part.trim().split('=');
        return key.toLowerCase() === 'max-age' && Number(value.join('=')) <= 0 ||
          key.toLowerCase() === 'expires' && Date.parse(value.join('=')) <= Date.now();
      });
      for (const c of values) if (c.name === name) c.value = expired ? null : parts[0].slice(equals + 1);
    },
  });
}

async function forwardApproved(raw, options, approved, cookies = [], fetchImpl = fetch) {
  let url = raw;
  let current = { ...options };
  for (let hop = 0; hop <= 10; hop++) {
    if (!sameOrigin(url, approved)) throw new Error('Forwarding outside approved origin');
    const response = await fetchImpl(url, { ...current, headers: scopedHeaders(current.headers, url, approved, cookies), redirect: 'manual' });
    const location = response.headers.get('location');
    if (![301, 302, 303, 307, 308].includes(response.status) || !location) return { response };
    const next = new URL(location, url).href;
    if (response.body) await response.body.cancel();
    // Let the browser process cross-origin redirects, with its normal CORS and
    // network policy. The next interception strips configured credentials.
    if (!sameOrigin(next, approved)) return { redirectURL: next, redirectStatus: response.status };
    if (response.status === 303 && current.method !== 'HEAD' || [301, 302].includes(response.status) && current.method === 'POST') {
      current = { ...current, method: 'GET', body: undefined, headers: { ...current.headers } };
      for (const name of Object.keys(current.headers)) if (['content-type', 'content-length', 'transfer-encoding'].includes(name.toLowerCase())) delete current.headers[name];
    }
    url = next;
  }
  throw new Error('Too many redirects');
}

module.exports = { sameOrigin, scopedHeaders, installOriginState, forwardApproved };
