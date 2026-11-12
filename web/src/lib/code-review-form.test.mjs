import {
  CODE_REVIEW_TEMPLATE_GOAL,
  codeReviewTemplateDescription,
  joinRepositoryRef,
  splitRepositoryRef,
  validateRef,
  validateRepository,
} from "./code-review-form.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("repository forms the server accepts pass", () => {
  for (const value of [
    "owner/name",
    " owner/name ",
    "owner/na.me-x_1",
    "owner/name.git",
    "owner/name#release",
    "https://github.com/owner/name",
    "https://github.com/owner/name.git",
    "https://github.com/owner/name/",
    "https://github.com/owner/name/tree/feature/x",
    "github.com/owner/name",
  ]) {
    assert.equal(validateRepository(value), null, value);
  }
});

test("repository mistakes get a specific problem", () => {
  assert.equal(validateRepository(""), "required");
  assert.equal(validateRepository("   "), "required");
  assert.equal(validateRepository("owner/na me"), "whitespace");
  assert.equal(validateRepository("http://github.com/owner/name"), "scheme");
  assert.equal(validateRepository("https://token@github.com/owner/name"), "credentials");
  assert.equal(validateRepository("https://user:pw@github.com/owner/name"), "credentials");
  assert.equal(validateRepository("git@github.com:owner/name"), "credentials");
  assert.equal(validateRepository("https://gitlab.com/owner/name"), "host");
  assert.equal(validateRepository("owner"), "format");
  assert.equal(validateRepository("owner/name/extra"), "format");
  assert.equal(validateRepository("-owner/name"), "format");
  assert.equal(validateRepository("owner/.name"), "format");
  assert.equal(validateRepository(`owner/${"a".repeat(101)}`), "format");
  assert.equal(validateRepository("owner/name#"), "format");
  assert.equal(validateRepository("owner/name#a..b"), "format");
  assert.equal(validateRepository("https://github.com/owner/name/issues"), "format");
});

test("refs follow the parser limits", () => {
  assert.equal(validateRef(""), null);
  assert.equal(validateRef("main"), null);
  assert.equal(validateRef("feature/x-1.2"), null);
  assert.equal(validateRef("0123abcd"), null);
  assert.equal(validateRef("-x"), "format");
  assert.equal(validateRef("a..b"), "format");
  assert.equal(validateRef("a b"), "format");
  assert.equal(validateRef("a".repeat(256)), "format");
});

test("template presets round-trip the ref inside the repository", () => {
  assert.equal(joinRepositoryRef("owner/name", " v1 "), "owner/name#v1");
  assert.equal(joinRepositoryRef("owner/name", ""), "owner/name");
  assert.equal(joinRepositoryRef("owner/name#a", "b"), "owner/name#a");
  assert.deepEqual(splitRepositoryRef("owner/name#v1"), { repository: "owner/name", ref: "v1" });
  assert.deepEqual(splitRepositoryRef("owner/name"), { repository: "owner/name", ref: "" });
  assert.equal(codeReviewTemplateDescription(""), "");
  assert.equal(codeReviewTemplateDescription("owner/name"), "Code review: owner/name");
  assert.ok(CODE_REVIEW_TEMPLATE_GOAL.length > 0);
});
