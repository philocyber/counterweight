'use strict';

const { Agent, ProxyAgent, fetch: transportFetch } = require('undici');
const { sameOrigin } = require('./credential_policy');

function createTargetTLS(base, config = {}, proxy = '') {
  const approved = new URL(base).origin;
  if (!sameOrigin(base, approved)) throw new Error('TLS base must be HTTP(S) without embedded credentials');
  if (!config || typeof config !== 'object' || Array.isArray(config) ||
      config.insecure !== undefined && typeof config.insecure !== 'boolean') throw new Error('tls.insecure must be a boolean');
  const insecure = config.insecure === true;
  if (insecure && !approved.startsWith('https://')) throw new Error('tls.insecure requires an HTTPS base origin');
  const make = rejectUnauthorized => proxy
    ? new ProxyAgent({ uri: proxy, requestTls: { rejectUnauthorized }, proxyTls: { rejectUnauthorized: true } })
    : new Agent({ connect: { rejectUnauthorized } });
  const verified = make(true);
  const exception = insecure ? make(false) : null;
  return {
    approved, insecure,
    async fetch(raw, options = {}) {
      // Do not inherit ambient NODE_TLS_REJECT_UNAUTHORIZED or a caller's
      // global dispatcher. Each redirect/request is verified independently.
      const dispatcher = exception && sameOrigin(raw, approved) ? exception : verified;
      return transportFetch(raw, { ...options, dispatcher, redirect: 'manual' });
    },
    async close() { await Promise.all([verified.destroy(), exception?.destroy()]); },
  };
}

async function installBrowserTLS(page, policy) {
  if (!policy.insecure) return;
  const cdp = await page.target().createCDPSession();
  cdp.on('Security.certificateError', event => {
    const action = sameOrigin(event.requestURL, policy.approved) ? 'continue' : 'cancel';
    cdp.send('Security.handleCertificateError', { eventId: event.eventId, action }).catch(() => {});
  });
  await cdp.send('Security.enable');
  await cdp.send('Security.setOverrideCertificateErrors', { override: true });
}

module.exports = { createTargetTLS, installBrowserTLS };
