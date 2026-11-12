/**
 * api-recon coverage preload script: reference template
 *
 * Adapt this template to the target site before injection.
 * Typical changes: loginPathRe, stubs, neutralize.fields/success, apiPattern, mockTier, forward
 *
 * Inject at document start (CDP addScriptToEvaluateOnNewDocument or userscript)
 * Keep CONFIG fields consistent with recon/config.json
 */
(function () {
  'use strict';

  const CONFIG = {
    loginPathRe: /\/(login|signin)(\/|$|\?)/i,
    mockTier: 'L1+L2',
    forward: true,
    neutralize: {
      fields: ['response_code', 'code', 'errno', 'ret', 'status'],
      success: 0,
      flags: { success: true, message: 'ok' },
    },
    stubs: [],
    apiPattern: /\/(api|apis|v\d+|dev|internal|graphql)\//i,
    apiFallbackRe: /^\/(api|apis|v\d+|dev|internal)\//i,
    // API discovery: fetch/XHR hooks, request-header and response capture
    recordDetail: true,           // Capture method/url/headers/body/response details
    respMax: 600,
    responseBudget: { maxResponseBytes: 8 * 1024 * 1024, maxTotalBytes: 64 * 1024 * 1024,
      maxResponses: 2048, maxConcurrent: 8, responseTimeoutMs: 20000 },
    extractUrlsFromResponse: true, // Extract nested URLs from JSON responses
    neutralizeVueRouter: true,    // Neutralize Vue beforeEach/push login redirects
    observe: {
      storageReads: false,        // Observe localStorage.getItem to identify session keys
      cookieReads: false,         // Observe document.cookie reads
      xhrHeaders: true,           // Record XHR setRequestHeader
    },
  };

  window.__API_RECON_PRELOAD__ = true;
  window.__API_RECON_LOG__ = window.__API_RECON_LOG__ || new Set();
  window.__API_RECON_DETAIL__ = window.__API_RECON_DETAIL__ || [];
  window.__API_RECON_ROUTES__ = window.__API_RECON_ROUTES__ || new Set();
  window.__API_RECON_OBSERVE__ = window.__API_RECON_OBSERVE__ || { storage: [], headers: [] };
  const budget = window.__API_RECON_BUDGET__ = {
    ...CONFIG.responseBudget, bytes: 0, retainedBytes: 0, responses: 0, active: 0, omitted: 0,
  };
  for (const key of Object.keys(CONFIG.responseBudget)) {
    if (!Number.isSafeInteger(budget[key]) || budget[key] <= 0) throw new Error('Response budgets must be positive safe integers');
  }

  function retain(collection, value) {
    if (collection instanceof Set && collection.has(value)) return;
    const count = collection instanceof Set ? collection.size : collection.length;
    let encoded;
    try { encoded = JSON.stringify(value); } catch (e) { budget.omitted++; return; }
    if (encoded === undefined) { budget.omitted++; return; }
    const bytes = encoded.length * 2;
    if (count >= budget.maxResponses || budget.retainedBytes + bytes > budget.maxTotalBytes) {
      budget.omitted++;
      return;
    }
    budget.retainedBytes += bytes;
    if (collection instanceof Set) collection.add(value);
    else collection.push(JSON.parse(encoded)); // Caller mutation cannot expand retained metadata later.
  }

  async function inspectResponse(resp) {
    if (budget.responses >= budget.maxResponses || budget.bytes >= budget.maxTotalBytes || budget.active >= budget.maxConcurrent) {
      budget.omitted++;
      return { text: '', error: 'inspection budget unavailable' };
    }
    if (/text\/event-stream/i.test(resp.headers.get('content-type') || '')) {
      return { text: '', error: 'streaming response not inspected' };
    }
    budget.responses++;
    budget.active++;
    let reader, timer, text = '', size = 0;
    const decoder = new TextDecoder();
    try {
      const clone = resp.clone();
      if (!clone.body) return { text: '', error: '' };
      reader = clone.body.getReader();
      const timeout = new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error('inspection deadline exceeded')), budget.responseTimeoutMs);
      });
      for (;;) {
        const { value, done } = await Promise.race([reader.read(), timeout]);
        if (done) return { text: text + decoder.decode(), error: '' };
        const remaining = Math.min(budget.maxResponseBytes - size, budget.maxTotalBytes - budget.bytes);
        const keep = Math.min(value.byteLength, Math.max(0, remaining));
        if (keep) {
          text += decoder.decode(value.subarray(0, keep), { stream: true });
          size += keep;
          budget.bytes += keep;
        }
        if (value.byteLength > remaining) {
          budget.omitted++;
          return { text: text + decoder.decode(), error: 'inspection byte budget exceeded' };
        }
      }
    } catch (e) {
      budget.omitted++;
      return { text, error: 'inspection failed: ' + e.message };
    } finally {
      clearTimeout(timer);
      budget.active--;
      // A tee branch's cancel promise waits for the original branch. Never
      // await it: return that untouched original so the application can read it.
      if (reader) void reader.cancel().catch(() => {});
    }
  }

  function trunc(s, n) {
    n = n || CONFIG.respMax || 600;
    s = String(s == null ? '' : s);
    return s.length > n ? s.slice(0, n) + '…' : s;
  }

  function extractApiUrlsFromText(text) {
    if (!CONFIG.extractUrlsFromResponse || !text) return;
    const re = /["'](\/(?:api|apis|v\d+|dev|internal|graphql)[^"'`\\s]*)["'`]/gi;
    let m;
    while ((m = re.exec(text))) {
      const p = m[1].split('?')[0];
      retain(window.__API_RECON_LOG__, 'GET ' + p);
    }
    const broad = /\/api\/[a-zA-Z0-9_./-]+/g;
    while ((m = broad.exec(text))) {
      const p = m[0].split('?')[0];
      if (!/\.(svg|png|jpg|gif|ico)$/i.test(p)) retain(window.__API_RECON_LOG__, 'GET ' + p);
    }
  }

  function recordApi(url, method) {
    const p = String(url || '').split('?')[0];
    if (CONFIG.apiPattern.test(p)) {
      retain(window.__API_RECON_LOG__, (method || 'GET').toUpperCase() + ' ' + p);
    }
  }

  function recordApiDetail(entry) {
    recordApi(entry.url, entry.method);
    if (!CONFIG.recordDetail) return;
    retain(window.__API_RECON_DETAIL__, entry);
    if (entry.responseBody) extractApiUrlsFromText(entry.responseBody);
  }

  function blocked(url) {
    return url && CONFIG.loginPathRe.test(String(url));
  }

  // --- Redirect neutralization ---
  (function neutralizeNativeNavigation() {
    const rawAssign = Location.prototype.assign;
    const rawReplace = Location.prototype.replace;
    Location.prototype.assign = function (url) {
      if (blocked(url)) return;
      return rawAssign.call(this, url);
    };
    Location.prototype.replace = function (url) {
      if (blocked(url)) return;
      return rawReplace.call(this, url);
    };

    const rawPush = history.pushState;
    const rawRep = history.replaceState;
    history.pushState = function (s, t, url) {
      if (blocked(url)) return;
      return rawPush.apply(this, arguments);
    };
    history.replaceState = function (s, t, url) {
      if (blocked(url)) return;
      return rawRep.apply(this, arguments);
    };

    const hrefDesc = Object.getOwnPropertyDescriptor(Location.prototype, 'href');
    if (hrefDesc && hrefDesc.set) {
      const nativeSet = hrefDesc.set;
      Object.defineProperty(Location.prototype, 'href', {
        configurable: true,
        enumerable: hrefDesc.enumerable,
        get: hrefDesc.get,
        set(url) {
          if (blocked(url)) return;
          return nativeSet.call(this, url);
        },
      });
    }
    window.close = function () {};
  })();

  // --- Vue Router login-redirect neutralization ---
  function neutralizeVueRouter() {
    if (!CONFIG.neutralizeVueRouter) return;
    try {
      const el = document.querySelector('[data-v-app]');
      const app = (el && el.__vue_app__) || window.__VUE__;
      if (!app) return;
      const router = app.config && app.config.globalProperties && app.config.globalProperties.$router;
      if (!router) return;
      router.beforeEach(function (to, from, next) { next(); });
      if (router.beforeResolve) router.beforeResolve(function (to, from, next) { next(); });
      function wrapNav(fn) {
        return function (loc) {
          const path = typeof loc === 'string' ? loc : (loc && (loc.path || loc.fullPath)) || '';
          if (blocked(path)) return Promise.resolve();
          return fn.apply(this, arguments);
        };
      }
      router.push = wrapNav(router.push.bind(router));
      router.replace = wrapNav(router.replace.bind(router));
      if (router.getRoutes) {
        router.getRoutes().forEach(function (r) {
          if (r.path) retain(window.__API_RECON_ROUTES__, r.path);
        });
      }
    } catch (e) { /* ignore */ }
  }

  function runPostLoadHooks() {
    neutralizeVueRouter();
    // Application navigation functions (goPage / navigateTo, etc.)
    ['goPage', 'navigateTo', 'jumpTo', 'redirectTo'].forEach(function (name) {
      if (typeof window[name] !== 'function' || window[name].__apiReconWrapped) return;
      const raw = window[name];
      window[name] = function () {
        const arg = arguments[0];
        const path = typeof arg === 'string' ? arg : (arg && arg.path) || '';
        if (blocked(path)) return;
        return raw.apply(this, arguments);
      };
      window[name].__apiReconWrapped = true;
    });
  }
  document.addEventListener('DOMContentLoaded', runPostLoadHooks);
  window.addEventListener('load', runPostLoadHooks);

  // --- Observation hooks for session keys and headers (storage/cookie disabled by default) ---
  if (CONFIG.observe && CONFIG.observe.storageReads) {
    const rawGet = Storage.prototype.getItem;
    Storage.prototype.getItem = function (key) {
      retain(window.__API_RECON_OBSERVE__.storage, { type: 'getItem', key: key, at: location.pathname });
      return rawGet.apply(this, arguments);
    };
  }
  if (CONFIG.observe && CONFIG.observe.cookieReads) {
    const cookieDesc = Object.getOwnPropertyDescriptor(Document.prototype, 'cookie');
    if (cookieDesc && cookieDesc.get) {
      const nativeGet = cookieDesc.get;
      Object.defineProperty(document, 'cookie', {
        configurable: true,
        get: function () {
          retain(window.__API_RECON_OBSERVE__.storage, { type: 'cookieRead', at: location.pathname });
          return nativeGet.call(this);
        },
        set: cookieDesc.set,
      });
    }
  }

  // --- Mock helpers ---
  const NEGATIVE_RE = /未登录|未授权|授权|not\s*login|unauthorized|forbidden/i;
  const tier = CONFIG.mockTier || 'L1+L2';

  function patchJsonBody(text) {
    if (!tier.includes('L2')) return text;
    try {
      const j = JSON.parse(text);
      if (j && typeof j === 'object') {
        const msg = String(j.message || j.msg || '');
        for (const f of CONFIG.neutralize.fields) {
          if (f in j && j[f] !== CONFIG.neutralize.success && NEGATIVE_RE.test(msg)) {
            j[f] = CONFIG.neutralize.success;
          }
        }
        Object.assign(j, CONFIG.neutralize.flags || {});
        if (j.data == null) j.data = {};
        return JSON.stringify(j);
      }
    } catch (e) { /* ignore */ }
    return text;
  }

  function lookupPrecise(url) {
    if (!tier.includes('L1')) return null;
    const p = String(url || '');
    for (const s of CONFIG.stubs) {
      if (s._re ? s._re.test(p) : new RegExp(s.match).test(p)) return s.body;
    }
    return null;
  }

  function fallbackBody(url) {
    const p = String(url).split('?')[0];
    if (/\/(list|search|query|page|all|tree|nodes|options)/i.test(p)) {
      return { response_code: 0, data: [] };
    }
    if (/\/(get|detail|info|config|status)/i.test(p)) {
      return { response_code: 0, data: {} };
    }
    return { response_code: 0, data: {}, success: true };
  }

  function matchMock(url) {
    const precise = lookupPrecise(url);
    if (precise) return precise;
    if (tier.includes('L3') && CONFIG.apiFallbackRe.test(String(url || '').split('?')[0])) {
      return fallbackBody(url);
    }
    return null;
  }

  CONFIG.stubs.forEach(function (s) {
    if (s.match && !s._re) s._re = new RegExp(s.match);
  });

  // --- fetch Hook ---
  const rawFetch = window.fetch;
  window.fetch = async function (input, init) {
    const url = typeof input === 'string' ? input : (input && input.url) || '';
    const method = ((init && init.method) || 'GET').toUpperCase();
    const reqHeaders = (init && init.headers) || {};
    const reqBody = init && init.body ? trunc(init.body) : null;

    const mock = matchMock(url);
    if (mock && !CONFIG.forward) {
      recordApiDetail({ method: method, url: url, reqHeaders: reqHeaders, reqBody: reqBody, status: 200, responseBody: JSON.stringify(mock), source: 'mock' });
      return new Response(JSON.stringify(mock), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }

    const resp = await rawFetch.apply(this, arguments);
    const inspection = await inspectResponse(resp);
    const text = inspection.text;

    recordApiDetail({
      method: method, url: url, reqHeaders: reqHeaders, reqBody: reqBody,
      status: resp.status, responseBody: trunc(text), source: 'fetch',
      captureError: inspection.error || undefined,
    });

    if (inspection.error || !tier.includes('L2') && !mock) return resp;

    const patched = patchJsonBody(text);
    if (patched !== text) {
      if (resp.body) void resp.body.cancel().catch(() => {});
      return new Response(patched, {
        status: resp.status,
        statusText: resp.statusText,
        headers: resp.headers,
      });
    }
    return resp;
  };

  // --- XHR Hook ---
  const rawOpen = XMLHttpRequest.prototype.open;
  const rawSend = XMLHttpRequest.prototype.send;
  const rawSetHeader = XMLHttpRequest.prototype.setRequestHeader;

  XMLHttpRequest.prototype.open = function (method, url) {
    this.__apiReconUrl = url;
    this.__apiReconMethod = method;
    this.__apiReconHeaders = {};
    return rawOpen.apply(this, arguments);
  };

  if (CONFIG.observe && CONFIG.observe.xhrHeaders) {
    XMLHttpRequest.prototype.setRequestHeader = function (name, value) {
      if (!this.__apiReconHeaders) this.__apiReconHeaders = {};
      this.__apiReconHeaders[name] = value;
      retain(window.__API_RECON_OBSERVE__.headers, { name: name, url: this.__apiReconUrl });
      return rawSetHeader.apply(this, arguments);
    };
  }

  XMLHttpRequest.prototype.send = function (body) {
    const xhr = this;
    const url = xhr.__apiReconUrl || '';
    const method = (xhr.__apiReconMethod || 'GET').toUpperCase();
    const mock = matchMock(url);

    if (mock && !CONFIG.forward) {
      const bodyStr = JSON.stringify(mock);
      recordApiDetail({ method: method, url: url, reqHeaders: xhr.__apiReconHeaders, reqBody: trunc(body), status: 200, responseBody: bodyStr, source: 'mock' });
      setTimeout(function () {
        Object.defineProperty(xhr, 'readyState', { configurable: true, get: function () { return 4; } });
        Object.defineProperty(xhr, 'status', { configurable: true, get: function () { return 200; } });
        Object.defineProperty(xhr, 'responseText', { configurable: true, get: function () { return bodyStr; } });
        xhr.onreadystatechange && xhr.onreadystatechange();
        xhr.onload && xhr.onload();
      }, 0);
      return;
    }

    const orig = xhr.onreadystatechange;
    xhr.onreadystatechange = function () {
      if (xhr.readyState === 4) {
        let responseText = '', captureError = '';
        try { responseText = xhr.responseText; } catch (e) { captureError = 'non-text XHR response'; }
        if (!captureError) {
          if (budget.responses >= budget.maxResponses || budget.bytes >= budget.maxTotalBytes) {
            captureError = 'inspection budget unavailable';
          } else if (responseText.length > budget.maxResponseBytes) {
            captureError = 'inspection byte budget exceeded';
          } else {
            // Native XHR owns its body. Bound additional encoding/JSON work;
            // never replace or abort an oversized application response.
            const bytes = new TextEncoder().encode(responseText).byteLength;
            budget.responses++;
            if (bytes > budget.maxResponseBytes || bytes > budget.maxTotalBytes - budget.bytes) {
              captureError = 'inspection byte budget exceeded';
            } else budget.bytes += bytes;
          }
        }
        if (captureError) budget.omitted++;
        recordApiDetail({
          method: method, url: url, reqHeaders: xhr.__apiReconHeaders,
          reqBody: trunc(body), status: xhr.status,
          responseBody: trunc(responseText), source: 'xhr', captureError: captureError || undefined,
        });
        if (!captureError && tier.includes('L2') && responseText) {
          try {
            const patched = patchJsonBody(responseText);
            if (patched !== responseText) {
              Object.defineProperty(xhr, 'responseText', { configurable: true, get: function () { return patched; } });
            }
          } catch (e) { /* ignore */ }
        }
      }
      orig && orig.apply(this, arguments);
    };
    return rawSend.apply(this, arguments);
  };
})();
