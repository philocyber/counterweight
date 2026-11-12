import { managementTransportAllowed } from "./management-transport.ts";
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

test("management origin policy permits HTTPS and literal loopback, without DNS or prefix trust", () => {
  for (const url of [
    "https://console.example.test",
    "https://192.168.1.2:8787",
    "http://localhost:5173",
    "http://127.0.0.1:8787",
    "http://[::1]:8787",
  ]) {
    assert.equal(managementTransportAllowed(url), true, url);
  }
  for (const url of [
    "http://console.example.test",
    "http://192.168.1.2:8787",
    "http://localhost.attacker.test",
    "http://127.0.0.1.attacker.test",
    "http://user@localhost",
    "https://user:password@example.test",
    "file:///app",
    "invalid",
  ]) {
    assert.equal(managementTransportAllowed(url), false, url);
  }
  assert.equal(managementTransportAllowed("/api/stream", "https://console.example.test"), true);
  assert.equal(managementTransportAllowed("/api/stream", "http://console.example.test"), false);
});

test("actual API client rejects remote plaintext before reading credentials or making a request", async (t) => {
  // Load the real API/i18n modules; only the backend-free mock handler is stubbed.
  const hooks = registerHooks({
    resolve(specifier, context, next) {
      if (specifier === "@/lib/mock/handler")
        return {
          url: "data:text/javascript,export function mockHandle(){throw new Error('mock must not run')}",
          shortCircuit: true,
        };
      if (specifier.startsWith("@/"))
        return { url: new URL("../" + specifier.slice(2) + ".ts", import.meta.url).href, shortCircuit: true };
      return next(specifier, context);
    },
  });
  t.after(() => hooks.deregister());
  process.env.NEXT_PUBLIC_MOCK = "0";
  const { http, sseUrl } = await import("./api.ts");
  const original = {
    window: globalThis.window,
    document: globalThis.document,
    storage: globalThis.localStorage,
    fetch: globalThis.fetch,
  };
  t.after(() => {
    globalThis.window = original.window;
    globalThis.document = original.document;
    globalThis.localStorage = original.storage;
    globalThis.fetch = original.fetch;
    delete process.env.NEXT_PUBLIC_SSE_BASE;
  });
  let calls = 0,
    credentialReads = 0;
  globalThis.document = { cookie: "" };
  globalThis.localStorage = {
    getItem() {
      credentialReads++;
      return "synthetic.fixture.token";
    },
  };
  globalThis.fetch = async (_url, init) => {
    calls++;
    assert.equal(new Headers(init.headers).get("authorization"), "Bearer synthetic.fixture.token");
    return new Response("{}", { status: 200 });
  };
  function origin(raw) {
    const u = new URL(raw);
    globalThis.window = {
      location: { href: u.href, protocol: u.protocol, hostname: u.hostname },
      localStorage: globalThis.localStorage,
    };
  }
  origin("http://remote.example.test");
  for (const path of ["/auth/init", "/auth/login", "/auth/change-password", "/settings"]) {
    await assert.rejects(http(path, { method: "POST", body: '{"password":"synthetic"}' }), /requires HTTPS/);
  }
  assert.throws(() => sseUrl("/api/events"), /requires HTTPS/);
  assert.equal(calls, 0);
  assert.equal(credentialReads, 0);

  for (const url of ["http://127.0.0.1:5173", "http://localhost:5173", "https://remote.example.test"]) {
    origin(url);
    await http("/auth/login", { method: "POST", body: "{}" });
    assert.equal(sseUrl("/api/events").includes("synthetic.fixture.token"), false);
  }
  assert.equal(calls, 3);
  origin("https://remote.example.test");
  process.env.NEXT_PUBLIC_SSE_BASE = "http://remote.example.test:8787";
  assert.throws(() => sseUrl("/api/events"), /requires HTTPS/);
  assert.equal(calls, 3);
  process.env.NEXT_PUBLIC_SSE_BASE = "https://remote.example.test";
  assert.ok(sseUrl("/api/events").startsWith("https://remote.example.test/api/events?"));
});
