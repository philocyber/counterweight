---
name: vuln-assessment-methodology
description: "Load when performing vulnerability assessment in any domain. Enforces source-to-sink tracing, disprove-first analysis, threat-model-aware severity, confidence classification, attack chain analysis, CWE mapping, remediation quality, root-cause deduplication, scope documentation, and opt-in PoC validation. Prevents false positives and severity inflation."
---

# Vulnerability Assessment Methodology

> Adapted from [dreadnode/capabilities](https://github.com/dreadnode/capabilities) (`vuln-assessment-methodology`, MIT License), commit `52f369e`, merged with the reusable part of its `web-security/report-preflight` (same license) — see [docs/CAPABILITIES-IMPORT-2026-10-22.md](../../docs/CAPABILITIES-IMPORT-2026-10-22.md). Applies directly to `report_finding`'s `severity`/`summary`/`evidence` fields.

**The goal is accurate, honest findings — not volume.** One correctly-assessed
finding is worth more than ten inflated ones.

## Hard Rules

### 1. NEVER report a sink without tracing the full data flow

Seeing a dangerous function is NOT a finding. Trace from **attacker-controlled
source** through every transformation to the sink. If sanitization exists, the
finding is invalid unless you demonstrate a specific bypass.

**Before reporting, answer:**
- What is the attacker-controlled input?
- What transformations does it undergo?
- Does any transformation neutralize the attack?
- Can you construct a concrete input that reaches the sink?

### 2. Try to DISPROVE your finding before reporting it

Actively look for evidence it is NOT exploitable. Read the FULL function. Look
for validation, input filtering, authorization checks, type constraints,
config gates. If you find defensive code, demonstrate a bypass or retract.

### 3. Severity must reflect the ACTUAL threat model

Assign severity by source, access, and context — not vulnerability class name.

| Source of dangerous input | Access required | Severity |
|---|---|---|
| HTTP request param | Unauthenticated, internet-facing | Critical/High |
| HTTP request param | Authenticated user | High/Medium |
| HTTP request param | Internal network only | Medium |
| Config/env var | Container-level access | Low |
| Hardcoded value (as sink input) | N/A | Not a finding (but hardcoded credentials are — actual secrets, not placeholders or example values) |

**What each level requires:**
- **Critical**: Unauth RCE, hardcoded prod credentials, full auth bypass. Attacker needs nothing beyond a network connection.
- **High**: Authed RCE, SQL injection via HTTP params, stored XSS, SSRF from internet-facing endpoint.
- **Medium**: Incomplete validation bypass, internal-only exposure, defense-in-depth gaps.
- **Low**: Defense-in-depth issues (env var in SQL), code quality that could become exploitable.
- **Not a finding**: Complete upstream sanitization, config-as-designed, token parsed for metadata only, framework defaults, attacker already needs higher privileges.

### 4. Read the COMPLETE defensive code

If validation exists but is incomplete (e.g., blocks `;` but not `"`), report
the **specific bypass**, not "no sanitization." Severity reflects the bypass
narrowness, not unrestricted injection impact.

- BAD: "Command injection via bash -c with no sanitization" (HIGH)
- GOOD: "Incomplete validation in ValidateCommand() — misses `\"`, allowing
  quote-escape from bash -c wrapping" (MEDIUM)

### 5. Configuration options are not vulnerabilities

Dev mode / unsecured mode is a design decision unless attacker-toggleable.

### 6. Internal tools have different threat models

Report missing auth on internal tools as a **dependency on network controls**,
not "missing security." Don't assign CRITICAL unless evidence shows internet
exposure.

### 7. AI prompt injection is a design concern, not a code bug

Not a code vulnerability unless AI output feeds a security-sensitive sink
(e.g., `eval()`, SQL, shell commands, file paths, unencoded HTML).

### 8. Distinguish application code from framework code

Don't report framework defaults as vulnerabilities.

### 9. Flag obvious attack chains — do not force them

When multiple findings converge on a single exploitable outcome, note the
chain. An IDOR + information disclosure + missing rate limiting may each be
Medium alone but chain to account takeover. Report the chain as an additional
compound finding with its own severity reflecting the combined impact — keep
the individual findings too, since each needs its own remediation. Do not
exhaustively search for chains — flag them when apparent from findings already
identified.

## Confidence Levels

Every finding must include a confidence level. When the full source-to-sink
trace is complete, mark it Confirmed. When it is not, classify the gap.

| Level | Criteria | Documentation required |
|---|---|---|
| Confirmed | Full trace complete, concrete payload constructable | Complete data flow from source to sink with specific input |
| Probable | Most of trace complete, specific gap identified | State the exact gap (e.g., "dynamic dispatch at line 42 — two implementors exist, both pass input unsanitized") |
| Suspected | Pattern match or shallow trace only | State what additional analysis (dynamic testing, debug tracing, etc.) would confirm or refute |

Common trace gaps: dynamic dispatch, reflection, external dependencies, plugin
systems, runtime-generated code. Always name the specific mechanism that
blocked the trace.

## Reporting Standards

Reports must:
- State access prerequisites explicitly
- Note existing defensive code and why it's insufficient
- Map each finding to the most specific applicable CWE ID (leaf-level variant,
  not the pillar — e.g., CWE-89 not CWE-74)
- Include specific, actionable remediation referencing the technology in use
  and the code location where the fix applies (not "add input validation" but
  "use parameterized queries via `db.Query()` with placeholder args at
  `handler.go:47`")
- When multiple findings share a root cause, report one root-cause finding
  with a list of affected locations rather than separate findings per instance
- State what was analyzed and what was not — files, components, and entry
  points covered, plus what could not be assessed (runtime behavior,
  infrastructure config, third-party dependency internals)
- Be defensible under peer review by a senior security engineer

## Proof-of-Concept Validation (Opt-in)

Default behavior is to trace data flow and assess exploitability conceptually.
Do not construct payloads or simulate execution unless the user requests it.

If the user requests proof-of-concept validation:
- Construct a concrete payload that demonstrates the vulnerability
- Document exact attacker-controlled input values
- Show the code path execution trace from source to sink
- State environmental prerequisites (auth state, config, timing)
- For web targets: provide the specific HTTP request that triggers the issue

## Common Over-Inflated Patterns

These are **not vulnerabilities without specific proof of impact** — scanners and
agents over-report them constantly. Reportable only when the right-hand column is
actually demonstrated, not assumed.

| Pattern | Not a finding because | IS a finding when |
|---|---|---|
| Source map / `REACT_APP_*` env vars exposed | Dev tooling / client-side config by design | Contains hardcoded secrets or write-access API keys (not public client IDs) |
| Service returns 401/403 | Auth is working correctly | A bypass is demonstrated, or the body leaks data despite the status code |
| CORS misconfiguration | Informational without exploitation | Demonstrates credential theft or cross-origin data exfiltration |
| Staging/dev endpoint exposure | May be intentional | Contains real user PII, valid credentials, or unauthenticated admin access |
| Blind SSRF (boolean oracle only) | No demonstrated data access | Demonstrates port scanning, internal service interaction, or metadata access |
| Username/email enumeration | Low impact, often by design | Enables an account-takeover chain or violates a privacy requirement in scope |
| Rotated/expired credentials found in JS | No longer valid | Still valid and grant access |
| Public OpenAPI/Swagger schema | Documentation, not a vulnerability | Contains a validated API key or reveals an exploitable unauthenticated endpoint |
| Exposed admin panel | Access ≠ vulnerability | Unauthenticated access to sensitive data or actions |
| GraphQL introspection enabled | Often intentional | Reveals hidden mutations reachable without authentication |
| Verbose stack trace | Informational | Leaks secrets, internal IPs, or a specific exploitable version |
| Public `.git` directory | Depends on contents | Contains secrets, credentials, or sensitive source |
| Open redirect | Informational alone | Chains with OAuth token theft, session fixation, or a verified phishing path in scope |
| Missing rate limiting | Best practice | Enables brute force with a demonstrated account compromise |
| Subdomain-takeover candidate | Often a false positive | Verified claimable against the provider's own fingerprint, not just CNAME presence |
| Clickjacking | No sensitive action on the page | The framed page performs a state-changing action |
| CSRF on a non-sensitive form | Logout, language toggle, etc. | The form changes account state or security settings |

## Anti-patterns

| Anti-pattern | Example | Why it's wrong |
|---|---|---|
| Sink-only analysis | "Dangerous function found → vuln" | Didn't check upstream defenses |
| Ignoring defensive code | "Injection, no sanitization" when validation exists | Didn't read the full function |
| Class-name severity | "SQL injection → HIGH" regardless of source | Env var source ≠ HTTP param source |
| Feature-as-vulnerability | "Unsecured mode exists" | Documented design decision |
| Framework noise | "Framework uses cookies" | Expected framework behavior |
| Theoretical-only | "If attacker could modify env vars..." | Attacker already has code exec |
| Quantity over quality | 10 low-confidence findings | 1 verified > 10 guesses |
| Context-free severity | "No auth → CRITICAL" on internal tool | Deployment model matters |
| Confirmation bias | Rationalizing why mitigations don't count | Try to disprove first |
| Forced chaining | "These 3 lows chain to Critical" without shared attack flow | Chain must converge on a single exploitable outcome, not just co-exist |
| Generic remediation | "Add input validation" | Must name specific fix and code location |
| Duplicate inflation | 15 separate XSS findings from one missing encoder | One root cause = one finding + affected locations |
