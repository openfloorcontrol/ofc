# OFC Roadmap

> Post-ACP integration — what's next for Open Floor Control

## Current State

ACP integration with Claude Code is working end-to-end: multi-agent coordination with real tool execution in sandboxed containers. The core protocol (mentions, activation modes, call-stack delegation, PASS semantics) is validated. Go implementation is the primary runtime, with some features from the Python version still pending port (e.g., `tool_context: summary`).

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
- [ ] Integrate MCP servers as furniture items
- [ ] Define how agents discover and use MCP-provided tools
- [ ] Test with practical MCP servers (filesystem, database, APIs)

### 2b: Collaboration Furniture
- [ ] Task lists — shared state agents can read/write/update
- [ ] Explore other shared artifacts (kanban boards, documents, knowledge bases)
- [ ] Define furniture access patterns (who can read/write, scoping)

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

- [ ] Event-driven activation (external triggers, webhooks, scheduled)
- [ ] Async agent work — agents that take time and report back
- [ ] Integration patterns: how external systems interact with a running floor
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
