# OFC Roadmap

> Tracking progress on Open Floor Control

## Current State

Multi-agent coordination is working end-to-end with LLM (OpenAI-compatible) and ACP (Claude Code) agents. The core protocol (mentions, activation modes, call-stack delegation, PASS semantics) is validated. A web UI provides a browser-based chat interface with furniture panels, inline image rendering, and responsive design. OFC is distributed via Homebrew (`brew install openfloorcontrol/tap/ofc`).

### Implemented

- **Floor/Controller/Agent architecture** — Decoupled event-driven design: `Floor` (shared state, rooms, lifecycle), `Controller` (pure-logic turn-taking, fully testable), `Agent` interface (LLM and ACP implementations).
- **Chat as event bus** — Thread-safe message history + typed event channel with fan-out to subscribers. `PostUserInput` for unified command routing.
- **AgentContext** — Per-agent message accumulators. LLM agents read from `Entries()`, ACP agents use `Delta()`/`MarkSent()` for incremental context delivery.
- **Rooms** — Isolated sub-conversations with own Chat and Controller. Auto-close when agents go idle, results posted back to main floor.
- **Unified event channel** — `TaggedEvent` merges main floor + room events. Frontends route by room ID.
- **Command handling** — `HandleCommand` as single parsing point for `/quit`, `/clear`, `/room`. Works across CLI, TUI, and web.
- **HTTP API** — Messages, SSE events, agent metadata, furniture proxy, file serving. Always-on, enables webhooks and external integration.
- **CLI frontend** — Terminal-based interaction with streaming, tool call display, thinking indicators.
- **TUI frontend** — Bubble Tea split-layout terminal UI (`--tui`).
- **Web frontend** — Vue + Tailwind chat UI (`--web`) with SSE-driven streaming, furniture sidebar (task boards, file lists), inline image rendering from agent markdown, responsive design, token-based auth.
- **Furniture system** — Shared interactive objects (task boards, external MCP servers) exposed via API server. LLM agents get namespaced tool injection; ACP agents get MCP pass-through with capability-based transport selection.
- **External MCP servers** — Connect to MCP servers via subprocess (`command`/`args`) or HTTP (`url`). Tool discovery, call proxying, binary file serving via `FileReader` interface.
- **File serving** — Unified `/api/v1/file/*` endpoint serving workspace files and furniture-qualified paths with MIME detection.
- **Integration tests** — Full-pipeline tests over HTTP: agent triggering, mention delegation, PASS semantics, SSE streaming, rooms, furniture proxy.
- **Homebrew distribution** — `brew install openfloorcontrol/tap/ofc` (macOS + Linux, Intel + ARM).

---

## Next Up

### Protocol Refinements

- [ ] Blueprint rooms — permanent rooms defined in YAML (agents assigned at startup)
- [ ] Test edge cases in delegation/return flow (nested mentions, multi-agent chains)

### Furniture Expansion

- [ ] Per-agent access control at the tool/function level
- [ ] Furniture persistence (currently in-memory only)
- [ ] Explore shared artifacts (documents, knowledge bases)

### Collaboration Modes

- [ ] Define how prompting strategies enable different modes without protocol changes
- [ ] Free conversation, brainstorming, debate/adversarial patterns

### Distribution & Scaling

- [ ] Agent hub / registry for sharing agents
- [ ] Scheduled activation (cron-like triggers)
- [ ] Long-running floors that persist across sessions

---

## Notes

- Each feature validated by dogfooding with real projects.
- Go runtime is the primary target for all new work.

ofc. 🎤
