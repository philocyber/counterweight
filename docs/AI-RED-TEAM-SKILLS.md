# AI red teaming skills

These skills extend Counterweight's existing vulnerability assessment and web/API skills for authorized LLM-enabled systems. Each skill is a reusable testing method. On startup, Counterweight makes them visible to the task planner and worker and to the standalone pentest agent. Operators can change visibility per agent; disabled skills stay disabled after restart. Code-review agents do not receive them. The Docker build copies the skills directory.

| Skill | Focus |
| --- | --- |
| ai-red-team-method | Scope, living threat model, evidence, reporting |
| ai-target-recon | AI component and trust-boundary discovery |
| ai-prompt-injection-testing | Direct and indirect instruction/data crossing |
| ai-agent-workflows | Single and multi-agent delegation, memory, A2A |
| ai-rag-retrieval | Ingestion, retrieval, authorization, vectors |
| ai-mcp-tool-trust | Tool schema, identity, authorization, results |
| ai-model-supply-chain | Models, adapters, tokenizers, packages |
| ai-llm-output-sinks | Renderers, function calls, execution sinks |
| ai-serving-infrastructure | Model serving and MLOps boundaries |
| ai-control-validation | Guardrails, regression, legitimate-task impact |

Predictive-model evasion, training-set membership inference, differential privacy, and perturbation-budget exercises are outside this update.

## Taxonomy notes

Use the [current OWASP LLM Top 10](https://github.com/GenAI-Security-Project/GenAI-LLM-Top10) and [OWASP Agentic AI guidance](https://genai.owasp.org/2025/12/09/owasp-top-10-for-agentic-applications-the-benchmark-for-agentic-security-in-the-age-of-autonomous-ai/) when mapping a finding. [MITRE ATLAS](https://atlas.mitre.org/) and [NIST AI 100-2](https://csrc.nist.gov/pubs/ai/100/2/e2025/final) provide complementary attack and adversarial-ML taxonomies. Framework categories aid communication; they never replace target-specific proof.

The [payload catalog](../skills/ai-prompt-injection-testing/references/payload-catalog.md) supplies 32 original probes plus five controls, including Spanish, Portuguese, Crescendo-style, multi-turn, RAG, MCP, agent, and output-channel variants. Each requires an observed effect and scoped test fixtures.

