import { accountReference } from "./authz.ts";
import type {
  AuthzAccount,
  AuthzDocCase,
  AuthzDocExpectation,
  AuthzDocPersona,
  AuthzDocRequest,
  AuthzDocument,
} from "./types.ts";

// A policy as data, for the matrix editor: what the editor changes, and how the result is written
// back as policy text. The server reads that text like any other policy, so nothing here decides
// whether a policy is valid.

export const POLICY_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;

// Methods the server sends only when the case confirms it with allow_write.
export function changesData(method: string): boolean {
  return ["POST", "PUT", "PATCH", "DELETE"].includes(method);
}

export function emptyDocument(): AuthzDocument {
  return { version: 1, target: { base_url: "" }, personas: [], cases: [] };
}

// ---------------------------------------------------------------- policy text

const PLAIN_TEXT = /^[A-Za-z_/][A-Za-z0-9_./-]*$/;
// Words YAML reads as something other than text when they are not quoted.
const NOT_TEXT = new Set(["true", "false", "null", "yes", "no", "on", "off", "y", "n", "nan", "inf"]);

function quoted(value: string): string {
  // A JSON string is a YAML double-quoted string, except for a few characters YAML wants escaped.
  return JSON.stringify(value).replace(
    /[\u007f-\u009f\u2028\u2029\ufeff]/g,
    (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
}

function text(value: string): string {
  return PLAIN_TEXT.test(value) && !NOT_TEXT.has(value.toLowerCase()) ? value : quoted(value);
}

// A JSON value on one line. Every key and string is quoted, so it reads the same as YAML.
function inline(value: unknown): string {
  if (value === null || value === undefined) return "null";
  if (typeof value === "string") return quoted(value);
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.length === 0 ? "[]" : `[${value.map(inline).join(", ")}]`;
  const entries = Object.entries(value as Record<string, unknown>);
  if (entries.length === 0) return "{}";
  return `{ ${entries.map(([k, v]) => `${quoted(k)}: ${inline(v)}`).join(", ")} }`;
}

function statuses(codes: number[]): string {
  return codes.length === 1 ? String(codes[0]) : `[${codes.join(", ")}]`;
}

function textMap(out: string[], indent: string, name: string, map?: Record<string, string> | null) {
  if (!map) return;
  const entries = Object.entries(map);
  if (entries.length === 0) {
    out.push(`${indent}${name}: {}`);
    return;
  }
  out.push(`${indent}${name}:`);
  for (const [k, v] of entries) out.push(`${indent}  ${text(k)}: ${text(v)}`);
}

function textList(out: string[], indent: string, name: string, list?: string[] | null) {
  if (!list || list.length === 0) return;
  out.push(`${indent}${name}:`);
  for (const item of list) out.push(`${indent}  - ${text(item)}`);
}

function request(out: string[], indent: string, req: AuthzDocRequest) {
  out.push(`${indent}request:`);
  const inner = `${indent}  `;
  out.push(`${inner}method: ${text(req.method ?? "")}`);
  out.push(`${inner}path: ${text(req.path ?? "")}`);
  textMap(out, inner, "headers", req.headers && Object.keys(req.headers).length > 0 ? req.headers : null);
  if (req.json !== undefined && req.json !== null) out.push(`${inner}json: ${inline(req.json)}`);
  textMap(out, inner, "form", req.form);
  if (req.body) out.push(`${inner}body: ${text(req.body)}`);
}

function sameCodes(codes: number[] | undefined | null, only: number): boolean {
  return !codes || codes.length === 0 || (codes.length === 1 && codes[0] === only);
}

function persona(out: string[], p: AuthzDocPersona) {
  const empty =
    !p.role &&
    !(p.headers && Object.keys(p.headers).length > 0) &&
    !(p.cookies && Object.keys(p.cookies).length > 0) &&
    !p.login &&
    !p.identity;
  if (empty) {
    out.push(`  ${text(p.key)}: {}`);
    return;
  }
  out.push(`  ${text(p.key)}:`);
  if (p.role) out.push(`    role: ${text(p.role)}`);
  textMap(out, "    ", "headers", p.headers && Object.keys(p.headers).length > 0 ? p.headers : null);
  textMap(out, "    ", "cookies", p.cookies && Object.keys(p.cookies).length > 0 ? p.cookies : null);
  if (p.login) {
    out.push("    login:");
    request(out, "      ", p.login.request);
    // 200 and Authorization are what the server assumes when the policy says nothing.
    if (!sameCodes(p.login.status, 200)) out.push(`      status: ${statuses(p.login.status ?? [])}`);
    if (p.login.token) {
      out.push("      token:");
      out.push(`        json_pointer: ${text(p.login.token.json_pointer ?? "")}`);
      if (p.login.token.header && p.login.token.header !== "Authorization") {
        out.push(`        header: ${text(p.login.token.header)}`);
      }
      if (p.login.token.prefix) out.push(`        prefix: ${quoted(p.login.token.prefix)}`);
    }
  }
  if (p.identity) {
    out.push("    identity:");
    request(out, "      ", p.identity.request);
    if (!sameCodes(p.identity.status, 200)) out.push(`      status: ${statuses(p.identity.status ?? [])}`);
    textList(out, "      ", "body_contains", p.identity.body_contains);
  }
}

function expectation(out: string[], key: string, e: AuthzDocExpectation | null | undefined) {
  const name = `      ${text(key)}:`;
  if (!e) {
    out.push(name);
    return;
  }
  const verdict: string[] = [];
  if (e.allow && e.allow.length > 0) verdict.push(`allow: ${statuses(e.allow)}`);
  if (e.deny && e.deny.length > 0) verdict.push(`deny: ${statuses(e.deny)}`);
  const markers = (e.body_contains?.length ?? 0) > 0 || (e.body_not_contains?.length ?? 0) > 0 || !!e.location_contains;
  if (!markers) {
    out.push(verdict.length > 0 ? `${name} { ${verdict.join(", ")} }` : name);
    return;
  }
  out.push(name);
  for (const line of verdict) out.push(`        ${line}`);
  textList(out, "        ", "body_contains", e.body_contains);
  textList(out, "        ", "body_not_contains", e.body_not_contains);
  if (e.location_contains) out.push(`        location_contains: ${text(e.location_contains)}`);
}

// policyText writes a document as policy text. Comments of an earlier text are not part of a
// document, so they are not written.
export function policyText(doc: AuthzDocument): string {
  const out: string[] = [`version: ${doc.version}`, "", "target:", `  base_url: ${text(doc.target.base_url ?? "")}`];
  if (doc.target.allow_plain_http) out.push("  allow_plain_http: true");
  if (doc.target.timeout_seconds) out.push(`  timeout_seconds: ${doc.target.timeout_seconds}`);
  out.push("");
  if (doc.personas.length === 0) out.push("personas: {}");
  else {
    out.push("personas:");
    for (const p of doc.personas) persona(out, p);
  }
  out.push("");
  if (doc.cases.length === 0) out.push("cases: []");
  else {
    out.push("cases:");
    const known = new Set(doc.personas.map((p) => p.key));
    for (const c of doc.cases) {
      out.push(`  - id: ${text(c.id)}`);
      if (c.allow_write) out.push("    allow_write: true");
      if (c.deny_all) out.push("    deny_all: true");
      request(out, "    ", c.request);
      const expect = c.expect ?? {};
      // Personas in their own order, then any name the policy does not define, so nothing is dropped.
      const keys = [...doc.personas.map((p) => p.key), ...Object.keys(expect).filter((key) => !known.has(key))];
      if (keys.length === 0) continue;
      out.push("    expect:");
      for (const key of keys) expectation(out, key, Object.hasOwn(expect, key) ? expect[key] : null);
    }
  }
  return `${out.join("\n")}\n`;
}

// ---------------------------------------------------------------- sign-in of a persona

// How a persona signs in, when it is one of the shapes the editor shows as a form.
export type SignIn =
  | { kind: "anonymous" }
  // Sends the account's stored token in a header, optionally after a scheme word such as Bearer.
  | { kind: "token"; header: string; scheme: string }
  | {
      kind: "login";
      method: string;
      path: string;
      format: "json" | "form";
      usernameField: string;
      passwordField: string;
      // Where the session comes from: a token in the JSON answer, or cookies the answer sets.
      session: "token" | "cookie";
      pointer: string;
      header: string;
      prefix: string;
    };

export const DEFAULT_LOGIN: Extract<SignIn, { kind: "login" }> = {
  kind: "login",
  method: "POST",
  path: "/api/login",
  format: "json",
  usernameField: "username",
  passwordField: "password",
  session: "token",
  pointer: "/token",
  header: "Authorization",
  prefix: "Bearer ",
};

function isTextRecord(value: unknown): value is Record<string, string> {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    Object.values(value).every((v) => typeof v === "string")
  );
}

function hasEntries(map?: Record<string, string> | null): boolean {
  return !!map && Object.keys(map).length > 0;
}

// signInOf reads how a persona signs in. It returns null for anything written in a way the form
// does not cover (several headers, cookies, a body with nested values...); that persona keeps its
// text and is edited as text.
export function signInOf(p: AuthzDocPersona): SignIn | null {
  const headers = Object.entries(p.headers ?? {});
  if (headers.length === 0 && !hasEntries(p.cookies) && !p.login) return { kind: "anonymous" };
  if (hasEntries(p.cookies)) return null;
  if (!p.login) {
    if (headers.length !== 1) return null;
    const [name, value] = headers[0];
    const reference = accountReference(p.key, "token");
    if (value === reference) return { kind: "token", header: name, scheme: "" };
    const scheme = /^([A-Za-z][A-Za-z0-9-]*) /.exec(value);
    if (scheme && value === `${scheme[1]} ${reference}`) return { kind: "token", header: name, scheme: scheme[1] };
    return null;
  }
  if (headers.length > 0) return null;
  const req = p.login.request;
  if (hasEntries(req.headers) || req.body) return null;
  const hasJson = req.json !== undefined && req.json !== null;
  const hasForm = req.form !== undefined && req.form !== null;
  if (hasJson === hasForm) return null;
  const fields = hasJson ? req.json : req.form;
  if (!isTextRecord(fields)) return null;
  const find = (field: "username" | "password") => {
    const matches = Object.entries(fields).filter(([, v]) => v === accountReference(p.key, field));
    return matches.length === 1 ? matches[0][0] : null;
  };
  const usernameField = find("username");
  const passwordField = find("password");
  if (!usernameField || !passwordField) return null;
  const token = p.login.token;
  return {
    kind: "login",
    method: req.method || "POST",
    path: req.path ?? "",
    format: hasJson ? "json" : "form",
    usernameField,
    passwordField,
    session: token ? "token" : "cookie",
    pointer: token?.json_pointer ?? DEFAULT_LOGIN.pointer,
    header: token?.header || DEFAULT_LOGIN.header,
    prefix: token ? (token.prefix ?? "") : DEFAULT_LOGIN.prefix,
  };
}

// withSignIn returns the persona signing in the given way. Fields of the sign-in request that the
// form does not show (a fixed tenant name, say) are kept.
export function withSignIn(p: AuthzDocPersona, signIn: SignIn): AuthzDocPersona {
  const next: AuthzDocPersona = { key: p.key };
  if (p.role) next.role = p.role;
  if (signIn.kind === "anonymous") return next;
  if (p.identity) next.identity = p.identity;
  if (signIn.kind === "token") {
    const reference = accountReference(p.key, "token");
    next.headers = {
      [signIn.header.trim()]: signIn.scheme.trim() ? `${signIn.scheme.trim()} ${reference}` : reference,
    };
    return next;
  }
  const before = signInOf(p);
  const old = p.login?.request;
  const oldFields = (before?.kind === "login" ? (old?.json ?? old?.form) : null) as Record<string, string> | null;
  const kept = Object.entries(oldFields ?? {}).filter(
    ([k]) => !(before?.kind === "login" && (k === before.usernameField || k === before.passwordField)),
  );
  // Built from entries, so a field with a name such as __proto__ is a field like any other.
  const fields = Object.fromEntries([
    ...kept.filter(([k]) => k !== signIn.usernameField.trim() && k !== signIn.passwordField.trim()),
    [signIn.usernameField.trim(), accountReference(p.key, "username")],
    [signIn.passwordField.trim(), accountReference(p.key, "password")],
  ]);
  next.login = {
    request: { method: signIn.method, path: signIn.path.trim(), [signIn.format]: fields },
    ...(p.login?.status ? { status: p.login.status } : {}),
    ...(signIn.session === "token"
      ? { token: { json_pointer: signIn.pointer.trim(), header: signIn.header.trim(), prefix: signIn.prefix } }
      : {}),
  };
  return next;
}

// identityOf and withIdentity read and set the check that proves a session belongs to its persona.
export function identityOf(p: AuthzDocPersona): { path: string; texts: string[] } {
  return { path: p.identity?.request.path ?? "", texts: p.identity?.body_contains ?? [] };
}

export function withIdentity(p: AuthzDocPersona, path: string, texts: string[]): AuthzDocPersona {
  const { identity: _old, ...rest } = p;
  if (signInOf(p)?.kind === "anonymous") return rest;
  return {
    ...rest,
    identity: {
      request: { ...p.identity?.request, method: "GET", path: path.trim() },
      ...(p.identity?.status ? { status: p.identity.status } : {}),
      body_contains: texts,
    },
  };
}

// personaForAccount builds the persona of a test account. It signs in like `like` (another
// account's persona) when that one has a shape the form knows, and with the usual defaults
// otherwise. Its identity check looks for the account's username, which a profile page usually shows.
export function personaForAccount(
  account: Pick<AuthzAccount, "key" | "has_token" | "has_password" | "username">,
  like?: AuthzDocPersona,
): AuthzDocPersona {
  const shape = like ? signInOf(like) : null;
  let signIn: SignIn;
  if (shape && shape.kind !== "anonymous") signIn = shape;
  else if (account.has_token && !account.has_password)
    signIn = { kind: "token", header: "Authorization", scheme: "Bearer" };
  else signIn = DEFAULT_LOGIN;
  const base = withSignIn({ key: account.key }, signIn);
  const path = (like && identityOf(like).path) || "/api/me";
  return withIdentity(base, path, account.username ? [accountReference(account.key, "username")] : []);
}

// ---------------------------------------------------------------- cases

// caseIdFor names a case after its request ("get-api-projects"), unique among `taken`.
export function caseIdFor(method: string, path: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  const slug = `${method} ${path}`
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 56);
  const base = slug || "request";
  if (!used.has(base)) return base;
  for (let n = 2; ; n++) if (!used.has(`${base}-${n}`)) return `${base}-${n}`;
}

// A case whose id still follows its request keeps following it when the request changes.
export function hasGeneratedId(c: AuthzDocCase): boolean {
  const base = caseIdFor(c.request.method ?? "", c.request.path ?? "", []);
  return c.id === "" || c.id === base || (c.id.startsWith(`${base}-`) && /^\d+$/.test(c.id.slice(base.length + 1)));
}

// parseStatuses reads "200, 204" as status codes; null when the text is not a list of numbers.
export function parseStatuses(value: string): number[] | null {
  const parts = value.split(/[\s,]+/).filter(Boolean);
  if (parts.length === 0 || parts.some((part) => !/^\d{3}$/.test(part))) return null;
  return [...new Set(parts.map(Number))];
}

// expectationOf reads what a case expects for a persona. A persona may be named like something
// every object has ("constructor"), so only the case's own entries count.
export function expectationOf(c: AuthzDocCase, key: string): AuthzDocExpectation | null {
  return c.expect && Object.hasOwn(c.expect, key) ? (c.expect[key] ?? null) : null;
}

// withVerdict sets whether a persona may perform a case, and with which statuses. The texts the
// answer is checked for stay: the server applies them to an allowed answer and to a denial alike.
export function withVerdict(
  e: AuthzDocExpectation | null | undefined,
  verdict: "allow" | "deny",
  codes: number[],
): AuthzDocExpectation {
  const { allow: _allow, deny: _deny, ...checks } = e ?? {};
  return { ...checks, [verdict]: codes };
}

// The editor shows a list of texts one per line. A text that holds a line break, or starts or ends
// with a space, cannot be shown that way without changing it.
export function fitsLines(texts: string[] | null | undefined): boolean {
  return (texts ?? []).every((item) => item !== "" && item === item.trim() && !/[\r\n]/.test(item));
}

// linesOf reads such a field back. Untouched, it gives the list it was filled from, unchanged.
export function linesOf(value: string, original: string[] | null | undefined): string[] {
  if (value === (original ?? []).join("\n")) return original ?? [];
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

export function verdictOf(e: AuthzDocExpectation | null | undefined): "allow" | "deny" | null {
  if (e?.allow && e.allow.length > 0) return "allow";
  if (e?.deny && e.deny.length > 0) return "deny";
  return null;
}
