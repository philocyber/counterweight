---
name: ai-red-team-method
description: Plan and document an authorized assessment of an AI-enabled application. Use when mapping AI trust boundaries, choosing tests, or assembling evidence across LLM and conventional components.
---

# AI Red Team Method

Start with the engagement's authorized assets, rules, test accounts, permitted data, time window, and stop conditions. Preserve Counterweight's scope and approval gates. A course lab or CTF objective never grants permission on a real target. Prefer synthetic records and harmless markers when testing data access or state changes.

## Map before testing

Build a living map of entry points, models, prompts, agents, retrieval data, vector stores, tool servers, output renderers, identities, and deployment infrastructure. Mark each item observed, inferred, or unknown, with evidence and confidence. Keep an assumption register; absence of a documented control does not prove a vulnerability.

For each candidate path, record the attacker-controlled surface, the trust boundary, the security-relevant decision or sink, required access, and the smallest safe confirmation. Prioritize by impact, reachable prerequisites, scope, and cost. Revise the map when a hypothesis fails. Load a focused AI skill only for a surface actually present. Use existing recon-planning, api-recon, and vuln-assessment-methodology skills for their general roles.

## Evidence and result

Record exact input and placement, account or role, application and model version when known, relevant tool calls and outputs, timestamps, and a baseline response. Repeat stochastic tests enough to distinguish a one-off answer from reproducible behavior within the agreed query budget. A model saying it would act is weaker evidence than an observed tool call or state change. Separate attempted, observed, and confirmed outcomes; test a plausible benign explanation. Report the actual boundary crossed and impact, including blocked and inconclusive paths. Recommend a control at that boundary and a focused regression check.

Use current OWASP LLM risks (https://github.com/GenAI-Security-Project/GenAI-LLM-Top10), MITRE ATLAS (https://atlas.mitre.org/), and NIST AI 100-2 (https://csrc.nist.gov/pubs/ai/100/2/e2025/final) as taxonomies, not as proof of exploitability. Check the edition before assigning an identifier.

