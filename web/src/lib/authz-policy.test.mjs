import {
  caseIdFor,
  changesData,
  DEFAULT_LOGIN,
  emptyDocument,
  expectationOf,
  fitsLines,
  hasGeneratedId,
  identityOf,
  linesOf,
  parseStatuses,
  personaForAccount,
  policyText,
  signInOf,
  verdictOf,
  withIdentity,
  withSignIn,
  withVerdict,
} from "./authz-policy.ts";
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import test from "node:test";

// Fixtures shared with the server's tests (authz/document_test.go): the document the server hands
// the editor for a policy, and the text the editor writes back. The server's test reads that text
// again and requires the same document.
const fixture = (name) => new URL(`../../../authz/testdata/${name}`, import.meta.url);
const read = (name) => readFileSync(fixture(name), "utf8").replaceAll("\r\n", "\n");

test("policyText writes the shared fixture", () => {
  const emitted = policyText(JSON.parse(read("editor-document.json")));
  if (process.env.UPDATE_FIXTURES) writeFileSync(fixture("editor-emitted.yaml"), emitted);
  assert.equal(emitted, read("editor-emitted.yaml"), "run UPDATE_FIXTURES=1 npm test, then go test ./authz");
});

test("policyText quotes what YAML would not read as the same text", () => {
  const doc = emptyDocument();
  doc.target.base_url = "https://staging.example.test";
  doc.personas = [{ key: "anonymous" }, { key: "true", role: "No: really #1" }];
  doc.cases = [
    {
      id: "a",
      request: { method: "GET", path: "/a b" },
      expect: { anonymous: { deny: [401] }, true: null },
    },
  ];
  const out = policyText(doc);
  assert.match(out, /^ {2}anonymous: \{\}$/m);
  assert.match(out, /^ {2}"true":$/m);
  assert.match(out, /^ {4}role: "No: really #1"$/m);
  assert.match(out, /^ {6}path: "\/a b"$/m);
  assert.match(out, /^ {6}anonymous: \{ deny: 401 \}$/m);
  // A persona without a verdict stays in the text, so the server names it as a problem.
  assert.match(out, /^ {6}"true":$/m);
  assert.equal(policyText(emptyDocument()), 'version: 1\n\ntarget:\n  base_url: ""\n\npersonas: {}\n\ncases: []\n');
});

test("policyText keeps an expectation for a persona the policy does not define", () => {
  const doc = emptyDocument();
  doc.personas = [{ key: "anonymous" }];
  doc.cases = [{ id: "a", request: { method: "GET", path: "/a" }, expect: { ghost: { deny: [403] } } }];
  assert.match(policyText(doc), /^ {6}ghost: \{ deny: 403 \}$/m);
});

test("signInOf reads the shapes the form shows and nothing else", () => {
  const doc = JSON.parse(read("editor-document.json"));
  const by = Object.fromEntries(doc.personas.map((p) => [p.key, p]));
  assert.deepEqual(signInOf(by.anonymous), { kind: "anonymous" });
  assert.deepEqual(signInOf(by.viewer), { ...DEFAULT_LOGIN });
  assert.deepEqual(signInOf(by.editor), {
    kind: "login",
    method: "POST",
    path: "/session",
    format: "form",
    usernameField: "user",
    passwordField: "pass",
    session: "cookie",
    pointer: "/token",
    header: "Authorization",
    prefix: "Bearer ",
  });
  assert.deepEqual(signInOf(by.admin), { kind: "token", header: "X-Api-Key", scheme: "" });
  assert.deepEqual(signInOf(by["007"]), { kind: "token", header: "Authorization", scheme: "Bearer" });

  // Written some other way: left to the text view.
  for (const persona of [
    { key: "a", cookies: { session: "${account:a.token}" } },
    { key: "a", headers: { A: "${account:a.token}", B: "${account:a.token}" } },
    { key: "a", headers: { Authorization: "Bearer ${account:other.token}" } },
    { key: "a", login: { request: { method: "POST", path: "/l", json: { user: { name: "${account:a.username}" } } } } },
    { key: "a", login: { request: { method: "POST", path: "/l", json: { u: "${account:a.username}" } } } },
    { key: "a", login: { request: { method: "POST", path: "/l", body: "u=1" } } },
  ]) {
    assert.equal(signInOf(persona), null, JSON.stringify(persona));
  }
});

test("withSignIn changes the sign-in and keeps what the form does not show", () => {
  const doc = JSON.parse(read("editor-document.json"));
  const viewer = doc.personas.find((p) => p.key === "viewer");
  const moved = withSignIn(viewer, { ...DEFAULT_LOGIN, path: "/auth", usernameField: "email", session: "cookie" });
  assert.deepEqual(moved.login.request, {
    method: "POST",
    path: "/auth",
    json: { tenant: "acme", email: "${account:viewer.username}", password: "${account:viewer.password}" },
  });
  assert.equal(moved.login.token, undefined);
  assert.deepEqual(moved.identity, viewer.identity);
  assert.deepEqual(signInOf(moved), {
    ...DEFAULT_LOGIN,
    path: "/auth",
    usernameField: "email",
    session: "cookie",
  });

  const token = withSignIn(viewer, { kind: "token", header: "Authorization", scheme: "Bearer" });
  assert.deepEqual(token.headers, { Authorization: "Bearer ${account:viewer.token}" });
  assert.equal(token.login, undefined);

  // An anonymous persona has no session to prove.
  assert.deepEqual(withSignIn(viewer, { kind: "anonymous" }), { key: "viewer" });
});

test("withIdentity sets the proof of a signed-in persona only", () => {
  const signedIn = withIdentity(withSignIn({ key: "a" }, DEFAULT_LOGIN), " /me ", ["a@example.test"]);
  assert.deepEqual(identityOf(signedIn), { path: "/me", texts: ["a@example.test"] });
  assert.equal(signedIn.identity.request.method, "GET");
  assert.equal(withIdentity({ key: "anonymous" }, "/me", ["x"]).identity, undefined);
});

test("personaForAccount signs in like the persona it is modelled on", () => {
  const doc = JSON.parse(read("editor-document.json"));
  const editor = doc.personas.find((p) => p.key === "editor");
  const auditor = personaForAccount({ key: "auditor", has_password: true, has_token: false, username: "au" }, editor);
  assert.deepEqual(auditor.login.request.form, {
    user: "${account:auditor.username}",
    pass: "${account:auditor.password}",
  });
  assert.equal(auditor.login.request.path, "/session");
  assert.deepEqual(identityOf(auditor), { path: "/api/me", texts: ["${account:auditor.username}"] });
  assert.ok(!JSON.stringify(auditor).includes("account:editor."));

  const alone = personaForAccount({ key: "svc", has_password: false, has_token: true, username: "" });
  assert.deepEqual(signInOf(alone), { kind: "token", header: "Authorization", scheme: "Bearer" });
  assert.deepEqual(identityOf(alone), { path: "/api/me", texts: [] });
  assert.deepEqual(
    signInOf(personaForAccount({ key: "u", has_password: true, has_token: false, username: "u" })),
    DEFAULT_LOGIN,
  );
});

test("case ids follow the request until someone names the case", () => {
  assert.equal(caseIdFor("GET", "/api/projects", []), "get-api-projects");
  assert.equal(caseIdFor("GET", "/api/projects", ["get-api-projects", "get-api-projects-2"]), "get-api-projects-3");
  assert.equal(caseIdFor("", "", []), "request");
  assert.match(caseIdFor("DELETE", `/${"x".repeat(200)}`, []), /^[a-z0-9][a-z0-9_.-]{0,63}$/);
  const request = { method: "GET", path: "/api/projects" };
  assert.equal(hasGeneratedId({ id: "get-api-projects", request }), true);
  assert.equal(hasGeneratedId({ id: "get-api-projects-2", request }), true);
  assert.equal(hasGeneratedId({ id: "", request }), true);
  assert.equal(hasGeneratedId({ id: "projects-list", request }), false);
  assert.equal(hasGeneratedId({ id: "get-api-projects-x", request }), false);
});

test("statuses and verdicts", () => {
  assert.deepEqual(parseStatuses("200"), [200]);
  assert.deepEqual(parseStatuses(" 200, 204 200 "), [200, 204]);
  for (const bad of ["", "abc", "20", "200.5", "2000", "200;204"]) assert.equal(parseStatuses(bad), null, bad);
  assert.equal(changesData("PUT"), true);
  assert.equal(changesData("GET"), false);

  const denied = withVerdict({ allow: [200] }, "deny", [403]);
  assert.deepEqual(denied, { deny: [403] });
  assert.equal(verdictOf(denied), "deny");
  // The server checks the texts of an answer whatever the verdict, so a change of status or of
  // verdict never drops them.
  const marked = { deny: [200], body_contains: ["Access denied"], location_contains: "/login" };
  assert.deepEqual(withVerdict(marked, "deny", [302]), { ...marked, deny: [302] });
  const shown = { allow: [200], body_contains: ["Quarterly plan"], body_not_contains: ["error"] };
  assert.deepEqual(withVerdict(shown, "allow", [200, 204]), { ...shown, allow: [200, 204] });
  assert.deepEqual(withVerdict(shown, "deny", [403]), {
    deny: [403],
    body_contains: ["Quarterly plan"],
    body_not_contains: ["error"],
  });
  assert.equal(verdictOf(null), null);
  assert.equal(verdictOf({}), null);
});

test("a persona named like something every object has is still only a persona", () => {
  const item = { id: "a", request: { method: "GET", path: "/a" }, expect: { viewer: { allow: [200] } } };
  assert.deepEqual(expectationOf(item, "viewer"), { allow: [200] });
  for (const key of ["constructor", "toString", "__proto__", "missing"]) assert.equal(expectationOf(item, key), null);
  assert.equal(expectationOf({ id: "a", request: {} }, "viewer"), null);

  const doc = emptyDocument();
  doc.personas = [{ key: "constructor" }];
  doc.cases = [{ id: "a", request: { method: "GET", path: "/a" }, expect: {} }];
  assert.match(policyText(doc), /^ {6}constructor:$/m);

  // A sign-in field may have any name, too.
  const odd = withSignIn({ key: "a" }, { ...DEFAULT_LOGIN, usernameField: "__proto__" });
  assert.deepEqual(Object.keys(odd.login.request.json), ["__proto__", "password"]);
  assert.match(policyText({ ...emptyDocument(), personas: [odd] }), /"__proto__": "\$\{account:a\.username\}"/);
  // Renaming a field onto one that is already there leaves one field, not two.
  const merged = withSignIn(
    {
      key: "a",
      login: {
        request: {
          method: "POST",
          path: "/l",
          json: { email: "x", username: "${account:a.username}", password: "${account:a.password}" },
        },
      },
    },
    { ...DEFAULT_LOGIN, path: "/l", usernameField: "email" },
  );
  assert.deepEqual(merged.login.request.json, { email: "${account:a.username}", password: "${account:a.password}" });
});

test("a list of texts shown one per line comes back unchanged unless it is edited", () => {
  const saved = ["Access\ndenied", " padded "];
  assert.equal(fitsLines(saved), false);
  assert.equal(fitsLines(["Access denied", "${account:a.username}"]), true);
  assert.equal(fitsLines(null), true);
  assert.equal(fitsLines([""]), false);
  // Untouched: the very same list, line break and spaces included.
  assert.equal(linesOf(saved.join("\n"), saved), saved);
  assert.deepEqual(linesOf("one\n\n two \n", ["one"]), ["one", "two"]);
  assert.deepEqual(linesOf("", ["one"]), []);
  assert.deepEqual(linesOf("", null), []);
});
