# OFC Roadmap

> Tracking progress on Open Floor Control

## Current State

Multi-agent coordination with real tool execution is working end-to-end, via both LLM (OpenAI-compatible) and ACP (Claude Code) agents. The core protocol (mentions, activation modes, call-stack delegation, PASS semantics) is validated. Per-agent context management, rooms, and an HTTP API for external integration are implemented. Go implementation is the primary runtime.

### Implemented

- **Floor/Controller/Agent architecture** — Decoupled event-driven design: `Floor` (shared state, rooms, lifecycle), `Controller` (pure-logic turn-taking, fully testable), `Agent` interface (LLM and ACP implementations).
- **Chat as event bus** — Thread-safe message history + typed event channel with fan-out to subscribers. `PostUserInput` for unified command routing.
- **AgentContext** — Per-agent message accumulators. Each agent has its own view of the conversation. LLM agents read from `Entries()`, ACP agents use `Delta()`/`MarkSent()` for incremental context delivery (no more duplicate messages).
- **Rooms** — Isolated sub-conversations with own Chat and Controller. `/room #name @agent1 @agent2 [prompt]` creates a room, agents work together, room auto-closes when agents go idle. Results posted back to main floor. `/room close #name` for manual close.
- **Unified event channel** — `TaggedEvent` merges main floor + room events. Frontends route by room ID.
- **Command handling** — `HandleCommand` as single parsing point for `/quit`, `/clear`, `/room`. Works identically across CLI, TUI, and future web frontends.
- **HTTP API** — `POST /api/v1/messages` (inject messages), `GET /api/v1/messages` (history), `GET /api/v1/events` (SSE stream). Always-on, enables webhooks and external integration.
- **Integration tests** — Full-pipeline tests over HTTP: agent triggering, mention delegation, PASS semantics, SSE streaming, multiple subscribers, webhooks. Room tests cover creation, isolation, agent context, close, auto-close.
- **TUI frontend** — Bubble Tea split-layout terminal UI (`--tui`) alongside the CLI frontend.
- **Furniture system** — Shared interactive objects (task boards, external MCP servers) exposed via API server. LLM agents get namespaced tool injection; ACP agents get MCP pass-through with capability-based transport selection.
- **External MCP servers** — Spawn MCP server subprocesses (`type: mcp` with `command`/`args`), discover tools, proxy calls. Validated with filesystem MCP for both LLM and ACP agents.

---

## Next Up

### Protocol Refinements

- [ ] Test edge cases in delegation/return flow (nested mentions, multi-agent chains)
- [ ] Stress-test with adversarial/ambiguous inputs
- [ ] Blueprint rooms — permanent rooms defined in YAML (agents assigned at startup)

### Furniture Expansion

- [ ] External MCP servers via URL (connect to already-running servers)
- [ ] Per-agent access control at the tool/function level
- [ ] Furniture persistence (currently in-memory only)
- [ ] Explore shared artifacts (documents, knowledge bases)

### Collaboration Modes

- [ ] Free conversation with `[PASS]` — agents contribute when they have something to add
- [ ] Brainstorming — divergent thinking mode
- [ ] Debate/adversarial — agents argue positions, user synthesizes
- [ ] Define how prompting strategies enable different modes without protocol changes

### Distribution & Packaging

- [ ] Web frontend (chat UI over HTTP/WebSocket)
- [ ] `brew install ofc` / release binaries
- [ ] Agent hub / registry for sharing agents
- [ ] Scheduled activation (cron-like triggers)
- [ ] Long-running floors that persist across sessions

---

## Notes

- Each feature validated by dogfooding with real projects.
- Go runtime is the primary target for all new work.

ofc.
