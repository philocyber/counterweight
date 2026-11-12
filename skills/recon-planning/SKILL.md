---
name: recon-planning
description: Use to plan and sequence reconnaissance (subdomain/asset discovery, port and service enumeration, API surface mapping) before deeper testing, and to decide which recon tool or technique fits a given target and authorization scope.
---

# Recon Planning

Phased reconnaissance plan for a new scope, built around Counterweight's own asset
model instead of any specific tool. Use this before deep-diving into a single
host so recon stays systematic and the asset graph ends up complete enough to
plan the rest of the engagement on.

> Synthesizes the phased-recon idea from [dreadnode/capabilities](https://github.com/dreadnode/capabilities)'s `attack-surface-management/reconnaissance-planning` and `web-security/initial-recon` (MIT License), commit `52f369e`, rewritten against Counterweight's own tools rather than BBOT/Shodan MCP. See [docs/CAPABILITIES-IMPORT-2026-10-22.md](../../docs/CAPABILITIES-IMPORT-2026-10-22.md).

## Phase 0: Confirm scope before touching anything

- Read the task's existing scope (companies, domains, IP ranges, keywords).
  If a target isn't covered, propose it via `add_company_scope` /
  `add_task_scope` and wait for it to be in scope — don't probe first and
  justify after.
- Note explicit exclusions (out-of-scope subdomains, rate limits, blackout
  windows) from the task's supplementary instructions. Recon moves fast and
  is the easiest phase to accidentally overrun scope in.

## Phase 1: Passive discovery (no packets to the target beyond what's already in scope)

Goal: enumerate subdomains and related assets without touching unconfirmed
hosts.

- If `subfinder` is declared as an available tool (System → Tools, same as
  nmap/sqlmap/ffuf), run it for subdomain enumeration — it's a single static
  Go binary with no account required and is the fastest path to a subdomain
  list.
- Without it, fall back to Bash: certificate-transparency lookups
  (`crt.sh`'s JSON endpoint via curl), the target's own `robots.txt`/sitemap,
  and DNS records already visible from prior assets.
- If a Shodan API key is configured as a tool/credential, dork syntax like
  `hostname:"<domain>"`, `ssl.cert.subject.cn:"<domain>"`, or
  `http.title:"<app-name>"` surfaces internet-facing hosts and exposed
  services without sending them a single request — valuable when direct
  scanning is restricted by the engagement's rules of engagement. Without a
  key, skip this step; don't substitute an unauthenticated scrape of a paid
  recon service.
- Record every discovered host via `insert_assets` as it's found, not in a
  single batch at the end — a crash or context cutover shouldn't lose recon
  progress.

## Phase 2: Active confirmation (now touching in-scope hosts)

Goal: find out which discovered names actually resolve and respond, and
what's listening.

- If `httpx` (ProjectDiscovery) is declared, use it to probe the subdomain
  list for live HTTP(S) hosts, status codes, titles, and tech fingerprints
  in one pass. Without it, Bash `curl -sI`/`curl -s -o /dev/null -w` loops
  work, just slower and one host at a time.
- If `naabu` is declared, use it for fast port discovery on the confirmed
  live hosts before deciding which ones need a deeper `nmap` pass (reserve
  `nmap -sV`/NSE scripts for hosts that are actually interesting — don't
  full-port-scan everything found in Phase 1).
- Update each asset's status (`insert_assets`) as it's confirmed live/dead so
  `list_untested_assets` stays an accurate work queue for the rest of the
  task.

## Phase 3: API surface mapping (once a host is confirmed to serve an app)

- Hand off to the `api-recon` skill for inventorying a confirmed app's
  endpoints, parameters, and UI-triggered requests — this skill stops at
  "the host is alive and serving something," `api-recon` takes it from
  there.
- A target's own OpenAPI/Swagger schema, GraphQL introspection, or exposed
  `.well-known` paths (if present and in scope) are worth checking before
  manual crawling — they can shortcut most of `api-recon`'s manual
  enumeration work when available, at the cost of only reflecting what the
  spec documents rather than what's actually reachable.

## When recon tools aren't installed

Every ProjectDiscovery tool referenced above (`subfinder`/`httpx`/`naabu`,
and `dnsx` for bulk DNS resolution) is optional. Counterweight doesn't install
them — like nmap/sqlmap/ffuf, an operator declares one as available in
**System → Tools** only once it's actually on the host, and the agent's
Bash access covers the same ground manually when it isn't. Don't fail or
stall a recon phase because a specific tool is missing; fall back to the
Bash equivalent noted above and keep going. Don't ask the operator to
install anything mid-task — note what a tool would have sped up and move on.

## Anti-patterns

| Anti-pattern | Why it's wrong |
|---|---|
| Full port-scanning every discovered host immediately | Noisy, slow, and usually out of proportion to what the engagement needs; confirm liveness cheaply first (Phase 2), then scan selectively |
| Treating a missing optional tool as a blocker | The whole point of declaring tools is "use if present" — Bash is always the fallback |
| Batching all discovered assets into one `insert_assets` call at the end | Loses progress on interruption; record as you go |
| Skipping Phase 0 and scanning first | Scope violations are easiest to make during recon, when things feel exploratory |
| Jumping straight to `api-recon` on an unconfirmed host | Confirm the host actually serves the app first — enumerating endpoints on a dead or wrong host wastes the whole phase |
