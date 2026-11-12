---
name: ai-mcp-tool-trust
description: "Assess MCP and similar LLM tool surfaces: schemas, descriptions, identity, authorization, and tool results. Use when an assistant can discover or call external tools."
---

# MCP and Tool Trust

Confirm the actual protocol, transport, server identity, and available tools from observed configuration or traffic. MCP tools, resources, prompts, and Apps UI are distinct surfaces. Tool descriptions and returned data are lower-trust content unless the operator has explicitly approved that server and its provenance.

## Inspect and test

Inventory each tool's name, schema, side effects, caller identity, downstream credentials, and resource constraints. Compare advertised permissions with server-side enforcement using test principals and harmless canaries. Check whether a tool can read or write outside its intended task, tenant, path, or resource. A schema accepted by the model is not an authorization boundary.

With an authorized controlled server or fixture, vary a tool description or response to test whether it can redirect the assistant, shadow a trusted tool, or request unrelated tool calls. Observe actual calls and state. For mutable registries, compare what was approved at discovery with what executes later. Keep client consent and server-side authorization separate. Stop before invoking privileged or destructive operations on real resources without the engagement's approval.

When the surface is OAuth, dynamic registration, token delegation, or client metadata fetching, route to the existing mcp-auth-exploitation skill and verify its assumptions against the current MCP specification (https://modelcontextprotocol.io/specification). For generic web authorization use auth-matrix-testing. Recommend pinning trusted server identity and metadata, per-tool least privilege, parameter validation, user confirmation for consequential actions, and auditing tool calls.



## Test inputs

For controlled tool-description and tool-result probes, read [the payload catalog](../ai-prompt-injection-testing/references/payload-catalog.md), especially P12 and P19-P21.

