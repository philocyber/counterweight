---
name: ai-serving-infrastructure
description: Review the deployment and access boundaries of LLM inference services, AI pipelines, registries, and containerized tool backends. Use when an AI-enabled target exposes serving or MLOps infrastructure.
---

# AI Serving Infrastructure

Map the deployment path from artifact registry and CI to model server, API gateway, orchestrator, data store, and tool backend. Record which identities and secrets each component has and whether access is internal, public, or reachable through an agent. AI-specific infrastructure often shares ordinary web, cloud, and Kubernetes failure modes; use existing domain skills for their exploit mechanics.

Prioritize low-impact checks: authenticated versus unauthenticated model endpoints, tenant separation, metadata and status endpoints, registry read/write permissions, secret placement, workload identity, service-account scope, and network paths between inference and higher-privilege services. Verify a claimed exposure from actual reachability and role context; a configuration string or documentation example alone is insufficient. For a candidate identity chain, record each permission grant and resource boundary before attempting a step.

Do not run untrusted model artifacts, alter live deployments, consume large GPU budgets, stress inference services, or probe neighboring hosts outside scope. Destructive tests, denial-of-service, privilege changes, and container escape demonstrations require the engagement's explicit authorization and an isolated plan. Use harmless test resources to prove impact where possible.

Report the exact endpoint or configuration, principal, permission, path to the protected resource, observed result, and uncertainty. Recommend least-privilege workload identities, authenticated inference, restricted registries and loaders, secret isolation, network segmentation, and observability at the demonstrated boundary.

