import { changeInput, shortCommit, validateChange } from "./review-change.ts";
import assert from "node:assert/strict";
import test from "node:test";

const draft = (over) => ({ kind: "repository", pull: "", base: "", head: "", ...over });

test("a whole-repository review sends no change", () => {
  assert.equal(validateChange(draft({}), "acme/example"), null);
  assert.equal(validateChange(draft({}), "acme/example#dev"), null);
  assert.equal(changeInput(draft({ pull: "7" })), undefined);
});

test("pull request numbers", () => {
  const pull = (value, repo = "acme/example") => validateChange(draft({ kind: "pull", pull: value }), repo);
  assert.equal(pull("7"), null);
  assert.equal(pull(" 42 "), null);
  assert.equal(pull("1000000000"), null);
  assert.equal(pull(""), "pullRequired");
  for (const bad of ["0", "-1", "1.5", "abc", "1e3", "1000000001", "12345678901"]) {
    assert.equal(pull(bad), "pullInvalid", bad);
  }
  assert.equal(pull("7", "acme/example#dev"), "repositoryHasRef");
  assert.deepEqual(changeInput(draft({ kind: "pull", pull: " 7 " })), { pull_number: 7 });
});

test("base and head", () => {
  const range = (base, head, repo = "acme/example") => validateChange(draft({ kind: "range", base, head }), repo);
  assert.equal(range("main", "feature/x"), null);
  assert.equal(range("v1.2.3", "0123456789abcdef0123456789abcdef01234567"), null);
  assert.equal(range("", "x"), "baseRequired");
  assert.equal(range("main", ""), "headRequired");
  assert.equal(range("-x", "y"), "baseInvalid");
  assert.equal(range("a..b", "y"), "baseInvalid");
  assert.equal(range("main", "has space"), "headInvalid");
  assert.equal(range("main", "x".repeat(256)), "headInvalid");
  assert.equal(range("main", "x", "acme/example#dev"), "repositoryHasRef");
  assert.deepEqual(changeInput(draft({ kind: "range", base: " main ", head: " topic " })), {
    base: "main",
    head: "topic",
  });
});

test("short commits", () => {
  assert.equal(shortCommit("0123456789abcdef"), "0123456");
  assert.equal(shortCommit(undefined), "");
});
