# Example engagement (template)

A start-to-finish walkthrough for running one Counterweight engagement against a web
target **you own or are explicitly authorized to test**, and turning the result
into a deliverable. Use it as a template: copy the steps, substitute your own
authorized target, your own model provider and your own scope.

> **Authorization first.** Only run Counterweight against systems you own or have
> written permission to test. The [Terms of Use](../README.md#license-and-disclaimer)
> and the upstream usage restrictions apply. This document does not authorize
> testing of any third party.
>
> The engagement below uses `www.acme.example` as a placeholder target (the
> `.example` domain is reserved and does not resolve) to show how an operator
> would configure scope for *their own* site: replace it with yours. The
> **Illustrative report** at the end is generated against a throwaway lab host
> (`demo-lab.example`) with fabricated, hardening-style observations — it is
> synthetic sample output to show the report *format*, not a real assessment of
> any live site.

## 1. Prerequisites

- A running Counterweight backend with its PostgreSQL database (see the
  [setup options](../README.md#installation)).
- A model provider configured in **LLM → New**. Pick a template (for example a
  strong reasoning model for the planner and a faster one for workers) and
  paste your own API key. See [LLM provider templates](llm-providers.md).
- The traffic-recording proxy enabled if you want HTTP evidence bound to
  findings (**System settings → Traffic capture**).

## 2. Define the authorized scope

Create a company and attach the domains, IPs or CIDRs you are authorized to
test, on the **Assets** page. Scope is the coverage denominator and the boundary
the agents respect.

| Field | Example value |
| --- | --- |
| Company | `PhiloCyber` |
| Root domain | `acme.example` |
| In-scope host | `www.acme.example` |

Keep the scope tight. Anything outside it is a coverage *proposal* an operator
must approve in the task Overview, not an authorization to act.

## 3. Turn on the approval gate before running anything

In **Interception rules**, enable interception for `bash` and the other
execution tools so any unmatched command waits for a human. This is the control
that keeps an autonomous run inside its boundaries; the approval gate is not an
OS sandbox, so still run the backend in an isolated environment. Review
decisions afterward in **Approval records**.

## 4. Create the task

**Tasks → New task**, or describe the engagement in **Chat** and let the main
agent create it. A good task states one verifiable outcome and names the target.

| Field | Example |
| --- | --- |
| Name | `PhiloCyber external surface` |
| Description | `Authorized external review of www.acme.example. Owner-operated site; testing permitted.` |
| Target | `Identify externally exposed weaknesses on www.acme.example and report any that are confirmed with evidence.` |
| LLM profile chain | `<your planner profile>` (falls back to the active profile) |

## 5. Watch it run

The task detail view shows the exploration graph, the planner/worker sessions
and per-step activity. The **Dashboard** summarises active tasks, token usage
and recent findings live. Most planner wake-ups send no new intent — that means
there is nothing new to try, not that something is wrong.

## 6. Triage findings

Open **Findings**. For each row, read the summary and any bound **traffic
evidence**, then set a workflow status (pending → confirmed / fixed / false
positive). Use **Retest** to re-validate a finding in a separate session after a
fix.

## 7. Export the deliverable

From a task's **Report** tab (or **Findings → Export**) you can produce a
Markdown / ZIP / CSV / JSON report. The Markdown report is rendered by
[`report.Markdown`](../report/report.go); its shape is shown below.

---

## Illustrative report (synthetic sample)

> Synthetic output against a throwaway lab host. Every value here is fabricated
> to show the report format. It is **not** an assessment of any real site. These are routine hardening observations, not exploits.

```markdown
# Penetration test report — Example external surface review

- **Task objective**: Identify externally exposed weaknesses on the lab host and report any confirmed with evidence.
- **Generated at**: 2026-10-22 00:00:00

## Summary

- Registered findings: **2** (review status is shown per finding)
- Assets: app 1, endpoint 3, root_domain 1, service 2, subdomain 1

## Scope and coverage

- root_domain: demo-lab.example (source: operator)
- subdomain: www.demo-lab.example (source: operator)

Assets with recorded results: **6 / 8** within this task and its direct read-only source context. This is approximate asset coverage, not a completed security test suite.

## Findings

### 1. [Low] Missing Content-Security-Policy header

- Finding ID: 1
- Review status: confirmed

Responses from the web root do not set a Content-Security-Policy header, so the browser applies no script-source allowlist. Low risk on its own; it removes one defense-in-depth layer against injected content.

**Recorded evidence:**

GET / returned 200 with security headers present (HSTS, X-Content-Type-Options) but no Content-Security-Policy. Recommendation: add a CSP starting in report-only mode.

### 2. [Info] Server version disclosed in response header

- Finding ID: 2
- Review status: confirmed

The Server response header includes a precise version string. Informational: it narrows an attacker's search for version-specific issues. Recommendation: suppress or generalize the header at the edge.

**Recorded evidence:**

GET / response header: `Server: <product>/<version>`. No version-specific weakness was confirmed against it.
```

No critical or high-severity findings in this sample — a realistic outcome for a
hardened site, where the value is in the recorded coverage and the few
hardening recommendations above.
