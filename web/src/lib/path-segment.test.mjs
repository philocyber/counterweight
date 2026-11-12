import { isAgentKey, isNumericId, seg, segPath } from "./path-segment.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("seg keeps ordinary identifiers and encodes everything that can change the path", () => {
  assert.equal(seg("planner"), "planner");
  assert.equal(seg(42), "42");
  assert.equal(seg("t-acme-web"), "t-acme-web");
  assert.equal(seg("../intercept/tool-config?"), "..%2Fintercept%2Ftool-config%3F");
  assert.equal(seg("a#b"), "a%23b");
  assert.equal(seg("a%2Fb"), "a%252Fb");
});

test("seg neutralises dot-only values that a browser would resolve", () => {
  assert.equal(seg(".."), "%252E%252E");
  assert.equal(seg("."), "%252E");
  assert.equal(seg("..."), "%252E%252E%252E");
});

test("segPath encodes each segment and keeps the separators", () => {
  assert.equal(segPath("docs/read me.md"), "docs/read%20me.md");
  assert.equal(segPath("../x"), "%252E%252E/x");
  assert.equal(segPath("a/b?/c"), "a/b%3F/c");
});

test("identifier grammars", () => {
  assert.equal(isAgentKey("planner"), true);
  assert.equal(isAgentKey("custom_x1"), true);
  for (const bad of ["", "../intercept/tool-config?", "Planner", "1a", "a-b", "a/b", "a\n"]) {
    assert.equal(isAgentKey(bad), false, bad);
  }
  assert.equal(isNumericId("12"), true);
  for (const bad of ["", "0", "01", "1/2", "../1", "1?", "a"]) assert.equal(isNumericId(bad), false, bad);
});
