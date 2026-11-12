# Architecture

Counterweight is a single Go backend, with the Next.js console embedded through `go:embed`, plus
PostgreSQL. Agent capabilities come from the [`norma`](https://github.com/Autumn-27/norma) SDK
(`agentcore` / `tool` / `permission` / `harness` / `memory` / `transcript`). The planner/worker
engine and the two-graph exploration model come from the upstream ARTEX project (see
[PROVENANCE.md](PROVENANCE.md)).

The core is a **two-graph architecture**, with two autonomy mechanisms around it:
**process-level information exchange between workers** and a **planner's shared multi-round
todolist that keeps an attack chain stable**.

## Layers

```mermaid
flowchart TB
  subgraph FE["Frontend Next.js (embedded in the single binary via go:embed)"]
    UI["Dashboard · Chat · Tasks · Findings · Assets · Traffic · System"]
  end
  subgraph SRV["server (Go net/http)"]
    API["REST /api/*  JWT auth  SSE"]
    ENG["engine scheduling loop"]
    MGR["Manager  task/engine/store lifecycle"]
  end
  subgraph AG["agent (norma SDK)"]
    GO["goals  goal decomposition + scope extraction"]
    PL["planner  (the only intent generator)"]
    WK["worker  executor ×N"]
    MA["mainagent  human-in-the-loop"]
  end
  subgraph DB["PostgreSQL"]
    AGRAPH["asset graph  assets / companies / task_scope"]
    EGRAPH["exploration graph  exploration_nodes / anchors / activity"]
  end
  subgraph SUB["Supporting subsystems"]
    PROXY["traffic-recording proxy  MITM + CA trail"]
    GUARD["guard / intercept  tool approval gate"]
    ENR["enrich  DNS / HTTP async completion"]
    EXT["MCP · skills · memory · report"]
  end

  UI -->|HTTP| API
  API --> MGR --> ENG
  ENG --> PL
  ENG --> WK
  API --> MA
  API --> GO
  PL --> DB
  WK --> DB
  MA --> DB
  GO --> DB
  WK -->|"Bash / HTTP fully traced"| PROXY
  WK --> GUARD
  WK --> ENR
  PL -.-> EXT
  WK -.-> EXT
  MA -.-> EXT
```

| Layer | Responsibility |
| --- | --- |
| **Frontend** | Next.js static export embedded in the binary; visualizes tasks, assets, exploration and coverage, and hosts the human-in-the-loop chat |
| **server** | `net/http` routing, JWT auth and SSE; `Manager` owns the lifecycle of tasks, engines and stores |
| **engine** | One `plannerLoop` plus N worker goroutines per task; intent claiming, timeout, pause and drain |
| **agent** | goals / planner / worker / mainagent; the `ToolSet` exposes the two graphs as model tools |
| **db** | PostgreSQL (pgx) for both graphs; the schema is embedded and applied idempotently on every start |
| **support** | Recording MITM proxy, approval gate, async enrichment, MCP, skills, memory and reports |

Traffic evidence is stored separately in SQLite under the data directory, with its own storage
limits (see [configuration.md](configuration.md#traffic-storage-limits)).

## Two graphs: exploration and assets

The system separates **what the target is** from **how far it has been tested**:

- **Asset graph (global, shared)**: the single source of truth for assets across tasks. Nodes are
  `root_domain / subdomain / ip / service / app / endpoint`, each owned by a company. Parent/child
  relationships and deduplication keys are computed by the program; agents only submit raw
  observations.
- **Exploration graph (per task)**: the reasoning and progress of one task. Nodes are
  `goal / intent / fact / finding / hint`, linked by `spawns / derived_from / yields / proves` edges
  into a lineage chain that answers "which direction came from which facts, and what did it
  produce".
- **Anchors connect them**: `exploration_anchors(node_id, asset_id)` ties an intent, fact or finding
  to an asset. This drives **asset test coverage** and the **asset coverage graph**.

```mermaid
flowchart LR
  subgraph EG["Exploration graph (per task · progress chain)"]
    direction TB
    G["goal"]
    I1["intent A"]
    F1["fact"]
    I2["intent B"]
    FD["finding"]
    G -->|spawns| I1
    I1 -->|yields| F1
    F1 -->|derived_from| I2
    I2 -->|proves| FD
  end
  subgraph AGR["Asset graph (global · source of truth)"]
    direction TB
    RD["root_domain"]
    SD["subdomain"]
    SV["service"]
    EP["endpoint"]
    RD --> SD --> SV --> EP
  end
  I1 -. anchor .-> SD
  F1 -. anchor .-> SV
  I2 -. anchor .-> EP
  FD -. anchor .-> EP
```

Division of labor: the **planner** reads the exploration graph, judges the goal, and sends an
**intent** only when there is an uncovered direction. A **worker** claims one intent, runs real
tools, writes new assets, facts and findings back to both graphs, and stops.

## Engine loop

The engine is event-driven: a graph change wakes the planner, the planner sends intents, a worker
claims one, executes and writes back, and the write-back triggers the next round, until the goal is
proven (`prove_goal`).

```mermaid
sequenceDiagram
  autonumber
  participant EV as graph-change debounce
  participant P as planner
  participant FR as frontier intent queue
  participant W as worker
  participant PX as recording proxy
  participant DB as two graphs + activity

  EV-->>P: wake
  P->>DB: read state (graph overview + coverage/scope)
  P->>FR: send 0..N intents (with asset_ids)
  Note over P,FR: most wakes send 0, meaning no new direction
  W->>FR: claim one intent
  W->>DB: load the intent's assets as context
  W->>PX: run tools (shell / HTTP)
  PX-->>W: response (recorded)
  W->>DB: write back facts / assets / findings + per-step activity
  DB-->>EV: graph change
  EV-->>P: wake again
```

Most planner wake-ups send no new intent. That means there is nothing new to try, not that
something is wrong.

## Sharing observations between workers

Useful observations often appear in one worker's execution without being recorded as a formal
fact. Workers can search each other's execution traces within the same task:

- `search_all_worker_traces(q)`: keyword search across other workers' steps in this task (the
  caller's own intent is excluded); hits carry an `intent_id`.
- `list_worker_traces` / `get_worker_trace(intent_id, step_ids=[...])`: list runs, then read the
  full content of specific steps.

Each worker still executes only the one intent it claimed.

## The planner's shared todolist

Multi-step work has dependencies; sending every step in parallel causes chaos. The planner keeps a
todolist per task that persists across wake-ups. Each wake is a fresh session, so the list lets the
planner record a dependent sequence once and release the next step only when its prerequisites are
done and the facts it depends on exist.

```mermaid
flowchart TB
  subgraph TODO["shared todolist (per task · persists across wakes)"]
    direction LR
    T1["step 1  [done]"]
    T2["step 2  [in progress]"]
    T3["step 3  [blocked]"]
    T1 -.prereq met.-> T2 -.-> T3
  end
  R1["round 1 wake: send intent 1"] --> T1
  R2["round 2 (step 1 produced a fact): send intent 2"] --> T2
```

## White-box code review

A code review task clones a GitHub repository (anonymously, or with the stored GitHub token when
the anonymous clone fails, which is how private repositories are reached) into the data directory (`codereview/`),
records the exact commit, and runs the same planner/worker engine over the source with the bundled
`whitebox-code-review-methodology` skill. Depth is `light` (security-critical files only),
`balanced` (default) or `comprehensive` (all files). Findings can then be verified independently,
fixed by a separate fixer agent with an automatic patch review, proposed as a pull request with
your GitHub token, and re-checked against the merged code.
