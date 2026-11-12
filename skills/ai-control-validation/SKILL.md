---
name: ai-control-validation
description: Evaluate prompt-injection defenses and AI application guardrails against defined tasks and attack paths. Use when validating a mitigation, comparing controls, or retesting an AI finding.
---

# AI Control Validation

Define what the application must permit, what it must block, and where enforcement occurs. Compare the deployed control with the finding's original trust boundary: input filtering, retrieval provenance, action authorization, output validation, or model behavior. A blocked phrase is not proof that a lower-trust source cannot cause the action through another representation.

Use a small versioned test set with benign tasks, the confirmed attack case, and nearby variants that change one factor at a time. Run against the same role, tools, corpus, model version, and configuration where feasible. Record attempts, successful harmful actions, blocked attempts, false refusals, tool calls, and state changes. With nondeterministic models, report counts and conditions rather than a single pass/fail claim; preserve query budgets and avoid tuning on the final holdout set.

For prompt injection, compare output-only defenses against controls on tool permissions and action parameters. For RAG, confirm access filters at retrieval as well as answer generation. For tool use, verify server-side authorization independently of model refusal. For output sinks, test the parser and renderer, not only the guardrail. When a mitigation changes the legitimate workflow, report the regression as well.

Use synthetic canaries and approved fixtures. Do not turn a defense evaluation into an unbounded jailbreak search. Return a measured verdict for the specific system and version, with residual paths and a reproducible regression case. Do not generalize a lab success rate to other models or production environments.


## Test inputs

For versioned benign controls and graded adversarial probes, read [the payload catalog](../ai-prompt-injection-testing/references/payload-catalog.md).

