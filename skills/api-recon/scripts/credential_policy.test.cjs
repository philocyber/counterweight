const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const { sameOrigin, scopedHeaders, forwardApproved } = require('./credential_policy');
const run = promisify(execFile);

async function listener(t, handler) {
  const server = http.createServer(handler);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return `http://127.0.0.1:${server.address().port}`;
}

test('origin identity rejects prefix, port, scheme and user-info changes', () => {
  const approved = 'https://example.test';
  for (const url of ['https://EXAMPLE.test:443/a', 'https://example.test/b']) assert.ok(sameOrigin(url, approved));
  for (const url of ['https://example.test.evil/a', 'https://example.test:8443/a', 'http://example.test/a', 'https://example.test@evil.test/a', 'https://user@example.test/a', 'file:///a']) assert.equal(sameOrigin(url, approved), false);
  assert.deepEqual(scopedHeaders({ Cookie: 'secret', Authorization: 'secret', 'Proxy-Authorization': 'secret', Accept: 'text/plain' }, 'https://example.test.evil/api', approved, []), { Accept: 'text/plain', cookie: '' });
});

test('forwarding follows only approved redirects and respects cookie paths', async t => {
  const foreignCalls = [];
  const foreign = await listener(t, (req, res) => { foreignCalls.push(req.headers); res.end('foreign'); });
  const calls = [];
  const approved = await listener(t, (req, res) => {
    calls.push({ path: req.url, cookie: req.headers.cookie, method: req.method });
    if (req.url === '/start') { res.writeHead(302, { location: '/private/final' }); res.end(); }
    else if (req.url === '/outside') { res.writeHead(307, { location: foreign + '/api/sink' }); res.end(); }
    else res.end('approved');
  });
  const cookies = [{ name: 'fixture_cookie', value: 'synthetic', path: '/private' }];
  const final = await forwardApproved(approved + '/start', { method: 'POST', headers: {}, body: 'fixture' }, approved, cookies);
  assert.equal(await final.response.text(), 'approved');
  assert.deepEqual(calls.slice(0, 2), [{ path: '/start', cookie: '', method: 'POST' }, { path: '/private/final', cookie: 'fixture_cookie=synthetic', method: 'GET' }]);
  const redirected = await forwardApproved(approved + '/outside', { method: 'GET', headers: {} }, approved, cookies);
  assert.equal(redirected.redirectURL, foreign + '/api/sink');
  assert.deepEqual(foreignCalls, []);
});

test('actual runtime harvester confines credentials and render state in Chromium', { timeout: 30000 }, async t => {
  const approvedCalls = [], foreignCalls = [];
  const foreign = await listener(t, (req, res) => {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', () => {
      foreignCalls.push({ path: req.url, headers: req.headers, body });
      // The redirect destination intentionally withholds CORS permission.
      // Node must not launder this body into an approved-origin response.
      if (req.url !== '/api/redirect-destination') {
        res.setHeader('Access-Control-Allow-Origin', req.headers.origin || '*');
        res.setHeader('Access-Control-Allow-Credentials', 'true');
        res.setHeader('Access-Control-Allow-Headers', 'authorization,content-type');
      }
      if (req.url === '/landing') {
        res.setHeader('Content-Type', 'text/html');
        res.end(`<script>fetch('/api/document-state', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({cookie:document.cookie,token:localStorage.getItem('fixture_token')})})</script>`);
      } else res.end('{"code":401,"fixture":"foreign"}');
    });
  });
  const approved = await listener(t, (req, res) => {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', () => {
      approvedCalls.push({ path: req.url, headers: req.headers, body });
      if (req.url === '/app') {
        res.setHeader('Content-Type', 'text/html');
        res.setHeader('Set-Cookie', 'fixture_cookie=native-cookie-fixture; Path=/');
        res.end(`<script>(async()=>{let r=await fetch('/api/in-scope');await fetch('/api/render-state',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({cookie:document.cookie,token:localStorage.getItem('fixture_token'),code:(await r.json()).code})});fetch('${foreign}/api/foreign',{credentials:'include',headers:{Authorization:'Bearer synthetic-browser-token'}}).catch(()=>{});fetch('/api/cross-redirect').then(r=>r.text()).then(body=>fetch('/api/private-observation',{method:'POST',body})).catch(()=>fetch('/api/private-observation',{method:'POST',body:'browser-denied'}));})()</script>`);
      } else if (req.url === '/foreign-navigation') {
        res.writeHead(302, { location: foreign + '/landing' }); res.end();
      } else if (req.url === '/api/cross-redirect') {
        res.writeHead(302, { location: foreign + '/api/redirect-destination' }); res.end();
      } else {
        res.setHeader('Content-Type', 'application/json'); res.end('{"code":401,"data":{}}');
      }
    });
  });
  const output = fs.mkdtempSync(path.join(os.tmpdir(), 'counterweight-recon-origin-'));
  t.after(() => fs.rmSync(output, { recursive: true, force: true }));
  const config = path.join(output, 'config.json');
  fs.writeFileSync(config, JSON.stringify({ baseUrl: approved + '/app', chromium: process.env.RECON_CHROMIUM || 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe', cookies: [{ name: 'fixture_cookie', value: 'synthetic-cookie-secret' }], localStorage: { fixture_token: 'synthetic-storage-secret' }, routes: ['/foreign-navigation'], waitMs: 600, perRouteMs: 500, recordWs: false, proxy: '' }));
  const script = process.env.RUNTIME_UNDER_TEST || path.join(__dirname, 'runtime_harvest.js');
  await run(process.execPath, [script, config], { timeout: 25000, env: { ...process.env, HTTP_PROXY: '', HTTPS_PROXY: '' } });
  const render = approvedCalls.find(call => call.path === '/api/render-state');
  assert.ok(foreignCalls.every(call => !call.headers.cookie && !call.headers.authorization), `foreign origin received configured credentials: ${JSON.stringify(foreignCalls.map(c => ({path:c.path,cookie:c.headers.cookie,auth:c.headers.authorization,body:c.body})))}; redirect observation: ${JSON.stringify(approvedCalls.filter(c=>c.path==='/api/private-observation').map(c=>c.body))}`);
  assert.ok(render, 'approved SPA render probe did not run');
  assert.deepEqual(JSON.parse(render.body), { cookie: 'fixture_cookie=synthetic-cookie-secret', token: 'synthetic-storage-secret', code: 0 });
  assert.ok(approvedCalls.every(call => call.headers.cookie === 'fixture_cookie=synthetic-cookie-secret'), 'approved homepage/API credentials were lost');
  assert.ok(foreignCalls.some(call => call.path === '/api/foreign'), 'native third-party API request stopped working');
  assert.ok(foreignCalls.some(call => call.path === '/api/redirect-destination'), 'cross-origin redirect stopped working');
  const document = foreignCalls.find(call => call.path === '/api/document-state');
  assert.ok(document, 'foreign navigation did not complete');
  assert.deepEqual(JSON.parse(document.body), { cookie: '', token: null });
  const observation = approvedCalls.find(call => call.path === '/api/private-observation');
  assert.ok(observation, 'cross-origin CORS observation did not run');
  assert.equal(observation.body, 'browser-denied', 'host-side redirect laundered a foreign body into an approved-origin response');
  const report = JSON.parse(fs.readFileSync(path.join(output, 'runtime_api.json'), 'utf8'));
  assert.ok(report.rec.some(entry => entry.full.startsWith(approved) && entry.resp), 'approved response capture stopped working');
  assert.ok(report.rec.filter(entry => entry.full.startsWith(foreign)).every(entry => !entry.resp), 'host-side forwarding captured a foreign response');
});
