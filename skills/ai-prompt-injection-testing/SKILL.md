---
name: ai-prompt-injection-testing
description: Test direct and indirect prompt injection at an LLM application's instruction-data boundary. Use when attacker-controlled text, documents, pages, messages, or tool results can influence a privileged assistant.
---

# Prompt Injection Testing

Define the legitimate task and the attacker-controlled entry point before crafting a probe. Distinguish a direct user prompt from lower-trust content that the assistant retrieves or reads. The relevant outcome is a deviation from the authorized task or a privileged action, not merely a model repeating adversarial text.

## Probe and confirm

1. Establish a baseline with a benign canary and capture the expected answer or action.
2. Place a harmless conflicting instruction at the exact lower-trust surface: document, search result, web page, email, tool response, image text, or URL prompt parameter. Keep the payload minimal and identify the claimed authority.
3. Observe whether the assistant treats content as instructions, whether it invokes a tool, and whether a policy or application boundary blocks the action. Verify downstream state rather than trusting the assistant's narration.
4. Vary one factor at a time: placement, formatting, claimed role, conversation state, or retrieval rank. Retest with a clean session and comparable input to identify persistence or stochastic effects.

A refusal or a changed answer alone does not establish a vulnerability. Prompt leakage matters when hidden context contains sensitive data or enables a consequential follow-on path. Use harmless markers for exfiltration tests and authorized destinations only. Stop before sending real secrets to external services. Do not mass-generate jailbreak strings without a specific hypothesis and query budget.

For a chat URL that pre-fills or submits a prompt, use the existing url-prompt-injection skill to distinguish prefill from automatic execution. For document retrieval use ai-rag-retrieval; for actions use ai-agent-workflows or ai-mcp-tool-trust. Recommend reducing privileges, validating action parameters, and preserving the instruction-data boundary; a stronger system prompt alone is not sufficient evidence of a fix.


## Test inputs

For graded, multilingual, multi-turn, and combined test inputs, read [the payload catalog](references/payload-catalog.md) and choose only the entries that match the observed surface.

