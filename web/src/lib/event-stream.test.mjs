import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

class FakeEventSource {
  static instances = [];
  static CLOSED = 2;
  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.closed = false;
    this.handlers = {};
    FakeEventSource.instances.push(this);
  }
  addEventListener(type, fn) {
    this.handlers[type] = fn;
  }
  close() {
    this.closed = true;
    this.readyState = 2;
  }
}

async function tick() {
  await new Promise((resolve) => setImmediate(resolve));
}

test("each connection attempt uses a fresh ticket and the current resume cursor", async (t) => {
  const hooks = registerHooks({
    resolve(specifier, context, next) {
      if (specifier === "@/lib/api")
        return {
          url: "data:text/javascript,let n=0;export async function streamUrl(p){n++;return p+(p.includes('?')?'&':'?')+'ticket=t'+n}",
          shortCircuit: true,
        };
      return next(specifier, context);
    },
  });
  const original = { EventSource: globalThis.EventSource, setTimeout: globalThis.setTimeout };
  t.after(() => {
    hooks.deregister();
    globalThis.EventSource = original.EventSource;
    globalThis.setTimeout = original.setTimeout;
  });
  globalThis.EventSource = FakeEventSource;
  // Run reconnect timers immediately; the delay values are not under test.
  globalThis.setTimeout = (fn) => original.setTimeout(fn, 0);
  const { TicketEventSource } = await import("./event-stream.ts");

  let cursor = 0;
  const stream = new TicketEventSource(() => `/api/stream?since=${cursor}`);
  const seen = [];
  stream.onmessage = (event) => seen.push(event.data);
  stream.addEventListener("snapshot", () => seen.push("snapshot"));
  await tick();
  const first = FakeEventSource.instances[0];
  assert.equal(first.url, "/api/stream?since=0&ticket=t1");
  first.onopen({});
  assert.equal(stream.readyState, TicketEventSource.OPEN);
  first.onmessage({ data: "a" });
  first.handlers.snapshot({});
  cursor = 7;

  // A dropped connection is closed and reopened with a new ticket and the new cursor.
  first.readyState = 0;
  first.onerror({});
  assert.equal(first.closed, true);
  await new Promise((resolve) => original.setTimeout(resolve, 10));
  const second = FakeEventSource.instances[1];
  assert.equal(second.url, "/api/stream?since=7&ticket=t2");
  second.handlers.snapshot({});
  assert.deepEqual(seen, ["a", "snapshot", "snapshot"]);

  // Closing from the error handler stops the retry.
  stream.onerror = () => stream.close();
  second.onerror({});
  await new Promise((resolve) => original.setTimeout(resolve, 10));
  assert.equal(FakeEventSource.instances.length, 2);
  assert.equal(stream.readyState, TicketEventSource.CLOSED);
});
