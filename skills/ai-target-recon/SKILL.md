---
name: ai-target-recon
description: Identify LLM, RAG, agent, tool, and model-serving surfaces in an authorized target. Use before selecting AI-specific tests or when the system architecture is unclear.
---

# AI Target Recon

Use the existing recon-planning and api-recon skills for asset and endpoint inventory. This skill adds the AI-specific architecture map.

## Discover

Inspect in-scope UI flows, API descriptions, JavaScript, headers, response metadata, repository configuration supplied for review, and documented integrations. Note model-serving APIs, chat sessions, retrieval citations, document ingestion, agent identities, tool schemas, MCP or A2A endpoints, model registries, and renderers. Treat banners and model self-reports as clues; confirm with traffic, configuration, or behavior. Do not assume a named model is actually used.

Use a small baseline set: ordinary question, organization-specific question, and a question likely outside the knowledge base. Compare whether citations, chunk identifiers, tool calls, or latency change. Identify where untrusted user, web, email, document, tool-result, and agent messages enter. Record which principal can submit each and what higher-privilege component consumes it.

## Produce

Return a compact component and data-flow map with evidence links, confidence, unknowns, and the next low-impact check for each important hypothesis. Distinguish public inference endpoints from internal services and code-only references. Do not enumerate nearby infrastructure solely because an AI service mentions it; scope applies to every destination. Route confirmed surfaces to ai-prompt-injection-testing, ai-agent-workflows, ai-rag-retrieval, ai-mcp-tool-trust, ai-llm-output-sinks, ai-model-supply-chain, or ai-serving-infrastructure.

