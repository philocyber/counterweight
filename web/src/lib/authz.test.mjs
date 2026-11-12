import {
  AUTHZ_POLICY_MAX_BYTES,
  accountReference,
  authzMatrix,
  failedChecks,
  personaRoles,
  policyTooLarge,
  starterPolicy,
} from "./authz.ts";
import assert from "node:assert/strict";
import test from "node:test";

const TEXT = {
  intro: "intro",
  baseUrl: "base url",
  signIn: "sign in",
  token: "token",
  fixedToken: "fixed token",
  identity: "identity",
  cases: "cases",
  expect: "expect",
};

const ACCOUNTS = [
  { key: "admin_tenant_a", role: "Admin tenant A", has_token: false },
  { key: "user_b", role: "User B", has_token: true },
];

// The policy lines that are not comments.
function content(policy) {
  return policy.split("\n").filter((line) => !line.trim().startsWith("#"));
}

test("an account reference names the account key and one of its fields", () => {
  assert.equal(accountReference("admin_tenant_a", "username"), `\${account:admin_tenant_a.username}`);
  assert.equal(accountReference("admin_tenant_a", "password"), `\${account:admin_tenant_a.password}`);
  assert.equal(accountReference("admin_tenant_a", "token"), `\${account:admin_tenant_a.token}`);
});

test("the policy size limit counts bytes, not characters", () => {
  assert.equal(policyTooLarge("a".repeat(AUTHZ_POLICY_MAX_BYTES)), false);
  assert.equal(policyTooLarge("a".repeat(AUTHZ_POLICY_MAX_BYTES + 1)), true);
  assert.equal(policyTooLarge("ñ".repeat(AUTHZ_POLICY_MAX_BYTES / 2 + 1)), true);
});

test("the starter policy has one persona per account plus anonymous", () => {
  const lines = content(starterPolicy(ACCOUNTS, TEXT));
  assert.ok(lines.includes("  anonymous: {}"));
  assert.ok(lines.includes("  admin_tenant_a:"));
  assert.ok(lines.includes("  user_b:"));
  // An account without a token signs in with its username and password.
  assert.ok(lines.includes(`          username: "\${account:admin_tenant_a.username}"`));
  assert.ok(lines.includes(`          password: "\${account:admin_tenant_a.password}"`));
  // An account with a token sends it as a fixed credential and needs no sign-in request.
  assert.ok(lines.includes(`      Authorization: "Bearer \${account:user_b.token}"`));
  assert.ok(!lines.some((line) => line.includes(`\${account:user_b.password}`)));
});

test("the starter policy decides no expectation, target or identity text", () => {
  const policy = starterPolicy(ACCOUNTS, TEXT);
  const lines = content(policy);
  const expect = lines.slice(lines.indexOf("    expect:") + 1).filter((line) => line.trim());
  assert.deepEqual(expect, ["      anonymous:", "      admin_tenant_a:", "      user_b:"]);
  assert.ok(!lines.some((line) => /\b(allow|deny)\s*:/.test(line)));
  assert.ok(lines.includes("  base_url:"));
  assert.equal(lines.filter((line) => line === "      body_contains: []").length, ACCOUNTS.length);
  assert.ok(policy.endsWith("\n"));
});

test("the starter policy works without accounts and never repeats a persona", () => {
  const empty = content(starterPolicy([], TEXT));
  assert.ok(empty.includes("  anonymous: {}"));
  assert.ok(empty.includes("      anonymous:"));

  const own = content(starterPolicy([{ key: "anonymous", role: "Anonymous", has_token: false }], TEXT));
  assert.ok(!own.includes("  anonymous: {}"));
  assert.equal(own.filter((line) => line === "  anonymous:").length, 1);
  assert.equal(own.filter((line) => line === "      anonymous:").length, 1);
});

test("a role name cannot add lines to the starter policy", () => {
  const policy = starterPolicy([{ key: "x", role: "Admin\nversion: 2\r\n  injected: true", has_token: false }], TEXT);
  assert.ok(policy.includes("  # Admin version: 2 injected: true\n"));
  assert.ok(!content(policy).some((line) => line.includes("injected")));
  assert.equal(content(policy).filter((line) => line.startsWith("version:")).length, 1);
});

const REPORT = {
  personas: [
    { name: "anonymous", authenticated: false, identity_verified: false },
    { name: "admin_tenant_a", role: "Admin tenant A", authenticated: true, identity_verified: true },
    { name: "user_b", role: " ", authenticated: true, identity_verified: false },
  ],
  cases: [
    {
      id: "admin-users",
      method: "GET",
      path: "/api/admin/users",
      result: "fail",
      checks: [
        { persona: "anonymous", expect: "deny", expected_status: [401], status: 401, result: "pass" },
        { persona: "user_b", expect: "deny", expected_status: [403], status: 500, result: "inconclusive" },
        { persona: "admin_tenant_a", expect: "allow", expected_status: [200], status: 200, result: "pass" },
      ],
    },
    {
      id: "doc-a-update",
      method: "PUT",
      path: "/api/docs/1",
      write: true,
      result: "fail",
      checks: [
        { persona: "user_b", expect: "deny", expected_status: [404], status: 200, result: "fail" },
        { persona: "ghost", expect: "deny", expected_status: [404], status: 404, result: "pass" },
      ],
    },
  ],
};

test("a persona is shown by its role name, or by its key when it has none", () => {
  const roles = personaRoles(REPORT);
  assert.equal(roles.get("admin_tenant_a"), "Admin tenant A");
  assert.equal(roles.get("anonymous"), "anonymous");
  assert.equal(roles.get("user_b"), "user_b");
  assert.equal(personaRoles({}).size, 0);
  assert.equal(personaRoles({ personas: null }).size, 0);
});

test("the matrix has one row per case and one cell per persona, in report order", () => {
  const { columns, rows } = authzMatrix(REPORT);
  assert.deepEqual(
    columns.map((column) => [column.key, column.role, column.identityVerified]),
    [
      ["anonymous", "anonymous", false],
      ["admin_tenant_a", "Admin tenant A", true],
      ["user_b", "user_b", false],
      // Named by a check only: appended so its result is not dropped.
      ["ghost", "ghost", false],
    ],
  );
  assert.deepEqual(
    rows.map((row) => [row.id, row.write, row.cells.map((cell) => cell?.result ?? null)]),
    [
      ["admin-users", false, ["pass", "pass", "inconclusive", null]],
      ["doc-a-update", true, [null, null, "fail", "pass"]],
    ],
  );
  assert.deepEqual(authzMatrix({}), { columns: [], rows: [] });
  assert.deepEqual(
    authzMatrix({ personas: null, cases: [{ id: "c", method: "GET", path: "/", result: "pass", checks: null }] })
      .rows[0].cells,
    [],
  );
});

test("checks that did not pass are listed with the role name, failures first", () => {
  const problems = failedChecks(REPORT);
  assert.deepEqual(
    problems.map((problem) => [problem.caseId, problem.persona, problem.role, problem.check.result]),
    [
      ["doc-a-update", "user_b", "user_b", "fail"],
      ["admin-users", "user_b", "user_b", "inconclusive"],
    ],
  );
  assert.deepEqual(failedChecks({}), []);
});
