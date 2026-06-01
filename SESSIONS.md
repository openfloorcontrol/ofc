# Sessions Architecture

> The three-layer model: Blueprint, Floor, Session. The Floor is a live DOM of agents and furniture; the Session is the conversation that runs against it.

## Status

Design agreed in discussion, not yet implemented. This document captures the model. Implementation proceeds in three staged PRs (see [Staging](#staging)).

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

Storage interface:

```go
type SessionStore interface {
    Create(floorID string) (sessionID string, err error)
    Append(sessionID string, ev SessionEvent) error
    Load(sessionID string) ([]SessionEvent, error)
    List(floorID string) ([]SessionMeta, error)
    Delete(sessionID string) error
}
```

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

Current → Target:

| Current                          | Target                          | Notes |
|----------------------------------|----------------------------------|-------|
| `floor.Floor` (does everything)  | `floor.Floor` (slimmer) + `floor.Session` (new) | Split |
| `floor.Floor.Chat`               | `floor.Session.Rooms["#main"]`  | Main chat becomes default room |
| `floor.Chat`                     | `floor.Room`                    | Same concept, unified type |
| `floor.Room` (current)           | `floor.Room` (unchanged concept) | Now lives under Session |
| `AgentContext`                   | Inside `floor.Session`          | Moves into Session |
| Blueprint loader                 | Sequence of mutation calls       | No separate code path |
| `acp.AgentSession`               | `acp.Subprocess` (rename)       | Free the word "Session" |

## Staging

Three PRs, each shippable independently:

1. **Introduce Session as runtime entity + Floor mutation primitives.** Pure refactor. No persistence yet, no behavior change. Floor gains `Sessions map`; blueprint loading goes through `addAgent`/`addFurniture` calls; CLI default = one anonymous Session. API surface gains `/sessions/{sid}/` prefix. All existing tests pass.

2. **Storage abstraction + JSONL impl.** Define `SessionStore` interface (event log) and `FloorStore` interface (current-state record). Memory + JSONL implementations of both. Named Floors enabled; `--session <id>` resume flag added. The log-is-context replay model lands here.

3. **Per-agent context strategies + compactions.** AgentContext gains a Strategy field. Implementations: PassThrough (current behavior), LastN, Summarize. Summarize writes `Compaction` events back to the session log. Tiny on top of (2).

Each PR is meaningful on its own and leaves the system functional.

## Future Work

Things noted during design but explicitly deferred:

- **Floor mutation event log.** v1 stores current state only. A future version could log mutations for audit/replay/duplication features (`ofc floors export`, `ofc floors duplicate`).
- **Versioned blueprints.** v1 overwrites the snapshot. Future: keep history, let sessions reference which version was active when a message was posted.
- **Drift summaries on resume.** v1 = naive resume, user is expected to explain changes. Future: per-furniture optional drift hooks injecting a "while you were away" system note.
- **Live add/remove of agents and furniture via API.** Mutation primitives exist in v1 (used by blueprint loader); v1 also supports CLI for these. REST endpoints for fine-grained mutation come later.
- **Mutation protection.** Confirmation flows or access control to prevent accidentally removing critical furniture/agents from a Floor with active sessions.
- **Multi-process Floor access.** JSONL is single-process-safe; concurrent multi-process writes need SQLite or Postgres backend.
- **Live session followers.** Web UI tailing a session in real time. Memory backend has this via event channel; JSONL/SQL need file-watching or LISTEN/NOTIFY.
- **Cross-session search.** Linear scan over JSONL works for small N; SQLite/Postgres for larger.
- **Renaming `MemoryTaskBoard`.** The built-in in-memory TaskBoard is explicitly ephemeral. Rename / README note to make the prototyping/production distinction obvious.

## Related Docs

- [FURNITURE.md](FURNITURE.md) — Furniture architecture (proxy principle, MCP transport)
- [BLUEPRINT.md](BLUEPRINT.md) — Blueprint YAML reference
- [ROADMAP.md](ROADMAP.md) — Project roadmap
