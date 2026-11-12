// Demo data and routes of a task's authorization check (/tasks/{id}/authz...).
// Account secrets are never kept here: like the server, the mock only records whether one was given.

import type { AuthzAccount, AuthzPolicyCheck, AuthzReport, AuthzRun } from "../types";

interface MockAuthzState {
  accounts: AuthzAccount[];
  policy: string;
  policyUpdatedAt: string | null;
  check: AuthzPolicyCheck | null;
  running: boolean;
  runs: AuthzRun[];
}

const DEMO_POLICY = `version: 1

target:
  base_url: https://staging.example.com

personas:
  anonymous: {}

  admin_tenant_a:
    login:
      request:
        method: POST
        path: /api/login
        json:
          email: "\${account:admin_tenant_a.username}"
          password: "\${account:admin_tenant_a.password}"
      token: { json_pointer: /token, prefix: "Bearer " }
    identity:
      request: { method: GET, path: /api/me }
      body_contains: ['"email":"root@tenant-a.test"']

  user_tenant_b:
    login:
      request:
        method: POST
        path: /api/login
        json:
          email: "\${account:user_tenant_b.username}"
          password: "\${account:user_tenant_b.password}"
      token: { json_pointer: /token, prefix: "Bearer " }
    identity:
      request: { method: GET, path: /api/me }
      body_contains: ['"email":"b@tenant-b.test"']

cases:
  - id: admin-users
    request: { method: GET, path: /api/admin/users }
    expect:
      anonymous: { deny: 401 }
      admin_tenant_a: { allow: 200 }
      user_tenant_b: { deny: 403 }

  # Another tenant gets 404, not 403: the API hides that the document exists.
  - id: doc-a-read
    request: { method: GET, path: /api/docs/1001 }
    expect:
      anonymous: { deny: 401 }
      admin_tenant_a: { allow: 200 }
      user_tenant_b: { deny: 404 }
`;

function demoReport(startedAt: string): AuthzReport {
  const check = (persona: string, expect: string, expected: number, status: number) => ({
    persona,
    expect,
    expected_status: [expected],
    status,
    result: "pass",
    duration_ms: 90,
    response_bytes: 120,
  });
  return {
    schema: "counterweight-authz-report/1",
    policy_sha256: "",
    target: "https://staging.example.com",
    started_at: startedAt,
    duration_ms: 840,
    result: "fail",
    exit_code: 1,
    counts: { cases: 2, checks: 6, passed: 5, failed: 1, inconclusive: 0 },
    personas: [
      { name: "anonymous", authenticated: false, identity_verified: false },
      { name: "admin_tenant_a", role: "Admin tenant A", authenticated: true, identity_verified: true },
      { name: "user_tenant_b", role: "User tenant B", authenticated: true, identity_verified: true },
    ],
    cases: [
      {
        id: "admin-users",
        method: "GET",
        path: "/api/admin/users",
        result: "pass",
        checks: [
          check("anonymous", "deny", 401, 401),
          check("user_tenant_b", "deny", 403, 403),
          check("admin_tenant_a", "allow", 200, 200),
        ],
      },
      {
        id: "doc-a-read",
        method: "GET",
        path: "/api/docs/1001",
        result: "fail",
        checks: [
          check("anonymous", "deny", 401, 401),
          {
            ...check("user_tenant_b", "deny", 404, 200),
            result: "fail",
            kind: "access_granted",
            detail: "expected a denial with status 404, but the target answered 200",
            excerpt: '{"id":1001,"tenant":"a","title":"Quarterly plan"}',
          },
          check("admin_tenant_a", "allow", 200, 200),
        ],
      },
    ],
  };
}

function demoRun(id: number, startedAt: string): AuthzRun {
  const report = demoReport(startedAt);
  return {
    id,
    result: report.result,
    exit_code: report.exit_code,
    target: report.target,
    started_at: report.started_at,
    duration_ms: report.duration_ms,
    counts: report.counts,
    report,
  };
}

// A rough stand-in for the server's validation, enough to show both outcomes in the demo.
function demoCheck(policy: string, accounts: AuthzAccount[]): AuthzPolicyCheck {
  const personas = ["anonymous", ...accounts.map((account) => account.key)];
  if (!policy.trim()) return { ok: false, errors: ["the policy is empty"], personas: [], cases: 0, checks: 0 };
  const cases = policy.match(/^\s*- id:/gm)?.length ?? 0;
  if (/\bTODO\b/.test(policy)) {
    return {
      ok: false,
      errors: ["target: base_url is required", "case example: every persona needs exactly one of allow and deny"],
      personas,
      cases,
      checks: cases * personas.length,
    };
  }
  return { ok: true, personas, cases, checks: cases * personas.length };
}

const states = new Map<string, MockAuthzState>();
let nextId = 100;

function stateOf(taskId: string): MockAuthzState {
  let state = states.get(taskId);
  if (!state) {
    const savedAt = new Date(Date.now() - 2 * 3600_000).toISOString();
    const accounts: AuthzAccount[] = [
      {
        id: nextId++,
        key: "admin_tenant_a",
        role: "Admin tenant A",
        username: "root@tenant-a.test",
        has_password: true,
        has_token: false,
        agent_access: false,
        updated_at: savedAt,
      },
      {
        id: nextId++,
        key: "user_tenant_b",
        role: "User tenant B",
        username: "b@tenant-b.test",
        has_password: true,
        has_token: false,
        agent_access: false,
        updated_at: savedAt,
      },
    ];
    state = {
      accounts,
      policy: DEMO_POLICY,
      policyUpdatedAt: savedAt,
      check: demoCheck(DEMO_POLICY, accounts),
      running: false,
      runs: [demoRun(nextId++, new Date(Date.now() - 3600_000).toISOString())],
    };
    states.set(taskId, state);
  }
  return state;
}

// mockAuthz answers the routes under /tasks/{id}/authz. `seg` is the split path, `b` the JSON body.
export function mockAuthz(m: string, seg: string[], b: Record<string, unknown>): unknown {
  const state = stateOf(seg[1]);
  const rest = seg.slice(3).join("/");

  if (rest === "" && m === "GET") {
    return {
      accounts: state.accounts,
      policy: state.policy,
      policy_updated_at: state.policyUpdatedAt,
      check: state.check,
      running: state.running,
      runs: state.runs.map(({ report: _report, ...summary }) => summary),
    };
  }

  if (rest === "accounts" && m === "POST") {
    const role = String(b.role ?? "").trim();
    if (!role) throw new Error("role is required");
    const key = role.toLowerCase().replace(/[^a-z0-9]/g, "_");
    if (state.accounts.some((account) => account.key === key)) {
      throw new Error(`another account of this task already uses the key ${key}`);
    }
    const account: AuthzAccount = {
      id: nextId++,
      key,
      role,
      username: String(b.username ?? ""),
      has_password: Boolean(b.password),
      has_token: Boolean(b.token),
      agent_access: b.agent_access === true,
      updated_at: new Date().toISOString(),
    };
    state.accounts.push(account);
    return account;
  }

  if (seg[3] === "accounts" && seg.length === 5) {
    const index = state.accounts.findIndex((account) => account.id === Number(seg[4]));
    if (index < 0) throw new Error("account not found");
    if (m === "DELETE") {
      state.accounts.splice(index, 1);
      return undefined;
    }
    if (m === "PATCH") {
      const account = state.accounts[index];
      if (typeof b.role === "string") account.role = b.role.trim();
      if (typeof b.username === "string") account.username = b.username;
      if (typeof b.password === "string") account.has_password = b.password !== "";
      if (typeof b.token === "string") account.has_token = b.token !== "";
      if (typeof b.agent_access === "boolean") account.agent_access = b.agent_access;
      account.updated_at = new Date().toISOString();
      return account;
    }
  }

  if (rest === "policy" && m === "PUT") {
    state.policy = String(b.policy ?? "");
    state.policyUpdatedAt = new Date().toISOString();
    state.check = demoCheck(state.policy, state.accounts);
    return { policy_updated_at: state.policyUpdatedAt, check: state.check };
  }

  if (rest === "runs" && m === "POST") {
    if (state.running) throw new Error("a run is already in progress");
    if (!state.check?.ok) throw new Error("the saved policy does not validate");
    state.running = true;
    setTimeout(() => {
      state.runs = [demoRun(nextId++, new Date().toISOString()), ...state.runs].slice(0, 20);
      state.running = false;
    }, 3000);
    return { running: true };
  }

  if (seg[3] === "runs" && seg.length === 5 && m === "GET") {
    const run = state.runs.find((item) => item.id === Number(seg[4]));
    if (!run) throw new Error("run not found");
    return run;
  }

  throw new Error("not found");
}
