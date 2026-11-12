import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import http from "node:http";
import { createRequire, stripTypeScriptTypes } from "node:module";
import test from "node:test";
import { fileURLToPath } from "node:url";

test("browser keeps JWT origin-local while other ports receive no credential cookie", { timeout: 20000 }, async (t) => {
  const require = createRequire(import.meta.url);
  let puppeteer;
  try {
    puppeteer = require("puppeteer-core");
  } catch {
    t.skip("requires isolated Puppeteer test dependencies");
    return;
  }
  if (!process.env.RECON_CHROMIUM) {
    t.skip("requires explicit test Chromium path");
    return;
  }
  const token = "browser.fixture.jwt";
  const sourcePath = process.env.AUTH_UNDER_TEST || fileURLToPath(new URL("./auth.ts", import.meta.url));
  const source = stripTypeScriptTypes(await readFile(sourcePath, "utf8"));
  const requests = [];
  const other = http.createServer((req, res) => {
    requests.push({ url: req.url, cookie: req.headers.cookie || "" });
    res.setHeader("Content-Type", "text/html");
    res.end("<!doctype html><title>Other port</title>");
  });
  const app = http.createServer((req, res) => {
    res.setHeader("Content-Type", req.url === "/auth.js" ? "text/javascript" : "text/html");
    res.end(req.url === "/auth.js" ? source : "<!doctype html><title>Auth fixture</title>");
  });
  await Promise.all([
    new Promise((resolve) => app.listen(0, "127.0.0.1", resolve)),
    new Promise((resolve) => other.listen(0, "127.0.0.1", resolve)),
  ]);
  t.after(() => {
    app.closeAllConnections();
    other.closeAllConnections();
    app.close();
    other.close();
  });
  const base = `http://127.0.0.1:${app.address().port}`;
  const foreign = `http://127.0.0.1:${other.address().port}`;
  const browser = await puppeteer.launch({ executablePath: process.env.RECON_CHROMIUM, headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  await page.goto(base);
  await page.evaluate(async (token) => {
    globalThis.process = { env: { NEXT_PUBLIC_MOCK: "0" } };
    globalThis.fixtureAuth = (await import("/auth.js")).auth;
    fixtureAuth.setToken(token);
  }, token);
  const stored = await page.evaluate(() => ({ token: fixtureAuth.getToken(), cookies: document.cookie }));
  assert.equal(stored.token, token);
  await page.evaluate(async (url) => {
    await fetch(url, { credentials: "include", mode: "no-cors" });
  }, foreign + "/resource");
  const received = requests.find((req) => req.url === "/resource");
  assert.ok(received, "same-host foreign-port request must actually reach the listener");
  assert.equal(received.cookie.includes(token), false, "foreign port received the administrator JWT cookie");
  assert.equal(received.cookie.includes("counterweight_token="), false);
  assert.equal(stored.cookies.includes(token), false);
  assert.match(stored.cookies, /counterweight_session=1/);
  // Legacy cookies are cleared without losing the explicit session token.
  await page.evaluate((token) => {
    document.cookie = `counterweight_token=${token}; path=/`;
    fixtureAuth.getToken();
  }, token);
  assert.equal(
    (await page.cookies()).some((cookie) => cookie.name === "counterweight_token"),
    false,
  );
  await page.goto(foreign);
  assert.equal(await page.evaluate(() => localStorage.getItem("counterweight_token")), null);
  await page.goto(base);
  assert.equal(await page.evaluate(() => localStorage.getItem("counterweight_token")), token);
});
