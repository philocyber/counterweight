import { auth } from "./auth.ts";
import assert from "node:assert/strict";
import test from "node:test";

function browser() {
  const storage = new Map();
  const cookies = new Map();
  const writes = [];
  globalThis.window = {};
  globalThis.localStorage = {
    getItem: (key) => storage.get(key) ?? null,
    setItem: (key, value) => storage.set(key, value),
    removeItem: (key) => storage.delete(key),
  };
  globalThis.document = {
    get cookie() {
      return [...cookies].map(([key, value]) => `${key}=${value}`).join("; ");
    },
    set cookie(value) {
      writes.push(value);
      const [name, entry] = value.split(";")[0].split("=");
      if (value.includes("max-age=0")) cookies.delete(name);
      else cookies.set(name, entry);
    },
  };
  return { storage, cookies, writes };
}

test("login and password refresh never put credentials in a cookie", () => {
  const state = browser();
  for (const token of ["initial.secret.jwt", "replacement.secret.jwt"]) {
    auth.setToken(token);
    assert.equal(auth.getToken(), token);
    assert.equal(state.storage.get("counterweight_token"), token);
    assert.equal(state.cookies.get("counterweight_session"), "1");
    assert.equal(state.cookies.has("counterweight_token"), false);
    assert.ok(state.writes.every((write) => !write.includes(token)));
  }
  auth.clearToken();
  assert.equal(state.storage.has("counterweight_token"), false);
  assert.equal(state.cookies.has("counterweight_session"), false);
});

test("existing localStorage sessions survive legacy cookie removal", () => {
  const state = browser();
  state.storage.set("counterweight_token", "existing.secret.jwt");
  state.cookies.set("counterweight_token", "existing.secret.jwt");
  assert.equal(auth.getToken(), "existing.secret.jwt");
  assert.equal(state.cookies.has("counterweight_token"), false);
  auth.setToken(auth.getToken());
  assert.equal(state.cookies.get("counterweight_session"), "1");
});

test("forged page-routing hint does not create a client credential", () => {
  const state = browser();
  state.cookies.set("counterweight_session", "1");
  assert.equal(auth.getToken(), null);
  auth.clearToken();
  assert.equal(state.cookies.size, 0);
});

test("signing out asks the server to revoke the session, then clears local state", async () => {
  const state = browser();
  auth.setToken("revocable.secret.jwt");
  const calls = [];
  const original = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    calls.push({ url, auth: init.headers.Authorization, method: init.method });
    throw new Error("server unreachable");
  };
  try {
    await auth.signOut(true);
    // With no token left there is nothing to revoke and no request is made.
    await auth.signOut();
  } finally {
    globalThis.fetch = original;
  }
  assert.deepEqual(calls, [{ url: "/api/auth/logout-all", auth: "Bearer revocable.secret.jwt", method: "POST" }]);
  assert.equal(state.storage.has("counterweight_token"), false, "token cleared even when the request failed");
});
