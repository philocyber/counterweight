const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const zlib = require('node:zlib');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { promisify } = require('node:util');
const run = promisify(require('node:child_process').execFile);
const { createResponseBudget, readResponseText } = require('./response_budget');

test('decoded bodies use exact byte boundaries and report truncated prefixes', async () => {
  for (const size of [63, 64, 65, 100000]) {
    const b = createResponseBudget({ maxResponseBytes: 64, maxTotalBytes: 1024 });
    const r = await readResponseText(new Response('x'.repeat(size)), b);
    assert.equal(r.text.length, Math.min(size, 64));
    assert.equal(r.truncated, size > 64);
    assert.equal(b.bytes, Math.min(size, 64));
  }
});

test('concurrent reads share byte, count and active-slot limits without a queue', async () => {
  const b = createResponseBudget({ maxResponseBytes: 64, maxTotalBytes: 96, maxConcurrent: 2, maxResponses: 2 });
  const a = b.acquire(), c = b.acquire();
  assert.ok(a && c);
  assert.equal(b.acquire(), null);
  const results = await Promise.all([readResponseText(new Response('x'.repeat(64)), b), readResponseText(new Response('y'.repeat(64)), b)]);
  a(); c(); c();
  assert.equal(b.active, 0);
  assert.equal(results.reduce((n, r) => n + Buffer.byteLength(r.text), 0), 96);
  assert.ok(results.some(r => r.truncated));
  assert.equal(b.acquire(), null);
  for (const value of [0, -1, Infinity, '64']) assert.throws(() => createResponseBudget({ maxTotalBytes: value }));
});

test('a never-ending response times out and cancels its reader', async () => {
  let cancelled = false;
  const stream = new ReadableStream({ cancel() { cancelled = true; } });
  await assert.rejects(readResponseText(new Response(stream), createResponseBudget({ responseTimeoutMs: 30 })), /deadline/);
  assert.equal(cancelled, true);
});

async function listener(t, handler) {
  const server = http.createServer(handler);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return `http://127.0.0.1:${server.address().port}`;
}

test('compressed HTTP responses are capped after decompression', async t => {
  const url = await listener(t, (_, res) => {
    res.setHeader('content-encoding', 'gzip');
    res.end(zlib.gzipSync('x'.repeat(100000)));
  });
  const capture = await readResponseText(await fetch(url), createResponseBudget({ maxResponseBytes: 256 }));
  assert.equal(capture.text.length, 256);
  assert.equal(capture.truncated, true);
});

test('actual runtime harvester bounds large POST responses without replay and keeps normal rewriting', { timeout: 30000 }, async t => {
  const calls = [];
  const url = await listener(t, (req, res) => {
    calls.push({ path: req.url, method: req.method });
    if (req.url === '/app') {
      res.setHeader('content-type', 'text/html');
      res.end(`<script>(async()=>{const big=await fetch('/api/big',{method:'POST',body:'once'});const stall=await fetch('/api/stall');const small=await fetch('/api/small');await fetch('/api/observed',{method:'POST',body:JSON.stringify({large:big.status,stall:stall.status,small:(await small.json()).code})});})()</script>`);
    } else if (req.url === '/api/big') {
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ code: 401, data: 'x'.repeat(5000) }));
    } else if (req.url === '/api/stall') {
      res.write('{"code":'); // Deliberately never finish this body.
    } else if (req.url === '/api/observed') {
      let body = '';
      req.on('data', chunk => { body += chunk; });
      req.on('end', () => { calls.push({ observed: JSON.parse(body) }); res.end('{}'); });
    } else res.end('{"code":401,"data":{}}');
  });
  const output = fs.mkdtempSync(path.join(os.tmpdir(), 'counterweight-response-budget-'));
  t.after(() => fs.rmSync(output, { recursive: true, force: true }));
  const config = path.join(output, 'config.json');
  fs.writeFileSync(config, JSON.stringify({ baseUrl: url + '/app', chromium: process.env.RECON_CHROMIUM, routes: [], waitMs: 600, recordWs: false, proxy: '', responseBudget: { maxResponseBytes: 256, maxTotalBytes: 4096, responseTimeoutMs: 150 } }));
  await run(process.execPath, [path.join(__dirname, 'runtime_harvest.js'), config], { timeout: 25000, env: { ...process.env, HTTP_PROXY: '', HTTPS_PROXY: '' } });
  assert.equal(calls.filter(c => c.path === '/api/big').length, 1, 'POST was replayed after exceeding response budget');
  assert.deepEqual(calls.find(c => c.observed)?.observed, { large: 502, stall: 502, small: 0 });
  const report = JSON.parse(fs.readFileSync(path.join(output, 'runtime_api.json')));
  assert.equal(report.rec.find(r => r.u === '/api/big').captureError, 'response byte budget exceeded');
  assert.equal(report.rec.find(r => r.u === '/api/big').resp.length, 256);
  assert.equal(report.rec.find(r => r.u === '/api/stall').captureError, 'response deadline exceeded');
  assert.equal(report.responseBudget.omitted, 2);
  assert.ok(report.responseBudget.bytes <= 4096);
});
