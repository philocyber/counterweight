import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire, stripTypeScriptTypes } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
globalThis.fixtureNext = require("next/server");
const { NextRequest } = globalThis.fixtureNext;
const source = stripTypeScriptTypes(await readFile(new URL("../proxy.ts", import.meta.url), "utf8")).replace(
  'import { NextResponse } from "next/server";',
  "const { NextResponse } = globalThis.fixtureNext;",
);
const { proxy } = await import(`data:text/javascript;base64,${Buffer.from(source).toString("base64")}`);

test("page routing uses a non-secret hint and expires legacy JWT cookies", () => {
  const response = (path, cookie) =>
    proxy(new NextRequest(`http://app.example.test:3000${path}`, { headers: { cookie } }));
  assert.equal(response("/function/tasks", "").headers.get("location"), "http://app.example.test:3000/login");
  assert.equal(response("/function/tasks", "counterweight_session=1").headers.get("x-middleware-next"), "1");
  assert.equal(
    response("/login", "counterweight_session=1").headers.get("location"),
    "http://app.example.test:3000/function/tasks",
  );
  assert.equal(response("/setup", "").headers.get("x-middleware-next"), "1");
  assert.equal(
    response("/function/tasks", "counterweight_session=forged").headers.get("location"),
    "http://app.example.test:3000/login",
  );
  const legacy = response("/function/tasks", "counterweight_token=old.fixture.jwt");
  assert.equal(legacy.headers.get("location"), "http://app.example.test:3000/login");
  assert.equal(legacy.cookies.get("counterweight_token")?.value, "");
  assert.ok(!legacy.headers.get("set-cookie").includes("old.fixture.jwt"));
});

test("mock routing remains available without a token", () => {
  const previous = process.env.NEXT_PUBLIC_MOCK;
  process.env.NEXT_PUBLIC_MOCK = "1";
  try {
    assert.equal(proxy(new NextRequest("http://127.0.0.1:3000/function/tasks")).headers.get("x-middleware-next"), "1");
  } finally {
    if (previous === undefined) delete process.env.NEXT_PUBLIC_MOCK;
    else process.env.NEXT_PUBLIC_MOCK = previous;
  }
});
