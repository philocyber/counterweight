---
name: ai-llm-output-sinks
description: Trace LLM-generated text and structured output into HTML, Markdown, SQL, code, shell, URLs, or tool calls. Use when a model response may be interpreted or executed by another component.
---

# LLM Output Sinks

Identify what consumes the model output. Separate displaying text from rendering active content, executing a query or command, choosing a URL, writing a file, and invoking a function. The security issue is the application's trust in the output at a sensitive sink; a model producing unusual text alone is insufficient.

Trace the complete path from attacker-controlled input or retrieved content through the model, parser, validation, authorization, and sink. Test a minimal benign marker in an approved environment. For rendered Markdown or HTML, observe the browser's parsed DOM and outbound requests; do not infer execution from raw source. For structured function calls, compare the model-proposed arguments with what the server accepts and what action actually occurs. For SQL, code, or shell generation, confirm the server executes the output and that existing validation does not neutralize it; avoid real data changes and command execution outside agreed test fixtures.

Record the prompt or source, exact model output, transformation, sink, and actual effect. Try to falsify the finding with escaping, content security policy, allowlists, prepared statements, parameter typing, role checks, and user confirmation. Distinguish misinformation or poor answer quality from an exploitable sink. Route conventional XSS, SQL injection, SSRF, and command injection mechanics to the corresponding existing skill once the AI-mediated path is established.

Recommend treating model output as untrusted data, using typed bounded actions, validating in the application, and rendering with safe parsers. A guardrail or refusal is a layer of defense, not a replacement for sink controls.


## Test inputs

For controlled renderer and output-channel probes, read [the payload catalog](../ai-prompt-injection-testing/references/payload-catalog.md), especially P14-P15 and P27.

