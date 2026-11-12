---
name: ai-agent-workflows
description: Assess LLM agents, memory, delegation, and agent-to-agent workflows. Use when an assistant plans tool calls, shares state with other agents, or accepts agent cards and delegated results.
---

# Agent Workflow Testing

Map the real execution loop: initiator, planner or orchestrator, each agent identity, memory store, tool authority, handoff format, and approval point. Track whether instructions or asserted completion from one principal are trusted by another. Confirm any A2A or agent-card protocol from observed traffic or configuration; do not infer it from naming alone.

## Test boundaries

Use a benign marker to test whether a lower-trust message, retrieved item, memory entry, or handoff can change another agent's goal, skip a required review, widen a tool call, or influence a later session. Compare the requested workflow with actual step records. A final answer that claims a security review happened is not evidence that it did.

For memory, distinguish transient context from persisted state; test a later clean session only when persistence is in scope. For delegation, compare the sender identity and permissions with what the receiver verifies. For agent cards or registration, inspect provenance, allowed endpoints, capabilities, and whether changes after discovery are noticed. Exercise state-changing paths only with agreed test resources and Counterweight's approval gate.

Record the source, receiving agent, trust boundary, observed tool call or state transition, and negative controls. Keep unrelated traditional exploit chains with the existing web or infrastructure skills. Recommend explicit identity and authorization at every handoff, typed workflow state, independent checks for mandatory steps, and narrow tool permissions. Treat an agent's natural-language confidence or completion claim as untrusted.


## Test inputs

For controlled handoff, memory, and multi-turn probes, read [the payload catalog](../ai-prompt-injection-testing/references/payload-catalog.md), especially P18 and P22-P28.

