const test = require('node:test');
const assert = require('node:assert/strict');
const https = require('node:https');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const fs = require('node:fs');
const path = require('node:path');
const { createTargetTLS, installBrowserTLS } = require('./tls_policy');
const run = promisify(execFile);

const key = fs.readFileSync(path.join(__dirname, 'test_fixtures/tls/key.pem'));
const cert = fs.readFileSync(path.join(__dirname, 'test_fixtures/tls/cert.pem'));

async function fixture(t) {
  const calls = [];
  const server = https.createServer({ key, cert }, (req, res) => {
    calls.push({ url: req.url, cookie: req.headers.cookie });
    res.setHeader('Content-Type', 'text/html');
    res.end('<html><body>synthetic target</body></html>');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return { url: `https://127.0.0.1:${server.address().port}`, calls };
}

test('Node TLS defaults reject untrusted credentials despite ambient bypass', async t => {
  const target = await fixture(t);
  const old = process.env.NODE_TLS_REJECT_UNAUTHORIZED;
  process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
  t.after(() => { if (old === undefined) delete process.env.NODE_TLS_REJECT_UNAUTHORIZED; else process.env.NODE_TLS_REJECT_UNAUTHORIZED = old; });
  const tls = createTargetTLS(target.url);
  t.after(() => tls.close());
  await assert.rejects(tls.fetch(target.url, { headers: { cookie: 'fixture=secret' } }), /fetch failed/);
  assert.deepEqual(target.calls, []);
});

test('Node exception is exact-origin and responses remain streamed', async t => {
  const approved = await fixture(t), foreign = await fixture(t);
  const tls = createTargetTLS(approved.url, { insecure: true });
  t.after(() => tls.close());
  const response = await tls.fetch(approved.url, { headers: { cookie: 'fixture=secret' } });
  assert.equal(response.status, 200);
  assert.equal(typeof response.body.getReader, 'function');
  assert.match(await response.text(), /synthetic target/);
  assert.equal(approved.calls[0].cookie, 'fixture=secret');
  await assert.rejects(tls.fetch(foreign.url), /fetch failed/);
  await assert.rejects(tls.fetch(approved.url.replace('127.0.0.1', 'localhost')), /fetch failed/);
  assert.deepEqual(foreign.calls, []);
  assert.equal(approved.calls.length, 1);
});

test('exception configuration rejects ambiguous values and non-HTTPS origins', () => {
  for (const value of ['true', 1, null, []]) assert.throws(() => createTargetTLS('https://fixture.test', { insecure: value }));
  assert.throws(() => createTargetTLS('http://fixture.test', { insecure: true }));
  assert.throws(() => createTargetTLS('https://user:password@fixture.test', { insecure: true }));
});

test('Chromium certificate exception stays at one origin with a shared certificate', { timeout: 20000 }, async t => {
  assert.ok(process.env.RECON_CHROMIUM, 'RECON_CHROMIUM is required for actual browser verification');
  const puppeteer = require('puppeteer-core');
  const approved = await fixture(t), foreign = await fixture(t);
  const browser = await puppeteer.launch({ executablePath: process.env.RECON_CHROMIUM, headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  t.after(() => browser.close());
  const securePage = await browser.newPage();
  await assert.rejects(securePage.goto(approved.url), /ERR_CERT/);
  assert.deepEqual(approved.calls, []);
  const page = await browser.newPage();
  const policy = createTargetTLS(approved.url, { insecure: true });
  t.after(() => policy.close());
  await installBrowserTLS(page, policy);
  await page.goto(approved.url);
  assert.match(await page.content(), /synthetic target/);
  assert.equal(await page.evaluate(async url => {
    try { await fetch(url, { mode: 'no-cors' }); return 'delivered'; }
    catch { return 'rejected'; }
  }, foreign.url), 'rejected');
  assert.deepEqual(foreign.calls, []);
  await assert.rejects(page.goto(foreign.url), /ERR_CERT|ERR_ABORTED|ERR_FAILED/);
  assert.deepEqual(foreign.calls, []);
});

test('HTTP CONNECT forwarding retains target TLS policy and verifies HTTPS proxies', async t => {
  const target = await fixture(t), foreign = await fixture(t);
  const proxy = http.createServer();
  const sockets = new Set();
  const tunnels = [];
  proxy.on('connect', (req, client, head) => {
    tunnels.push(req.url);
    const url = new URL(`https://${req.url}`);
    assert.equal(url.hostname, '127.0.0.1');
    const upstream = net.connect(Number(url.port), '127.0.0.1', () => {
      client.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      if (head.length) upstream.write(head);
      client.pipe(upstream).pipe(client);
    });
    for (const socket of [client, upstream]) {
      sockets.add(socket);
      socket.on('close', () => sockets.delete(socket));
      socket.on('error', () => { client.destroy(); upstream.destroy(); });
    }
  });
  await new Promise(resolve => proxy.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { for (const socket of sockets) socket.destroy(); proxy.close(resolve); }));
  const proxyURL = `http://127.0.0.1:${proxy.address().port}`;
  const secure = createTargetTLS(target.url, {}, proxyURL);
  const exception = createTargetTLS(target.url, { insecure: true }, proxyURL);
  const untrustedProxy = createTargetTLS(target.url, { insecure: true }, foreign.url);
  t.after(async () => { await secure.close(); await exception.close(); await untrustedProxy.close(); });
  await assert.rejects(secure.fetch(target.url), /fetch failed/);
  assert.deepEqual(target.calls, []);
  assert.match(await (await exception.fetch(target.url)).text(), /synthetic target/);
  await assert.rejects(exception.fetch(foreign.url), /fetch failed/);
  await assert.rejects(untrustedProxy.fetch(target.url), /fetch failed/);
  assert.deepEqual(foreign.calls, []);
  assert.equal(target.calls.length, 1);
  assert.equal(tunnels.length, 3);
});

test('actual runtime keeps verified defaults and explicitly authorized TLS/API rewriting', { timeout: 25000 }, async t => {
  assert.ok(process.env.RECON_CHROMIUM);
  const calls = [];
  const server = https.createServer({ key, cert }, (req, res) => {
    calls.push({ url: req.url, cookie: req.headers.cookie });
    if (req.url === '/api/fixture') {
      res.setHeader('Content-Type', 'application/json');
      res.end('{"code":401,"message":"unauthorized","data":{"fixture":true}}');
    } else if (req.url === '/api/observed') {
      let body = '';
      req.on('data', part => { body += part; });
      req.on('end', () => { calls.push({ observed: JSON.parse(body) }); res.end('{}'); });
    } else {
      res.setHeader('Content-Type', 'text/html');
      res.end(`<script>fetch('/api/fixture').then(r=>r.json()).then(data=>fetch('/api/observed',{
        method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)}))</script>`);
    }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  const base = `https://127.0.0.1:${server.address().port}`;
  const output = fs.mkdtempSync(path.join(os.tmpdir(), 'counterweight-tls-runtime-'));
  t.after(() => fs.rmSync(output, { recursive: true, force: true }));
  const config = path.join(output, 'config.json');
  for (const insecure of [false, true]) {
    fs.writeFileSync(config, JSON.stringify({ baseUrl: base, chromium: process.env.RECON_CHROMIUM, tls: { insecure },
      cookies: [{ name: 'session', value: 'fixture', path: '/' }], proxy: '', routes: [], recordWs: false, waitMs: 400 }));
    await run(process.execPath, [process.env.RECON_RUNTIME_TLS_UNDER_TEST || path.join(__dirname, 'runtime_harvest.js'), config], { timeout: 12000,
      env: { ...process.env, HTTP_PROXY: '', HTTPS_PROXY: '' } });
    if (!insecure) assert.deepEqual(calls, [], 'default TLS sent an HTTP request before validation');
  }
  assert.equal(calls.find(c => c.url === '/api/fixture')?.cookie, 'session=fixture');
  assert.equal(calls.find(c => c.observed)?.observed.code, 0);
  assert.equal(calls.find(c => c.observed)?.observed.data.fixture, true);
  const report = JSON.parse(fs.readFileSync(path.join(output, 'runtime_api.json')));
  assert.match(report.rec.find(r => r.u === '/api/fixture')?.resp, /unauthorized/);
});
