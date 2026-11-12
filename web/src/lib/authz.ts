import type { AuthzAccount, AuthzCheckResult, AuthzReport } from "./types";

// Pure helpers of a task's authorization check: account references, the starter policy and the
// shapes the results view renders.

export const ACCOUNT_REFERENCE_FIELDS = ["username", "password", "token"] as const;
export type AccountReferenceField = (typeof ACCOUNT_REFERENCE_FIELDS)[number];

// accountReference is the text a policy uses to take a value from the test account with this key.
export function accountReference(key: string, field: AccountReferenceField): string {
  return `\${account:${key}.${field}}`;
}

// The server refuses a larger policy.
export const AUTHZ_POLICY_MAX_BYTES = 256 * 1024;

export function policyTooLarge(text: string): boolean {
  return new TextEncoder().encode(text).length > AUTHZ_POLICY_MAX_BYTES;
}

// Comments of the starter policy, supplied by the caller in the interface language.
export interface StarterPolicyText {
  intro: string;
  baseUrl: string;
  signIn: string;
  token: string;
  fixedToken: string;
  identity: string;
  cases: string;
  expect: string;
}

// starterPolicy builds a policy to start from: one persona per test account plus anonymous, and one
// example case that lists every persona. It decides nothing for the user: the target address, the
// identity texts and every expectation are left empty, so the result does not validate until a
// person fills them in.
export function starterPolicy(
  accounts: Pick<AuthzAccount, "key" | "role" | "has_token">[],
  text: StarterPolicyText,
): string {
  // A comment stays on one line whatever the text holds, so a role name cannot add policy lines.
  const note = (indent: string, value: string) => `${indent}# ${value.replace(/\s+/g, " ").trim()}`;
  const anonymous = accounts.some((account) => account.key === "anonymous") ? [] : ["anonymous"];
  const lines = [
    note("", text.intro),
    "version: 1",
    "",
    "target:",
    note("  ", text.baseUrl),
    "  base_url:",
    "",
    "personas:",
    ...anonymous.map((key) => `  ${key}: {}`),
  ];
  for (const account of accounts) {
    lines.push("", note("  ", account.role), `  ${account.key}:`);
    if (account.has_token) {
      lines.push(
        note("    ", text.fixedToken),
        "    headers:",
        `      Authorization: "Bearer ${accountReference(account.key, "token")}"`,
      );
    } else {
      lines.push(
        "    login:",
        note("      ", text.signIn),
        "      request:",
        "        method: POST",
        "        path: /api/login",
        "        json:",
        `          username: "${accountReference(account.key, "username")}"`,
        `          password: "${accountReference(account.key, "password")}"`,
        note("      ", text.token),
        '      token: { json_pointer: /token, prefix: "Bearer " }',
      );
    }
    lines.push(
      "    identity:",
      note("      ", text.identity),
      "      request: { method: GET, path: /api/me }",
      "      body_contains: []",
    );
  }
  lines.push(
    "",
    "cases:",
    note("  ", text.cases),
    "  - id: example",
    "    request: { method: GET, path: /api/example }",
    "    expect:",
    note("      ", text.expect),
    ...[...anonymous, ...accounts.map((account) => account.key)].map((key) => `      ${key}:`),
  );
  return `${lines.join("\n")}\n`;
}

// personaRoles maps each persona key of a report to the name shown for it: the role of its test
// account, or the key itself for a persona without one (anonymous, or an account deleted since).
export function personaRoles(report: Pick<AuthzReport, "personas">): Map<string, string> {
  return new Map((report.personas ?? []).map((persona) => [persona.name, persona.role?.trim() || persona.name]));
}

export interface AuthzMatrixColumn {
  key: string;
  role: string;
  authenticated: boolean;
  identityVerified: boolean;
}

export interface AuthzMatrixRow {
  id: string;
  method: string;
  path: string;
  write: boolean;
  result: string;
  // One entry per column; null where the run sent no check for that persona.
  cells: (AuthzCheckResult | null)[];
}

// authzMatrix lays a report out as cases by personas. Columns follow the order of the report's
// personas; a persona that only appears in a check is appended so no check is dropped.
export function authzMatrix(report: Pick<AuthzReport, "personas" | "cases">): {
  columns: AuthzMatrixColumn[];
  rows: AuthzMatrixRow[];
} {
  const roles = personaRoles(report);
  const columns: AuthzMatrixColumn[] = (report.personas ?? []).map((persona) => ({
    key: persona.name,
    role: roles.get(persona.name) ?? persona.name,
    authenticated: persona.authenticated,
    identityVerified: persona.identity_verified,
  }));
  const cases = report.cases ?? [];
  for (const item of cases) {
    for (const check of item.checks ?? []) {
      if (columns.some((column) => column.key === check.persona)) continue;
      columns.push({ key: check.persona, role: check.persona, authenticated: false, identityVerified: false });
    }
  }
  const rows = cases.map((item) => ({
    id: item.id,
    method: item.method,
    path: item.path,
    write: item.write === true,
    result: item.result,
    cells: columns.map((column) => (item.checks ?? []).find((check) => check.persona === column.key) ?? null),
  }));
  return { columns, rows };
}

export interface AuthzProblem {
  caseId: string;
  method: string;
  path: string;
  persona: string;
  role: string;
  check: AuthzCheckResult;
}

// failedChecks lists every check that did not pass, failures before inconclusive ones, each group in
// the order of the report.
export function failedChecks(report: Pick<AuthzReport, "personas" | "cases">): AuthzProblem[] {
  const roles = personaRoles(report);
  const problems: AuthzProblem[] = [];
  for (const item of report.cases ?? []) {
    for (const check of item.checks ?? []) {
      if (check.result === "pass") continue;
      problems.push({
        caseId: item.id,
        method: item.method,
        path: item.path,
        persona: check.persona,
        role: roles.get(check.persona) ?? check.persona,
        check,
      });
    }
  }
  return [...problems.filter((p) => p.check.result === "fail"), ...problems.filter((p) => p.check.result !== "fail")];
}
