---
name: ai-model-supply-chain
description: Review LLM model, adapter, tokenizer, dataset, MCP package, and inference dependency provenance. Use when AI artifacts or plugins are imported, updated, or deployed.
---

# AI Model Supply Chain

Begin with the authorized deployment's artifact graph: source repository, package and model registry, version or digest, publisher, download path, conversion step, loader, runtime privilege, and rollout process. Prioritize artifacts that can execute code during installation or loading, and those that can silently alter model behavior.

Inspect metadata and source without executing untrusted artifacts. Check signatures or hashes against a trusted manifest, pinned revisions, dependency and transitive package sources, model formats, custom code flags, deserialization paths, tokenizer files, adapters, and MCP server packages. A hash alone proves consistency with a value, not publisher trust. Compare a known-good version or controlled fixture to find unexpected behavior or content changes. For data poisoning, document who can submit data, where it is reviewed, how it reaches training or retrieval, and whether the effect is reproduced on a clean holdout.

Do not run a suspicious model, pickle, plugin, or repository script on the host merely to inspect it. Dynamic validation needs an explicitly authorized isolated environment and a clear rollback plan. Report artifact identity, provenance gap or mutation path, loader behavior, and observed effect; avoid labeling a theoretical unsafe format as a confirmed compromise. Recommend verifiable provenance, pinned artifacts, restricted loaders, review gates, and rollback. Use ai-rag-retrieval for runtime knowledge-base poisoning and ai-serving-infrastructure for deployment identity or secret exposure.

