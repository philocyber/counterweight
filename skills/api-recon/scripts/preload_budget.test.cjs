const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const zlib = require('node:zlib');
const puppeteer = require('puppeteer-core');

test('actual Chromium preload bounds inspection and preserves native application responses', { timeout: 30000 }, async t => {
  const server = http.createServer((req, res) => {
    res.setHeader('content-type', req.url === '/' ? 'text/html' : 'application/json');
    if (req.url === '/') return res.end('<html><body>Local preload fixture</body></html>');
    if (req.url.startsWith('/api/stall')) return res.write('prefix');
    if (req.url === '/api/sse') {
      res.setHeader('content-type', 'text/event-stream');
      return res.write('data: fixture\n\n');
    }
    if (req.url === '/api/gzip') {
      res.setHeader('content-encoding', 'gzip');
      return res.end(zlib.gzipSync(JSON.stringify({ code: 401, message: 'unauthorized', data: 'x'.repeat(10000) })));
    }
    if (req.url === '/api/large') return res.end(JSON.stringify({ code: 401, message: 'unauthorized', data: 'x'.repeat(2048) }));
    return res.end('{"code":401,"message":"unauthorized","url":"/api/nested"}');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  const browser = await puppeteer.launch({ executablePath: process.env.RECON_CHROMIUM, headless: true, args: ['--no-sandbox'] });
  t.after(() => browser.close());
  const raw = fs.readFileSync(process.env.PRELOAD_UNDER_TEST || path.join(__dirname, 'preload.js'), 'utf8').replace(/\r\n/g, '\n');
  async function pageWithBudget(maxTotalBytes = 16384, maxResponses = 20) {
    const page = await browser.newPage();
    t.after(() => page.close().catch(() => {}));
    const original = 'responseBudget: { maxResponseBytes: 8 * 1024 * 1024, maxTotalBytes: 64 * 1024 * 1024,\n      maxResponses: 2048, maxConcurrent: 8, responseTimeoutMs: 20000 }';
    assert.ok(raw.includes(original), 'test budget configuration anchor changed');
    const source = raw.replace(original, `responseBudget: { maxResponseBytes: 256, maxTotalBytes: ${maxTotalBytes}, maxResponses: ${maxResponses}, maxConcurrent: 2, responseTimeoutMs: 100 }`);
    await page.evaluateOnNewDocument(source);
    await page.goto(`http://127.0.0.1:${server.address().port}/`);
    return page;
  }

  await t.test('normal JSON rewriting, nested discovery, large/gzip bodies and XHR', async () => {
    const page = await pageWithBudget();
    const result = await page.evaluate(async () => {
      const headers = { 'x-fixture': 'original' };
      const normal = await (await fetch('/api/normal', { headers })).json();
      headers['x-fixture'] = 'mutated';
      headers.expanded = 'x'.repeat(100000);
      const large = await (await fetch('/api/large')).json();
      const gzip = await (await fetch('/api/gzip')).json();
      const xhr = url => new Promise((resolve, reject) => {
        const x = new XMLHttpRequest(); x.open('GET', url);
        x.onreadystatechange = () => { if (x.readyState === 4) resolve(JSON.parse(x.responseText)); };
        x.onerror = reject; x.send();
      });
      const smallXHR = await xhr('/api/normal');
      const largeXHR = await xhr('/api/large');
      return { normal, large: { code: large.code, length: large.data.length }, gzip: { code: gzip.code, length: gzip.data.length },
        smallXHR, largeXHR: { code: largeXHR.code, length: largeXHR.data.length },
        details: window.__API_RECON_DETAIL__, log: [...window.__API_RECON_LOG__], budget: window.__API_RECON_BUDGET__ };
    });
    assert.equal(result.normal.code, 0);
    assert.equal(result.smallXHR.code, 0);
    const capturedHeaders = result.details.find(r => r.url === '/api/normal' && r.source === 'fetch').reqHeaders;
    assert.equal(capturedHeaders['x-fixture'], 'original');
    assert.equal(capturedHeaders.expanded, undefined);
    assert.deepEqual(result.large, { code: 401, length: 2048 });
    assert.deepEqual(result.gzip, { code: 401, length: 10000 });
    assert.deepEqual(result.largeXHR, { code: 401, length: 2048 });
    assert.ok(result.log.includes('GET /api/nested'));
    assert.equal(result.details.filter(r => r.captureError === 'inspection byte budget exceeded').length, 3);
    assert.equal(result.budget.active, 0);
    assert.ok(result.budget.bytes <= result.budget.maxTotalBytes);
    assert.ok(result.budget.retainedBytes <= result.budget.maxTotalBytes);
  });

  await t.test('stalled clone cancellation cannot deadlock the original; SSE stays streaming', async () => {
    const page = await pageWithBudget();
    const result = await page.evaluate(async () => {
      const start = performance.now();
      const stalled = await fetch('/api/stall');
      const elapsed = performance.now() - start;
      const reader = stalled.body.getReader();
      const prefix = new TextDecoder().decode((await reader.read()).value);
      await reader.cancel();
      const sse = await fetch('/api/sse');
      const stream = sse.body.getReader();
      const event = new TextDecoder().decode((await stream.read()).value);
      await stream.cancel();
      return { elapsed, prefix, event, details: window.__API_RECON_DETAIL__, budget: window.__API_RECON_BUDGET__ };
    });
    assert.ok(result.elapsed >= 80 && result.elapsed < 2000, `stalled inspection did not honor its deadline: ${result.elapsed}`);
    assert.equal(result.prefix, 'prefix');
    assert.equal(result.event, 'data: fixture\n\n');
    assert.ok(result.details.some(r => /inspection deadline exceeded/.test(r.captureError)));
    assert.ok(result.details.some(r => r.captureError === 'streaming response not inspected'));
    assert.equal(result.budget.active, 0);
  });

  await t.test('aggregate bytes and retained inventory limits keep later requests usable', async () => {
    const page = await pageWithBudget(512, 4);
    const result = await page.evaluate(async () => {
      const codes = [];
      for (let i = 0; i < 12; i++) codes.push((await (await fetch('/api/normal?i=' + i)).json()).code);
      return { codes, budget: window.__API_RECON_BUDGET__, details: window.__API_RECON_DETAIL__.length, inventory: window.__API_RECON_LOG__.size };
    });
    assert.deepEqual(result.codes.slice(0, 4), [0, 0, 0, 0]);
    assert.ok(result.codes.slice(4).every(code => code === 401));
    assert.equal(result.budget.responses, 4);
    assert.ok(result.budget.omitted > 0);
    assert.ok(result.budget.bytes <= 512 && result.budget.retainedBytes <= 512);
    assert.ok(result.details <= 4 && result.inventory <= 4);
  });

  await t.test('concurrent stalled inspections respect the clone-slot bound', async () => {
    const page = await pageWithBudget();
    const result = await page.evaluate(async () => {
      await Promise.all([0, 1, 2].map(async i => {
        const response = await fetch('/api/stall?i=' + i);
        const reader = response.body.getReader();
        const prefix = new TextDecoder().decode((await reader.read()).value);
        if (prefix !== 'prefix') throw new Error('native response lost');
        await reader.cancel();
      }));
      return { budget: window.__API_RECON_BUDGET__, details: window.__API_RECON_DETAIL__ };
    });
    assert.equal(result.budget.responses, 2);
    assert.equal(result.budget.active, 0);
    assert.equal(result.details.filter(r => r.captureError === 'inspection budget unavailable').length, 1);
  });

  await t.test('aggregate byte exhaustion stops inspection without changing later responses', async () => {
    const page = await pageWithBudget(80, 20);
    const result = await page.evaluate(async () => {
      const codes = [];
      for (let i = 0; i < 6; i++) codes.push((await (await fetch('/api/normal')).json()).code);
      return { codes, budget: window.__API_RECON_BUDGET__ };
    });
    assert.equal(result.codes[0], 0);
    assert.ok(result.codes.slice(1).every(code => code === 401));
    assert.equal(result.budget.bytes, 80);
    assert.equal(result.budget.responses, 2);
    assert.equal(result.budget.active, 0);
    assert.ok(result.budget.retainedBytes <= 80);
  });
});
