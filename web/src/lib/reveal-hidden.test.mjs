import { revealHiddenChars } from "./reveal-hidden.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("every bidi, zero-width, blank and tag code point is written out", () => {
  const points = [];
  for (let c = 0x202a; c <= 0x202e; c++) points.push(c);
  for (let c = 0x2066; c <= 0x2069; c++) points.push(c);
  for (let c = 0x200b; c <= 0x200f; c++) points.push(c);
  points.push(0x00a0, 0x2800, 0x3000, 0x2028, 0x2029, 0xfeff, 0x00ad, 0x061c, 0xe0041, 0xe000, 0x0000, 0x000d, 0x007f);
  for (const c of points) {
    const out = revealHiddenChars(`a${String.fromCodePoint(c)}b`);
    const hex = c.toString(16).toUpperCase().padStart(4, "0");
    assert.equal(out, `a<U+${hex}>b`, hex);
  }
});

test("lone surrogates are written out", () => {
  assert.equal(revealHiddenChars("x\ud800y"), "x<U+D800>y");
});

test("ordinary text, accents, CJK, RTL letters, emoji, spaces, tabs and newlines are unchanged", () => {
  const text = 'echo "héllo 你好 שלום ✓"\n\tls -la  /tmp';
  assert.equal(revealHiddenChars(text), text);
});

test("the input string is not modified", () => {
  const original = "rm\u202e -rf";
  const copy = original;
  revealHiddenChars(original);
  assert.equal(original, copy);
  assert.equal(original.length, 7);
});
