# Sessions Architecture

> The three-layer model: Blueprint, Floor, Session. The Floor is a live DOM of agents and furniture; the Session is the conversation that runs against it.

## Status

Design agreed in discussion. Implementation in progress — see [Staging](#staging) for the step-by-step plan and which steps have landed.

Phase 1 (structural refactor) is complete: Session is its own runtime entity, Floor is a live DOM with mutation primitives, Chat and Room are unified. Phase 2 (single-process persistence) is partially complete: SessionStore + MemoryStore + JSONLStore land event persistence; UUIDs + `ofc sessions` give the CLI workflow; SessionMeta + resume warnings give hygiene. FloorStore (Step 6) closes Phase 2. Phase 3 then shifts the primary surface from `ofc run` to `ofc serve` — REST mutation API, SQL-backed stores, web UI as the main consumer. That's what the architecture has been preparing for; the CLI keeps working but becomes a thin client over the REST API rather than its own design surface. Phase 4 (per-agent context strategies) follows and plugs into the existing AgentContext / AgentTurn regardless of which surface is driving.

## The Three Layers

OFC's current `Floor` struct conflates three distinct concepts. This document separates them.

```
Blueprint                              (design — YAML, immutable)
   │
   ├─→ Floor                           (instance — live DOM of agents + furniture)        N per Blueprint
   │      │
   │      ├─→ Session                  (conversation — log, agent memory, rooms)         N per Floor
   │      │      │
   │      │      ├─→ Room "#main"      (default, auto-created)
   │      │      └─→ Room "#analysis"  (spawned subconversation)
   │      │
   │      └─→ Session  ...             (multiple sessions share the Floor)
   │
   └─→ Floor  ...                      (different deployment, different furniture instances)
```

The metaphor split: **Floor is the world** — what's out there, shared, mutable, current-state. **Session is the memory** — what was said, per-conversation, historical, append-only. LLMs and users *work on* a Floor; they *have* a Session.

### Blueprint

The YAML file. Specification of what a Floor should look like: agents, furniture types, defaults. Immutable from OFC's perspective — edit and reload to change. 1:N with Floors. Same blueprint can spawn many floors.

### Floor

A live, mutable instance. Holds the current set of agents, furniture instances, sandbox/workspace configuration. Long-lived: exists even when no one is talking to it. Multiple sessions on the same Floor **share furniture** — two users on the same Floor see the same taskboard, same workspace files. That sharing is the point.

### Session

One conversation thread on a Floor. Owns the conversation log (rooms with messages), per-agent memory (AgentContexts), and the room structure. Lighter than Floor: doesn't snapshot the Floor's state. Resumable.

### Room

Sub-structure inside a Session. Each session has at least `#main` (auto-created). Rooms isolate sub-conversations (subagent-style) so they don't pollute the main context. AgentContext spans the whole session but tracks which room each message came from.

In code, `#main` is just a room — eliminates the current asymmetry between "the main chat" and "rooms."

### What each layer owns

| Concern                    | Blueprint | Floor       | Session     |
|----------------------------|-----------|-------------|-------------|
| Agent definitions          | ✓         |             |             |
| Furniture types/config     | ✓         |             |             |
| Live agent set             |           | ✓           |             |
| Live furniture instances   |           | ✓           |             |
| Workspace dir              |           | ✓           |             |
| Sandbox (Docker)           |           | ✓           |             |
| MCP/ACP subprocess pool    |           | ✓           |             |
| Conversation log (rooms)   |           |             | ✓           |
| AgentContexts              |           |             | ✓           |
| Room lifecycle             |           |             | ✓           |
| ACP per-conv subprocess    |           |             | ✓ (Floor manages pool) |

## Floor as Live DOM

Floor is not a passive container that gets constructed from a blueprint and then sits still. It's a **mutable structure** — like a DOM tree — that changes over its lifetime through a mutation API.

### Mutation primitives

```
addAgent(spec) / removeAgent(id) / updateAgent(id, patch)
addFurniture(spec) / removeFurniture(name) / updateFurniture(name, patch)
setBlueprintMeta(patch)         // name, description, defaults
```

These are the only ways to change a Floor. Three independent surfaces invoke them:

- **Blueprint loader** — traverses YAML, emits a sequence of `add*` calls against an empty Floor. There is no separate "blueprint loading" code path; loading is just the first mutation sequence.
- **CLI** — `ofc floors update X --from-file blueprint.yaml` (reconciliation: diff current vs file, apply mutations) or direct commands like `ofc floors agent prompt X @hiro <text>`.
- **REST API / web UI** — `PATCH /api/v1/floors/X/agents/@hiro`, etc.

All paths converge on the same primitives. A web UI is just a DOM inspector for the Floor.

### Lifecycle side effects

Each mutation has runtime consequences. Implementation handles them inside the mutation:

- `addFurniture` → spawn subprocess (for MCP), register MCP routes, notify agents
- `removeFurniture` → terminate subprocess, deregister routes, in-flight tool calls fail cleanly
- `addAgent` → create AgentContext for every active Session on this Floor
- `removeAgent` → stop polling in controller, preserve AgentContexts (audit), terminate ACP subprocess if any
- `updateAgent` (prompt/model/temp) → next agent turn picks up new value (free hot-reload — these are read fresh on every LLM call)

Mutations are serialized by a single mutation goroutine. Concurrent mutation requests queue; each completes (with all side effects) before the next runs.

### Graceful degradation across sessions

If a Floor changes mid-session — agent removed, furniture removed — active sessions degrade gracefully:

- Removed furniture → next tool call gets a clean "doesn't exist" error; the LLM can react.
- Removed agent → next poll cycle doesn't include it; existing AgentContext is preserved for audit but inactive.
- Updated prompt → next agent turn uses the new prompt.

The world moved on while the session was working; the session catches up at the next turn boundary. This matches how Claude Code behaves when files change on disk during a conversation.

## Sessions as Event Logs

A Session is an **append-only log of events**. Conversational state is the log; derived state (AgentContexts, room state) is rebuilt by walking the log.

### Event types (rough)

```
SessionStarted
RoomCreated / RoomClosed
MessagePosted          (room, from, content, tool_interactions, thought_signatures)
AgentJoinedRoom / AgentLeftRoom
Compaction             (per-agent — see below)
SessionEnded           (soft — sessions can resume after this)
```

Event payloads must capture **everything the LLM needs to reproduce its prior state**: tool calls, tool results, thought signatures (Gemini), cached prompt markers, anything else the LLM API round-trips. The conversation log is richer than today's `ChatMessage` — closer to the LLM API's message format with all structured bits intact.

### The log IS the context

A crucial insight: **replaying a session does not re-execute LLM turns.** The log already contains the LLM's outputs from prior turns. Resuming a session is just:

1. Read events from the log
2. Deserialize into `[]llm.Message`
3. Send to LLM as context
4. Continue

There is no separate "resume code path." Resume is the same operation as continuing — the first turn after resume reads the same log a continued turn would have read. Tokens are burned exactly once per turn, ever.

### Compactions as events

Compaction is a first-class event type, per-agent:

```json
{
  "type": "compaction",
  "agent_id": "@hiro",
  "covers_up_to_seq": 142,
  "summary": "User asked about EF lenses, you recommended the 50/1.8, prices discussed."
}
```

When agent `@hiro` builds context for the next turn:

```
context = [latest compaction.summary for @hiro] + [events after that compaction]
```

If no compaction yet, read from session start. Multiple agents in the same session compact independently — they share the underlying event log but read it through their own "last compaction marker" lens.

The log keeps the original messages (audit, debugging, the ability to regenerate the compaction with a different prompt). They just stop being sent to the LLM.

This gives strategies a clean home: a `Summarize` strategy is "when log gets long, generate a summary, write a Compaction event, future reads start from there." Durable, resumable, simple.

## Persistence Model

The two layers have different shapes, so different abstractions.

### Floor: value-shaped

Floor state is the **current value** — what agents/furniture/configs exist *now*. Mutations overwrite previous state; no historical record needed (v1).

Storage: a single record per Floor, atomically rewritten on each mutation.

- File backend: `~/.ofc/floors/<id>/floor.json` (atomic write via tmp + rename)
- DB backend: single row per Floor, JSON column or structured columns (same idea, different store)

No event log for Floor mutations in v1. The current snapshot is enough. Audit history is a v3 nice-to-have; users wanting history can roll their own (git the blueprint files, or record API calls externally).

### Session: log-shaped

Session state is **append-only events**. Derived state (AgentContexts, room structure) is rebuilt on load by walking the log.

Storage interface (as shipped):

```go
type SessionStore interface {
    // Event log
    Append(opts AppendOpts) (StoredEvent, error)
    Read(sessionID string, filter EventFilter) ([]StoredEvent, error)
    ReadForAgent(sessionID, agentID string, filter EventFilter) ([]StoredEvent, error)
    Clear(sessionID string, filter EventFilter) error

    // Per-session metadata (cwd, blueprint info, ofc version, created_at)
    SetMeta(sessionID string, meta SessionMeta) error
    GetMeta(sessionID string) (SessionMeta, error)
}
```

The event log uses two logical sets per session: an ordered event log (`Read`) and a per-agent visibility join (`ReadForAgent`). In SQL this naturally maps to two tables (`session_events` + `agent_event_refs`); in memory it's a slice + map; in JSONL it's a stream of typed records.

`SessionMeta` is a separate concern stored alongside but distinct from events — in SQL it'd be a `session_meta` table joined by sessionID.

Backends:

- **Memory** — in-RAM slice. Current behavior. Tests and ephemeral runs.
- **JSONL** — one file per session, `~/.ofc/floors/<id>/sessions/<sid>.jsonl`. Append on every event. Load = read whole file. Trivial. The original ask.
- **SQLite** — `events` table `(session_id, seq_no, ev_type, payload)`. Append = INSERT. Load = SELECT ORDER BY seq_no. Bonus: cross-session queries.
- **Postgres** — same as SQLite but networked. Multi-process safe.

### Furniture: not OFC's problem

OFC does not persist furniture state. Furniture is persisted by its backing service:

- JIRA-backed task board → JIRA persists
- GitHub-issue MCP → GitHub persists
- Filesystem MCP → the filesystem persists
- SQL-backed taskboard MCP → its DB persists
- In-memory built-in TaskBoard → ephemeral by design, lost on restart

OFC's job is **identifying which instance**: this Floor's `tasks` furniture is configured to point at JIRA project X. That configuration lives in the Floor record. The state lives in JIRA.

This dissolves an entire layer of complexity from the persistence story and makes the k8s scenario tractable: nothing in OFC needs a filesystem to function in production. File-based storage is one convenient deployment for local dev.

### On-disk layout (file backends)

```
~/.ofc/
└── floors/
    └── <floor-id>/
        ├── floor.json             (current state — agents, furniture, meta)
        ├── blueprint.yaml         (initial snapshot, for reference)
        ├── workspace/             (if applicable, already on disk)
        └── sessions/
            ├── <sid-1>.jsonl      (event log)
            └── <sid-2>.jsonl
```

Self-contained: copy the directory and the Floor is portable. Each session is one file — easy to archive, inspect with `cat`, share.

## Anonymous vs Named Floors

OFC supports two Floor lifecycle modes:

**Anonymous Floor** (`ofc run blueprint.yaml`):
- Created in-memory, no persistence
- Re-reads the blueprint file on every invocation
- Dies with the process
- Preserves current dev iteration workflow exactly

**Named Floor** (`ofc floors create blueprint.yaml --name X`, `ofc run --floor X`):
- Persisted to storage at creation
- Stored snapshot is source of truth after creation; the original file is just a template
- Survives across process restarts
- Mutable via CLI/API

The split keeps fast iteration cheap and product-mode deployments principled. You opt into the heavier model when you're past prototyping.

## Updating a Named Floor

After creation, the Floor's stored state is canonical. The original YAML file and the Floor are unlinked. Two equivalent mutation paths:

- **CLI**: `ofc floors update X --from-file blueprint.yaml` — reconciles file against current Floor state, emits mutations
- **API**: `PUT /api/v1/floors/X/blueprint` or finer-grained `PATCH /api/v1/floors/X/agents/@hiro`

### Hot-reload vs restart

Not all mutations are equally hot-swappable:

| Change                          | Effect on active sessions                          |
|---------------------------------|----------------------------------------------------|
| Prompt text edit                | Next agent turn uses new prompt. Hot.              |
| Temperature, model, endpoint    | Next agent turn uses new value. Hot.               |
| Add new agent                   | Available next turn. AgentContext created empty.   |
| Remove agent                    | Stops being polled. AgentContext preserved.        |
| Add furniture                   | Subprocess spawned, MCP routes registered.         |
| Remove furniture                | Subprocess terminated, in-flight calls fail clean. |
| Change furniture command/args   | Old subprocess down, new one up. In-flight fails.  |

Implementation: prompts/model/temp are read fresh on every LLM call (they're part of building the request from current Floor state), so hot-reload is free. Furniture changes involve subprocess lifecycle and are heavier but still handled by the mutation primitives.

CLI `update --from-file` could refuse to apply heavier changes without `--restart`. To be decided when implemented.

## Multi-User

Multi-user composes on top of this model:

- Users own Sessions.
- Floor membership controls who can start a Session on a given Floor.
- Multiple users on the same Floor = collaboration via shared furniture.
- Multiple users isolated = different Floors (same Blueprint, separate state).

Auth and ownership are a layer above this hierarchy, not woven into it.

## Code Mapping

Current → Target. `[x]` marks transformations already shipped.

| Current                          | Target                          | Status / Notes |
|----------------------------------|----------------------------------|----------------|
| `floor.Floor` (does everything)  | `floor.Floor` (slimmer) + `floor.Session` (new) | `[x]` Split shipped (step 1) |
| `Agent.Run(ctx, *Floor)`         | `Agent.Run(ctx, *Session)`      | `[x]` Shipped |
| `Floor.ViewForRoom`              | `Session.ForRoom`               | `[x]` Shipped |
| `AgentContext`                   | Inside `floor.Session`          | `[x]` Moved into Session |
| `acp.AgentSession`               | `acp.Subprocess` (rename)       | `[x]` Renamed |
| `floor.Session.Chat`             | `floor.Session.MainRoom` (= `Rooms["#main"]`)  | `[x]` Shipped (step 3) |
| `floor.Chat`                     | `floor.Room`                    | `[x]` Unified into single type (step 3) |
| Blueprint loader                 | Sequence of mutation calls       | `[x]` Applied via AddAgent/AddFurniture (step 2) |
| `Controller(*Blueprint)`         | `NewController(*Floor)`         | `[x]` Shipped (step 2) |
| `Floor.Blueprint.Agents`         | `Floor.Agents` (live, mutable)  | `[x]` Shipped (step 2) |

## Staging

Implementation broken into discrete steps. Each step is shippable on its own and leaves the system functional.

### Phase 1 — Structural refactor (no new features)

Pure plumbing. No persistence, no behavior change. Establishes the layering the rest of the work builds on.

- [x] **Step 1 — Extract `floor.Session` as runtime entity.**
  Splits per-conversation state (Chat, Rooms, AgentContexts, agentRoom) out of Floor. Floor gains `Sessions` map with a single `"default"` entry. `Agent.Run(ctx, *Floor)` becomes `Agent.Run(ctx, *Session)`. `Session.ForRoom` replaces `Floor.ViewForRoom`. `HandleCommand`/`TryAutoCloseRoom` take `*Session`. `acp.AgentSession` renamed to `acp.Subprocess` to free the word.
  *Landed:* commits `bebd8ba`, `3f9d0ef`, `404bd0b`. Plus the design doc itself at `35b84a6`.

- [x] **Step 2 — Floor mutation primitives + blueprint-as-mutations.**
  Added `AddAgent`, `RemoveAgent`, `UpdateAgent`, `AddFurniture`, `RemoveFurniture`, `SetBlueprintMeta` methods on Floor. Floor gained a live mutable `Agents []blueprint.Agent` slice — Blueprint is now strictly the loaded YAML template. Blueprint loading is no longer a separate code path: `NewFloor` applies LLM agents immediately; `Start()` brings up API/sandbox and then calls `AddFurniture` / `AddAgent` for infra-dependent pieces. Controller switches from `*Blueprint` to `*Floor`; room controllers use `NewControllerForRoom` with an `AllowedIDs` filter so they see the live floor agent set restricted to room members. Mutations serialized by `sync.Mutex` (TODO: upgrade to a single-writer queue when a REST mutation API arrives).
  *Landed:* commit `21f1e18`.

- [x] **Step 3 — Unify `Chat` and `Room` into a single type.**
  The `Chat` struct is gone — its fields/methods (Post, History, Subscribe, AddListener, etc.) moved into the unified `Room` type, which also has an ID. Session holds `Rooms map[string]*Room` with `#main` (constant `MainRoomID`) always present. `Session.MainRoom *Room` is a convenience pointer. Two constructors: `NewRoom(id)` for basic rooms (#main, test fixtures), `NewSubRoom(id, creator, agentIDs, prompt, floor)` for sub-rooms with membership/controller/context. Lifecycle methods split: `Close()` closes the event channel, `CloseWithSummary(summary)` marks closed and closes.
  *Landed:* commit `5394125`.

### Phase 2 — Persistence

Now that Session is a concrete runtime entity, give it a place to live across restarts.

- [x] **Step 4 — Storage abstraction.**
  Added `SessionStore` interface + `MemoryStore` implementation. Two logical sets per session: an ordered event log (`Read`) and a per-agent visibility join (`ReadForAgent`) — maps naturally to two tables in SQL, one slice + map in memory, or two streams in JSONL. Room/AgentContext route through the store: `Room.Post` calls `Store.Append`; `Room.History` calls `Store.Read`; `AgentContext.Entries`/`Delta` call `Store.ReadForAgent`. `AppendSystem` writes private (per-agent only) events. The `MessageListener` interface is gone — visibility is computed at Post time from session room-membership. `FloorStore` deferred to step 6 (it's value-shaped, smaller scope).
  *Landed:* commit `0b6c64f`.

- [x] **Step 5 — JSONL backend for `SessionStore`.**
  `JSONLStore` writes one record per line. Three record kinds: `event`, `ref`, `clear` (plus `meta` added in step 5c). Reads go to an in-memory mirror (MemoryStore) for speed; the file is write-only at runtime. Append writes event + N ref lines and fsyncs. Load replays the file into the mirror, preserving original Seq + Time. Crash recovery tolerates a truncated final line. CLI flag `--session-log <path>` overrides Floor.Store; pointing two runs at the same file resumes (seq numbers continue monotonically across invocations). The "log IS the context" model is real now — replay deserializes the log into runtime structures without re-executing agent turns.
  *Landed:* commit `fed20df`.

- [x] **Step 5b — UUIDs by default + `ofc sessions` commands.**
  Every `ofc run` generates a UUID and persists to `~/.ofc/sessions/<uuid>.jsonl` (overridable via `$OFC_SESSIONS_DIR`). Flags: `--session <uuid>` to resume, `--session-log <path>` for explicit-path override. New subcommands: `ofc sessions ls/show/rm`. Internal session_id stays `"default"` per file (file-as-session-container). UUID printed at startup so users can grep / copy. On resume, the last "turn" (most recent @user message + responses) is replayed to give the user context.
  *Landed:* commits `87a0c8f`, `8bc482c`, `f86785b`.

- [x] **Step 5c — `SessionMeta` + resume drift warnings.**
  Added `SessionMeta` to the store (cwd, blueprint path, blueprint name, blueprint file sha256, ofc version, created_at). `SetMeta`/`GetMeta` methods on the interface; new "meta" record kind in JSONL. Written on session creation, read on resume to warn about cwd / blueprint mismatches. Warnings are advisory only — deliberately tweaking a prompt between runs is a normal workflow, so we don't block. `ofc sessions ls` enriched with blueprint name + cwd columns; `ofc sessions show` prints a meta block before the transcript. Maps to a separate `session_meta` table in SQL backends.
  *Landed:* commit `207897a`.

- [ ] **Step 6 — File backend for `FloorStore`.**
  `floor.json` per named floor at `~/.ofc/floors/<id>/floor.json`. Atomic write (tmp + rename) on each mutation. Blueprint snapshot stored alongside. Single-process safe; multi-process safety is Phase 3's concern.

### Phase 3 — API and multi-process server

Shifts the primary surface from one-shot CLI invocations to a long-running daemon. The CLI keeps working but becomes a thin client over the REST API rather than its own design surface. Multi-process by nature — multiple HTTP requests handled concurrently, possibly multiple processes behind a load balancer — which is what promotes SQL-backed storage from a nice-to-have into a requirement: JSONL is single-process safe by design and won't cover this.

- [ ] **Step 7 — `ofc serve` + REST mutation API.**
  Long-running HTTP daemon. The mutation primitives from Phase 1 (AddAgent, AddFurniture, UpdateAgent, ...) become endpoints: `PATCH /api/v1/floors/X/agents/@hiro`, `POST /api/v1/floors`, etc. The existing API server already serves furniture MCP routes and session reads; Step 7 adds the write side and the daemon lifecycle.

- [ ] **Step 8 — SQL-backed `SessionStore` and `FloorStore`.**
  Required once multiple processes can write to the same store. `SessionStore` maps to `session_events`, `agent_event_refs`, `session_meta` tables (the design the interface was built around). `FloorStore` is one row per Floor with a JSON column or structured columns. SQLite for embedded deployments, Postgres for networked. JSONL and the file `FloorStore` keep working for local single-process development — backends sit behind the same interfaces.

- [ ] **Step 9 — Web UI: Floor switcher + Session switcher.**
  `web/dist` already exists as a single-Floor view; Step 9 gives it multi-Floor and multi-Session navigation appropriate for a daemon serving more than one conversation at a time.

- [ ] **Step 10 — Live session followers.**
  Web clients tail an active session in real time. MemoryStore already does this through its event channel; SQL-backed needs LISTEN/NOTIFY (Postgres) or a polling/eventfd hybrid (SQLite). API surface is SSE on an existing-or-new endpoint per session.

### Phase 4 — Per-agent context strategies

The original reason this stack exists. With Sessions persistent and the log as truth, strategies become a small layer on top. AgentContext + AgentTurn already give a clean seam — strategies plug in regardless of whether `ofc run` or `ofc serve` is driving.

- [ ] **Step 11 — `ContextStrategy` interface on AgentContext.**
  Implementations: `PassThrough` (current behavior, default), `LastN(20)`. AgentContext gains a Strategy field; LLMAgent builds messages by calling strategy.Build(ctx).

- [ ] **Step 12 — `Compaction` event type + `Summarize` strategy.**
  Event recorded per-agent, durably. Agent's context = "latest compaction summary + events after the compaction marker." Summarize triggers when log exceeds a threshold; writes a Compaction event with an LLM-generated summary. Original messages stay in the log for audit; agent simply stops seeing them.

### Beyond v1

Not on the immediate path, but the architecture should accommodate without rework:

- [ ] Named-floor CLI as a thin REST client (`ofc floors create/ls`, `ofc run --floor X`) — once the REST API exists in Phase 3, these become thin wrappers over it rather than direct file manipulation.
- [ ] Update-from-file reconciliation (`ofc floors update X --from-file blueprint.yaml`) — diffs the file against current Floor state and emits API mutations.
- [ ] Multi-user auth: Session ownership, Floor membership.
- [ ] Versioned blueprints / Floor mutation history.
- [ ] Per-furniture drift notes on resume (richer than the Step 5c config-level warnings).

## Future Work

Things noted during design but explicitly deferred. The bigger items (REST mutation API, SQL backends, web UI live followers, named-floor CLI) moved into Phase 3 and Beyond v1; what remains here are smaller design notes.

- **Floor mutation event log.** v1 stores current state only. A future version could log mutations for audit/replay/duplication features (`ofc floors export`, `ofc floors duplicate`).
- **Mutation protection.** Confirmation flows or access control to prevent accidentally removing critical furniture/agents from a Floor with active sessions.
- **Cross-session search.** Linear scan over JSONL works for small N; the SQL backend (Phase 3 Step 8) makes larger-scale search easy as a follow-on.
- **Renaming `MemoryTaskBoard`.** The built-in in-memory TaskBoard is explicitly ephemeral. Rename / README note to make the prototyping/production distinction obvious.

## Related Docs

- [FURNITURE.md](FURNITURE.md) — Furniture architecture (proxy principle, MCP transport)
- [BLUEPRINT.md](BLUEPRINT.md) — Blueprint YAML reference
- [ROADMAP.md](ROADMAP.md) — Project roadmap
