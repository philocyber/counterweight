---
name: ai-rag-retrieval
description: Assess RAG ingestion, retrieval, citations, and vector-store trust. Use when an LLM answers from external documents or a searchable knowledge base.
---

# RAG Retrieval Testing

Map the pipeline separately: who can write documents, how content is parsed and chunked, embedding and indexing, retrieval ranking and metadata filters, prompt assembly, and answer citation. A relevant retrieved chunk is not automatically authorized for the current user.

## Checks

- Access: compare the same synthetic document across two authorized test roles. Check whether tenant and document permissions survive ingestion, filtering, cache hits, and citations. Use only data planted for the assessment.
- Ingestion: insert a controlled document in an approved test corpus; verify its parsed text, metadata, chunks, and deletion behavior. Check whether hidden text, document properties, or later edits enter the index. Do not poison production knowledge bases without express authorization.
- Retrieval: compare lexical and semantic queries, near duplicates, and conflicting sources. Observe the actual retrieved chunks and scores when available. A plausible answer without a retrieved source is not proof of leakage.
- Injection: put a harmless instruction marker in an eligible chunk. Confirm whether retrieval occurs and whether the assistant promotes that lower-trust text into an instruction or tool action. Load ai-prompt-injection-testing for the behavioral test.
- Embeddings: if vectors or an embedding API are exposed, check access control and metadata first. Treat semantic similarity as approximate. Do not claim recovery of original text from a vector without independent validation; test only synthetic canaries if inversion or membership is in scope.

Report the exact document version, uploader privilege, chunk or citation ID, retrieval query, role, and observed output. Separate index poisoning, authorization failure, and prompt injection because they cross different boundaries. Recommend source-level ACLs, authenticated vector-store access, provenance, deletion propagation, and limits on what retrieved text can authorize.


## Test inputs

For controlled retrieval and citation probes, read [the payload catalog](../ai-prompt-injection-testing/references/payload-catalog.md), especially P13 and P16-P17.

