import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

const HOSTILE = "../intercept/tool-config?x=1#y";

async function loadApi(t) {
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
  const mod = await import("./api.ts");
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
  const requests = [];
  globalThis.document = { cookie: "" };
  globalThis.localStorage = { getItem: () => "synthetic.fixture.token", removeItem() {} };
  globalThis.window = {
    location: { href: "http://127.0.0.1:9000/", protocol: "http:", hostname: "127.0.0.1" },
    localStorage: globalThis.localStorage,
  };
  globalThis.fetch = async (url, init) => {
    // The localised fetch adds a language query parameter; the tests look at the request as built.
    requests.push({
      url: String(url)
        .replace(/([?&])lang=en(?=&|$|#)/, "$1")
        .replace(/[?&]$/, "")
        .replace("?&", "?"),
      method: init?.method ?? "GET",
      body: init?.body,
    });
    return new Response('{"ticket":"tk"}', { status: 200 });
  };
  return { ...mod, requests };
}

test("no API helper lets an identifier change the request path", async (t) => {
  const { api, requests } = await loadApi(t);
  const names = Object.keys(api).filter((name) => typeof api[name] === "function");
  assert.ok(names.length > 100, "the whole API object is exercised");
  for (const name of names) {
    // Every argument is hostile; a helper that needs an object or a number fails or ignores it.
    try {
      await Promise.race([
        Promise.resolve(api[name](HOSTILE, HOSTILE, HOSTILE, HOSTILE, HOSTILE)).catch(() => {}),
        new Promise((resolve) => setTimeout(resolve, 200)),
      ]);
    } catch {
      // Helpers that touch browser-only objects may throw before sending anything.
    }
  }
  assert.ok(requests.length > 100, `requests were made (${requests.length})`);
  for (const { url } of requests) {
    assert.ok(url.startsWith("/api/"), url);
    const path = url.split("?")[0];
    assert.ok(!url.includes("../"), url);
    assert.ok(!url.includes("#"), url);
    assert.ok(!/\/\.\.?(\/|$)/.test(path), url);
  }
});

test("agent helpers keep legitimate keys and neutralise a crafted one", async (t) => {
  const { api, requests } = await loadApi(t);
  await api.getAgent("planner");
  await api.saveAgentPrompt("custom_x", "tpl", "n");
  await api.getAgent("../intercept/tool-config?");
  await api.getAgent("..");
  assert.equal(requests[0].url, "/api/agents/planner");
  assert.equal(requests[1].url, "/api/agents/custom_x/prompt");
  assert.equal(requests[2].url, "/api/agents/..%2Fintercept%2Ftool-config%3F");
  assert.equal(requests[3].url, "/api/agents/%252E%252E");
});

test("skill file paths keep their directory separators", async (t) => {
  const { api, requests } = await loadApi(t);
  await api.readSkillFile("my-skill", "docs/a b.md");
  await api.readSkillFile("my-skill", "../../x");
  assert.equal(requests[0].url, "/api/skills/my-skill/files/docs/a%20b.md");
  assert.equal(requests[1].url, "/api/skills/my-skill/files/%252E%252E/%252E%252E/x");
});

test("stream URLs stay on the page origin and the ticket is requested for that exact path", async (t) => {
  const { streamUrl, requests } = await loadApi(t);
  const url = await streamUrl("/api/exploration/activity/stream?task=5&since=0");
  assert.ok(url.startsWith("/api/exploration/activity/stream?task=5&since=0"), url);
  assert.ok(url.endsWith("&ticket=tk"), url);
  assert.equal(requests[0].url, "/api/auth/stream-ticket");
  assert.equal(requests[0].method, "POST");
  assert.deepEqual(JSON.parse(requests[0].body), { path: "/api/exploration/activity/stream?task=5&since=0" });
});

test("an explicitly configured HTTPS stream origin is still honoured", async (t) => {
  const { sseUrl } = await loadApi(t);
  process.env.NEXT_PUBLIC_SSE_BASE = "https://stream.example.test";
  assert.ok(sseUrl("/api/events").startsWith("https://stream.example.test/api/events"));
});
