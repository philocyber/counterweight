# Dreadnode capabilities import (2026-10-22)

Source: [dreadnode/capabilities](https://github.com/dreadnode/capabilities), commit
`52f369eae047ba7a8b6b72d650355e4c6be4ec42`, MIT licensed (see NOTICE for the few
third-party exceptions, none of which fall in the imported set below).

Dreadnode ships "capabilities" (manifest + agents/tools/skills/MCP) for its own `dn`
runtime. Counterweight uses the same `SKILL.md` convention (agentskills.io format: YAML
frontmatter + Markdown body, progressive disclosure, per-agent visibility), so no
schema conversion was needed — the actual work was **content curation**: picking what
is relevant to a web-app pentesting agent that executes through Bash/HTTP (Counterweight's
real tool surface), checking for overlap with Counterweight's existing 3 skills
(`api-recon`, `playwright-cli`, `scopesentry`), and excluding anything that assumes a
toolchain Counterweight doesn't have or that conflicts with Counterweight's own usage policy.

Decision rule, in order: **(1) policy** — exclude destructive/DoS content outright;
**(2) relevance** — Counterweight is web-app focused, not AD/network, not AI-model
red-teaming, not mobile/binary forensics; **(3) tool coupling** — generic
curl/HTTP/browser technique ports cleanly, content wrapped around a Dreadnode-only
tool (Caido, BBOT, Shodan MCP, Pacu, HackerOne MCP, kiterunner) does not, without a
real rewrite; **(4) collision** — where Dreadnode's content overlaps an existing
Counterweight skill or covers the same ground as another imported skill, keep the better one
or merge, never duplicate.

## Excluded for policy (not a judgment call)

| Skill | Why |
| --- | --- |
| `web-security/app-layer-dos` | Application-layer DoS technique guide (ReDoS, decompression bombs, billion-laughs). Counterweight's own Terms of Use (README, Article 3) explicitly prohibit testing for "denial of service (DoS/DDoS)", and the project does not ship denial-of-service technique content. Not imported, in any form. |

## Out of scope for Counterweight today (not imported)

Different target class than Counterweight tests (web applications via Bash/HTTP). Noted here
so the reasoning is visible, not just a silent drop — revisit if Counterweight ever adds that
scope.

| Capability | Why out of scope |
| --- | --- |
| `ai-red-teaming` (10 skills) | Tests AI/ML model robustness (jailbreaks, ASR scoring) via Dreadnode's own AIRT platform/SDK — a different discipline from web-app pentesting, 100% coupled to that platform. |
| `network-ops` (3 skills) | Active Directory / internal network exploitation (Kerberoasting, impacket, certipy). Counterweight targets web applications, not internal AD networks. |
| `secure-software` (3 skills) | Package/SBOM triage via Spectra Assure, a specific commercial tool Counterweight doesn't integrate. |
| `attack-surface-management/bbot-module-reference`, `/cypher-query-playbook` | BBOT module reference and Cypher queries against BBOT's own Neo4j schema — meaningless without BBOT and that specific graph schema. |
| `attack-surface-management/shodan-reconnaissance` | Valuable dork-syntax knowledge, but wrapped entirely around Dreadnode's Shodan MCP tool. Folded the reusable query-syntax ideas into the new `recon-planning` skill instead of a standalone port. |
| `web-security/screenshot-triage` | Triage logic is sound but the retrieval layer is Cypher queries against BBOT's Neo4j `WEBSCREENSHOT` nodes — Counterweight has no equivalent store. Would need a real rewrite against Counterweight's asset graph, not a port; left as a backlog idea. |
| `web-security/{burp-suite,caido-mode,caido-proxy,caido-sdk}` (4 skills) | Wrap a specific commercial/open proxy's API. Counterweight has no Burp/Caido integration. |
| `web-security/agent-browser` | Wraps Vercel Labs' agent-browser CLI (Apache-2.0, separately licensed) — redundant with Counterweight's own `playwright-cli` skill, which already covers browser automation. |
| `web-security/hackerone-recon` | Scoped to HackerOne program-policy workflows; Counterweight has its own scope/company model, not a bug-bounty-program concept. |
| `web-security/ip-rotation` | Wraps Dreadnode's own `flareprox`/`fireprox` proxy-rotation tooling. |
| `web-security/pacu-aws-exploitation` | Wraps the Pacu AWS post-exploitation framework, not part of Counterweight's tool surface. |
| `web-security/wrangler-oast` | Wraps Cloudflare Workers + `wrangler` CLI for a custom OAST catcher — infra-specific. |
| `web-security/scorer-reference` | Dreadnode's own SDK rubric for its `task.yaml` evaluation harness, not pentesting content. |
| `web-security/phone-verification` | Automates third-party SMS-verification services; several such services' own terms prohibit automated use. Excluded on relevance and caution grounds, not imported even adapted. |
| `web-security/kiterunner` | CLI reference for the `kr` binary (Assetnote), not installed by Counterweight. The underlying idea (Swagger/OpenAPI-derived wordlists beat generic bruteforce) is one line in `recon-planning` instead. |
| `web-security/distributed-routing-testing` | Needs paid Shodan/Censys API access for its core value; self-gates behind an env var already. Left as a backlog idea rather than a first-batch import. |
| `web-security/data-exfil` | On closer read this is AI/LLM output-exfiltration technique content (markdown/HTML/SVG/KaTeX rendering gadgets to leak data from an AI app's own responses), not generic web app pentesting — the same category already excluded as `ai-red-teaming`. It also hard-depends on Dreadnode's `get_callback_url` OOB tool, which Counterweight has no equivalent for. Not imported, despite an earlier draft of this manifest listing it as "adapted" — that was a curation error, corrected here. `vuln-assessment-methodology`'s Hard Rule 7 (AI prompt injection is a design concern unless AI output feeds a security-sensitive sink) already covers the one genuinely web-app-relevant angle. |
| `web-security/report-preflight` | Scoped to bug-bounty-platform submission eligibility (HackerOne/Bugcrowd-specific ineligible-finding lists) — Counterweight has its own finding workflow, not a bug-bounty queue. Its one genuinely reusable part (the Tier-2 "common AI/scanner over-inflated patterns" table) was merged into `vuln-assessment-methodology` instead of importing the whole skill — see below. |

## Imported as-is or lightly adapted

All of the following are generic, curl/HTTP/browser-level technique or methodology
content with no Dreadnode-tool dependency (verified by grepping every file for
Caido/BBOT/Shodan/HackerOne/MCP call patterns before importing — only `data-exfil`
matched, handled below). Each keeps its original content; only the frontmatter/header
gained a one-line provenance note. Supplementary `references/`, `scripts/`, and PoC
files were carried over alongside their `SKILL.md`.

**Methodology (2):**
`vuln-assessment-methodology` (ported as-is, nothing in Counterweight covered this gap —
Counterweight's `report_finding` only takes a free-text `severity` string with no guidance on
assigning it, no confidence levels, no anti-false-positive rules; **enriched** with
report-preflight's Tier-2 over-inflated-pattern table, credited in its own header),
`vuln-critic` (adversarial finding pre-filter; references to Dreadnode's own
`exploit-verifier`/`report_item` names were rewritten to Counterweight's actual retest flow
and `report_finding` tool).

**Vulnerability-class technique skills (59, one line each in the commit, not repeated
here):** 403-bypass, aem-sling-exploitation, apache-confusion-attacks,
archive-path-traversal, auth-matrix-testing, blind-sqli-extraction,
blind-ssrf-chains, browser-side-channel, config-file-parsing-bugs,
content-type-mime-diff, crlf-response-splitting, csp-bypass, cspt-xss,
custom-sanitizer-audit, dom-vulnerability-detection,
dom-vulnerability-static-analysis, dompurify-mxss-bypass, doubleclickjacking,
esi-injection, exploit-verifier, git-integration-exploitation, graphql-pentest,
grpc-web-pentest, h2-connect-internal-scan, h2-waf-bypass, h2c-websocket-smuggling,
http-connection-contamination, http-desync-smuggling, http-query-method,
inline-script-breakout-exfil, insecure-defaults, libmagic-type-confusion,
math-rendering-macro-injection, mcp-auth-exploitation, nextjs-cache-poisoning,
oauth-flow-hijack, orm-filter-data-leak, parser-differential-bypass,
php-filter-chain-oracle, race-condition-single-packet, response-queue-poisoning,
saas-provider-url-ssrf, salesforce-aura-testing, self-xss-escalation,
soapwn-wsdl-rce, sqli-to-rce-escalation, ssrf-ip-filter-bypass, ssrf-redirect-loop,
ssti-error-based-detection, subdomain-takeover-check, te0-request-smuggling,
timing-attack-recon, type-confusion-testing, unicode-normalization-bypass,
url-prompt-injection, vuln-kb, web-cache-deception-path, write-path-to-rce,
xslt-injection.

Later change: `auth-matrix-testing` gained one Counterweight-written section, "Regression Policy
Draft", marked as an addition inside the skill. The imported content is unchanged.

## New, Counterweight-native (not a port)

`recon-planning` is a new skill, not copied from any single source — it synthesizes
the phased-approach idea from `attack-surface-management/reconnaissance-planning`
and `web-security/initial-recon`, rewritten end to end against Counterweight's actual tools
(`add_company_scope`, `add_task_scope`, `list_untested_assets`, `insert_assets`, the
asset coverage view) instead of BBOT/Shodan MCP calls. It references the
ProjectDiscovery suite (`subfinder`/`dnsx`/`httpx`/`naabu`) as an optional
enhancement an operator can declare once installed (single static Go binaries,
declared in **System → Tools** the same way as nmap/sqlmap/ffuf — Counterweight does not
install them), with the same graceful-fallback-to-Bash pattern Counterweight's own Tools
system already documents, rather than assuming they're present.

## Verification

- Every imported `SKILL.md`'s `name:` matches Counterweight's lowercase-letters/digits/hyphens
  validation (`server/platform_tools.go`).
- Loaded in a running Counterweight instance (System → Skills); no validation errors.
- `go build`/`go vet` and the frontend's `tsc`/`biome`/tests are unaffected (this
  import adds only `skills/` content, no code).
