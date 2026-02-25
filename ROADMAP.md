# OFC Roadmap

> Post-ACP integration — what's next for Open Floor Control

## Current State

Multi-agent coordination with real tool execution in sandboxed containers is working end-to-end, via both LLM (OpenAI-compatible) and ACP (Claude Code) agents. The core protocol (mentions, activation modes, call-stack delegation, PASS semantics) is validated. Go implementation is the primary runtime. The architecture has been refactored into a clean event-driven design with an HTTP API for external integration.

### Recent completions

- **Floor/Controller/Agent architecture** — Complete rewrite from the monolithic Coordinator into decoupled components: `Floor` (shared state: Chat + Furniture + API), `Controller` (pure-logic turn-taking: event in → decision out, fully testable with zero mocks), and `Agent` interface (LLM and ACP implementations). Legacy coordinator.go, runner.go, frontend.go removed.
- **Chat as event bus** — `Chat` is the communication fabric: thread-safe message history + typed event channel (`MessagePosted`, `StreamEvent`, `AgentFinished`, `AgentPassedEvent`, `AgentErrorEvent`) with fan-out to multiple subscribers.
- **HTTP API for external integration** — `POST /api/v1/messages` (inject messages from external systems), `GET /api/v1/messages` (read chat history as JSON), `GET /api/v1/events` (SSE stream of all chat events). The API server is always started, enabling webhooks and future web frontends.
- **Integration test infrastructure** — Full-pipeline integration tests driven entirely over HTTP: fake `testAgent` with configurable responses, minimal `testLoop` (event loop without terminal I/O), `setupTestFloor` helper. 10 integration tests covering agent triggering, mention delegation, PASS semantics, SSE streaming, multiple subscribers, external webhooks, and error handling.
- **Bubble Tea TUI frontend** — Split-layout terminal UI (`--tui`) with scrollable viewport and text input, built on charmbracelet/bubbletea. Runs alongside the existing CLI frontend via the decoupled Frontend interface.
- **ACP output routing** — ACP subprocess stderr and debug output properly routed through the frontend instead of directly to stdout/stderr, enabling clean TUI rendering.
- **Furniture system** — Shared interactive objects on the floor (task boards, etc.) that agents can interact with via tool calls. Built-in `Furniture` interface wrapped as MCP servers via go-sdk, exposed over HTTP (Echo) at `/api/v1/floors/{floor}/mcp/{name}`. LLM agents get namespaced tool injection; ACP agents get MCP pass-through. First built-in: TaskBoard with CRUD operations. Validated end-to-end with planner/coder demo.
- **SSE MCP transport for ACP agents** — API server now serves both Streamable HTTP and SSE endpoints per furniture item. Transport auto-selected from agent's MCP capabilities reported during init. Validated with Claude Code (claude-code-acp) reading/writing the shared task board via SSE MCP, writing and testing actual code.
- **External MCP servers as furniture** — OFC can spawn external MCP server subprocesses (`type: mcp` with `command`/`args` in blueprint) and expose their tools through the standard furniture pipeline. `ExternalMCP` connects as MCP client via go-sdk `CommandTransport`, discovers tools at startup, proxies calls. Validated with the Everything MCP test server (14 tools).

---

## Phase 1: Protocol Refinements

Harden the protocol foundations before building more on top.

- [ ] Port remaining Python-version features to Go (tool_context, etc.)
- [ ] Test edge cases in delegation/return flow (nested mentions, multi-agent chains)
- [ ] Validate `query_intent` structured signaling in real scenarios
- [ ] Stress-test moderator behavior with adversarial/ambiguous inputs

## Phase 2: Furniture

Expand what agents can interact with on the floor.

### 2a: MCP Servers
- [x] Integrate MCP servers as furniture items (Furniture interface → MCP wrapper → HTTP API)
- [x] Define how agents discover and use MCP-provided tools (LLM: namespaced tool calls; ACP: MCP pass-through)
- [x] SSE transport alongside Streamable HTTP (claude-code-acp requires SSE despite advertising HTTP)
- [x] Capability-based transport selection from agent init handshake
- [x] External MCP servers via command/stdio (`ExternalMCP` + go-sdk `CommandTransport`)
- [x] Validated with Everything MCP test server (14 tools discovered, tool calls work)
- [x] Validated with filesystem MCP server — LLM agent (function calls) and ACP agent (SSE) both working
- [ ] External MCP servers via URL (connect to already-running servers)
- [ ] Stdio bridge (`ofc mcp-bridge`) for ACP agents that only support stdio

### 2b: Collaboration Furniture
- [x] Task board — shared state agents can read/write/update
- [ ] Explore other shared artifacts (kanban boards, documents, knowledge bases)
- [ ] Per-agent access control at the tool/function level
- [ ] Furniture persistence (currently in-memory only)

## Phase 3: Rooms

Scoped sub-floors for private/parallel work.

- [ ] Implement room creation and lifecycle
- [ ] Agent movement between floor and rooms
- [ ] Room-scoped context (agents in a room don't pollute the main floor)
- [ ] Breakout patterns — moderator sends agents to a room for a sub-task, results return to floor

## Phase 4: Collaboration Modes

Beyond delegation — explore peer-to-peer agent dynamics.

- [ ] **Free conversation** with `[PASS]` — agents contribute when they have something to add
- [ ] **Brainstorming** — divergent thinking mode, multiple agents riffing on ideas
- [ ] **Debate/adversarial** — agents argue different positions, moderator synthesizes
- [ ] Define how moderator prompting strategies enable different modes without protocol changes

## Phase 5: Events & Async

Move beyond synchronous request-response.

- [x] Event-driven floor architecture (controller is pure event→event, no I/O)
- [x] Multiple frontend support via Frontend/StreamSink interfaces (CLI, TUI)
- [ ] Web frontend (chat UI over HTTP/WebSocket)
- [x] Event-driven activation (external triggers, webhooks) — `POST /api/v1/messages` injects messages from any source
- [x] Integration patterns — HTTP API (POST messages, GET history, SSE events) enables external system interaction
- [ ] Scheduled activation (cron-like triggers)
- [ ] Async agent work — agents that take time and report back
- [ ] Explore long-running floors that persist and respond to events over time

## Phase 6: Virtual Lab Rebuild

The capstone — rebuild the Stanford/CZ Biohub Virtual Lab pattern on OFC.

- [ ] Define specialist agents (immunologist, computational biologist, machine learning engineer, etc.)
- [ ] Blueprint for multi-round scientific discussion with structured outputs
- [ ] Validate that OFC's protocol can handle the collaboration patterns from the original paper
- [ ] Potential outreach/collaboration with the paper's authors

---

## Notes

- Phases are roughly sequential but overlap is expected — e.g., collaboration modes (4) might inform room design (3).
- Each phase should be validated by dogfooding with real projects (postmail, talkwithren, björn).
- The Go runtime is the primary target for all new work.

ofc. 🎤
