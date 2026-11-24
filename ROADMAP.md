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
- **JSONL output** — `--json` flag for machine-readable event output, foundation for testing and evaluation.
- **HTTP MCP auth** — `headers` field with `${VAR}` env expansion for authenticated MCP servers.
- **Homebrew distribution** — `brew install openfloorcontrol/tap/ofc` (macOS + Linux, Intel + ARM).

---

## Next Up

### Protocol Refinements

- [ ] Blueprint rooms — permanent rooms defined in YAML (agents assigned at startup)
- [ ] Test edge cases in delegation/return flow (nested mentions, multi-agent chains)
- [ ] Floor control beyond addressing — see [Floor control ideas](#floor-control-ideas-unsettled)

### Floor control ideas (unsettled)

Ideas for making turn-taking feel more like a real conversation. None is decided; they overlap and may combine.

What works today: a small model decides whom a message addresses (call / chime / nobody), the call stack returns answers to whoever asked, `default_agent` and `followup_agent` are fixed roles, `[PASS]` skips a turn. Known gap: when the user replies to two agents in one message, floor control picks one and doesn't track the other.

**The circle — who is in the conversation.** Next to the call stack (who waits for whom: an obligation), track who is part of the conversation right now (attention). Speaking or being called joins it; `[LEAVE]` ("this topic doesn't interest me, I step out") or N messages of silence leaves it, while `[PASS]` only means "nothing to say right now". Members may follow up on answers, and floor control sees the circle as context. `followup_agent` would become "always in the circle". Open: how several members get offered a turn without piling up (asking all in order made them stack unrelated questions); asking one at a time — "ask → agent doesn't pass → it adds" — then raises how agents interrupt each other: random order, or a protocol where the floor announces who is asked and others sign up to interrupt.

**Initiative — scores instead of a single answer.** Like initiative in DnD: each agent gets a score whether to speak, from modifiers, plus a roll.
- The floor control model rates relevance per agent from the topic (`{hiro: +10, maya: +0, danny: +2}`) instead of answering `chime @maya` — the model judges, the rules decide.
- Personality as a base value: a bossy agent +3, a shy one −2, maybe situational through the `turn_prompt`.
- State as modifiers: addressed, recently spoke (the circle as a number), passed, just spoke.
- With a seed the rolls are reproducible, and the eval checks the model's modifiers instead of dice results.
- Calls probably stay obligations (whoever is asked answers; the call stack stays); initiative decides the rest — chiming in, following up, who takes a message to nobody — and could replace `default_agent` and `followup_agent`.
- The numbers need workshopping.

**Actions and reactions — a mini rule set for conversation.** On its turn an agent takes an action: ask, answer, remark, step out. Others may react: "Hiro steps in before Danny can answer" reacts to a question, "Danny replies, but Hiro follows up" reacts to an answer. As in DnD, one reaction per round would limit chattiness more organically than `max_agent_turns`, and initiative decides who reacts first. Floor control becomes the DM: it reads the situation and hands out modifiers, the rules do the rest. Today's pieces map onto it: a call is an action with an obligation to answer, the call stack holds the open actions, `followup_agent` is a fixed reaction, `[PASS]` is no reaction.

### Furniture Expansion

- [ ] Per-agent access control at the tool/function level
- [ ] Furniture persistence (currently in-memory only)
- [ ] Explore shared artifacts (documents, knowledge bases)

### Collaboration Modes

- [ ] Define how prompting strategies enable different modes without protocol changes
- [ ] Free conversation, brainstorming, debate/adversarial patterns

### Testing & Evaluation

- [ ] `ofc eval` — pipe text/JSONL to an LLM with an evaluation prompt, get structured score + reasoning. The evaluation primitive.
- [ ] `ofctest` Go package — `Run()` a blueprint in-process, collect events, mechanical assertions (`AssertToolCalled`, `AssertAgentSpoke`, `AssertNoErrors`) + LLM evaluation via `Eval()`. Powers `go test` for Go developers and OFC's own integration tests.
- [ ] `ofc test` — CLI test runner reading test specs from YAML. Wraps `ofctest` for non-Go users who just have a blueprint. Mechanical assertions + eval prompts, no code required.
- [ ] Use `ofctest` for OFC's own integration tests (dogfooding).

### Distribution & Scaling

- [ ] Agent hub / registry for sharing agents
- [ ] Scheduled activation (cron-like triggers)
- [ ] Long-running floors that persist across sessions

---

## Notes

- Each feature validated by dogfooding with real projects.
- Go runtime is the primary target for all new work.

ofc. 🎤
