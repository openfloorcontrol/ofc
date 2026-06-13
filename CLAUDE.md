# OFC (Open Floor Control)

Multi-agent conversational orchestration system in Go. Agents take turns in a shared "floor" — like a chatroom with structured turn-taking, shared furniture (task boards, filesystems), and multiple agent types (LLM, ACP).

## Build & Run

```bash
cd cli && go build -o ofc .    # binary at cli/ofc (canonical location)
go test ./...                   # run from cli/
ofc run examples/taskboard-acp/blueprint.yaml --log --debug
```

Flags: `--debug` (debug output), `--log` (write ofc.log), `--tui` (Bubble Tea UI), `--file` (blueprint path).

## Project Layout

```
cli/                          # Go module (github.com/openfloorcontrol/ofc)
  main.go                     # Entry point
  cmd/                        # Cobra CLI commands (run, sessions, init, version, eval)
  blueprint/                  # YAML blueprint schema + loader
  floor/                      # Core engine — types, interfaces, runtime
    floor.go                  # Floor: live DOM (agents, furniture, sandbox, ACP pool, sessions, store, APIServer)
    session.go                # Session: one conversation thread (rooms + AgentContexts); implements SessionView
    room.go                   # Room: messages + event channel + subscribers; #main + sub-rooms
    controller.go             # Controller: pure-logic turn-taking (Decide → Decision, no I/O)
    agent.go                  # Agent interface (Run(ctx, AgentTurn) error)
    agent_iface.go            # AgentTurn, AgentRegistry, SessionView interfaces (DI seams)
    turn.go                   # agentTurn impl — one constructed per dispatch
    agent_context.go          # AgentContext: per-agent reader over the store (Entries/Delta/MarkSent)
    chat.go                   # ChatMessage, ChatEvent variants
    events.go                 # Event variants (stream), TaggedEvent
    events_json.go            # EventJSON: ChatEvent → JSON (used by api/ and frontend/json)
    store.go                  # SessionStore interface, StoredEvent, EventFilter, SessionMeta, MessagePostedEvent
    memorystore.go            # MemoryStore (default backend; stays here to avoid NewFloor import cycle)
    api_iface.go              # APIServer interface — concrete impl lives in api/
    floor_acp.go              # ACP subprocess lifecycle (spawnACPSubprocess, buildACPMCPServers)
    furniture_observable.go   # Furniture wrapper that emits FurnitureUpdated events
    run_once.go               # RunOnce helper (used by eval/)
  floor/sessionstore/         # File/DB-backed SessionStore implementations
    jsonl.go                  # JSONLStore: append-only file + in-memory mirror, crash-recovery
  floor/agents/llm/           # LLMAgent — implements floor.Agent through floor.AgentTurn
  floor/agents/acp/           # ACPAgent — implements floor.Agent through floor.AgentTurn
  frontend/                   # CLI / TUI / JSON frontends (composition layer)
    cli.go                    # CLIFrontend: stdin/stdout, unified event loop
    tui.go                    # TUIFrontend: Bubble Tea
    json.go                   # JSONFrontend: JSONL to stdout
    eventloop.go              # ResolveEventContext, DecideAndAutoClose — shared dispatch helpers
    output.go                 # Terminal + log file multiplexer
    colors.go                 # ANSI palette, BuildColorMap
  api/                        # HTTP API server (composition layer)
    api.go                    # api.Server: /api/v1/messages, /events, /agents, /furniture, /mcp/{name}, /sse/{name}, /file/*
  acp/                        # Agent Client Protocol integration (low-level)
    session.go                # ACP agent subprocess lifecycle + handshake
    client.go                 # FloorClient: implements acpsdk.Client callbacks
    terminal.go               # TerminalManager for ACP terminal operations
  llm/                        # OpenAI-compatible API client with streaming
  furniture/                  # Shared interactive objects
    furniture.go              # Furniture interface + Tool type
    taskboard.go              # Built-in in-memory task board
    mcpwrap.go                # WrapAsMCP: Furniture → MCP Server (go-sdk)
    externalmcp.go            # ExternalMCP: spawn MCP subprocess as Furniture
  sandbox/                    # Docker container management for sandboxed execution
  eval/                       # LLM-as-judge evaluation (used by `ofc eval` + ofctest)
  ofctest/                    # Test helpers: RunFloor + assertions
examples/                     # Blueprint examples
  taskboard-acp/              # ACP agent (Claude Code) + taskboard + filesystem MCP
  everything/                 # External MCP test server demo
  taskboard/                  # LLM agents with shared task board
  data-analysis/              # LLM agents with Docker sandbox
  data-analysis-acp/          # LLM analyst (fs MCP) + Claude Code coder
  data-analysis-opencode/     # LLM analyst (fs MCP) + OpenCode coder
  acp-test/                   # ACP agent with sandbox
  chaindepth/                 # Delegation chain depth test
```

**Package boundary cheat-sheet.** `floor/` owns the engine and defines the
interfaces. `floor/sessionstore/` and `floor/agents/{llm,acp}/` are
*extensions* — they implement floor interfaces (nesting signals the
plug-in relationship). `frontend/` and `api/` are top-level
*composition* — they consume floor through its interfaces but aren't
part of it. Agents never reach into `*Floor` or `*Session`: they get a
`floor.AgentTurn` handle from the dispatch site with exactly the
capabilities they need (Entries/Delta, Stream/Status/Reply, Furniture,
Sandbox, ACPSubprocess, Debug).

## Architecture

**Event-driven** with clean separation:

```
User Input → Room.PostUserInput() → ChatEvent → Frontend event loop
                                                      ↓
                                              Controller.Decide(room, event) → Decision
                                                      ↓
                                              Agent.Run(ctx, AgentTurn) — goroutine
                                                      ↓
                                              turn.Reply() → Room.Post → next event
```

**Floor** (`floor/floor.go`) is the live DOM: agents, furniture, sandbox, ACP subprocess pool, sessions, store, APIServer. Mutated only through `AddAgent`/`RemoveAgent`/`UpdateAgent`/`AddFurniture`/`RemoveFurniture` (all serialized by a mutex).

**Session** (`floor/session.go`) is one conversation thread on a Floor — owns `Rooms`, `AgentContexts`. Implements `SessionView` so Room and AgentContext consume it through an interface, not a back-pointer.

**Controller** (`floor/controller.go`) is the turn-taking heart — a pure function: event in, Decision out. No I/O, no goroutines. Consumes an `AgentRegistry` (one-method interface, Floor satisfies it) so it's testable without a Floor.

**Agents** run as goroutines and consume `floor.AgentTurn` — a small capability handle constructed per dispatch. The turn binds the agent to its target room and gives scoped access to context (Entries/Delta), emission (Stream/Status/Reply), and resources (Furniture, Sandbox, ACPSubprocess). Agent impls never see `*Floor` or `*Session`.

## Two Agent Paths

- **LLM agents** (`floor/agents/llm/`, factory `llm.New`): Builds `[]llmsdk.Message` from `turn.Entries()`. Calls OpenAI-compatible API. Furniture tools injected as function calls namespaced `{furniture}__{tool}`; dispatched through `turn.Furniture(name).Call(...)`. `can_use_sandbox` gates the bash tool, which uses `turn.Sandbox()`.
- **ACP agents** (`floor/agents/acp/`, factory `acp.New`, e.g. Claude Code, OpenCode): Send delta context via `turn.Delta()` / `turn.MarkSent()`. Subprocess obtained via `turn.ACPSubprocess()`; prompt sent over stdio. Floor exposes furniture as MCP server URLs at startup (`floor/floor_acp.go` builds the list — SSE preferred when both transports advertised, HTTP fallback). ACP agents also have built-in file read/write and terminal execution via FloorClient callbacks.

## Furniture System

Shared objects agents interact with via tool calls. OFC always proxies (for access control + observability).

```
Furniture interface:  Name(), Tools(), Call(toolName, args)
    ↓
Built-in:  TaskBoard (in-memory CRUD)
External:  ExternalMCP (spawns MCP subprocess, discovers tools, proxies calls)
    ↓
WrapAsMCP() → MCP Server (go-sdk)
    ↓
APIServer (Echo): /api/v1/floors/{floor}/mcp/{name}/  (Streamable HTTP)
                  /api/v1/floors/{floor}/sse/{name}/   (SSE, for claude-code-acp)
    ↓
LLM agents: tools injected directly as function calls
ACP agents: get MCP server URLs in session, connect themselves
```

## Turn-Taking Protocol

1. Extract `@mentions?` from last message
2. If `@user` mentioned → pause for user input
3. If `@agent?` mentioned → push Frame(caller, callee) on CallStack, wake callee
4. No mentions → pop CallStack (return to caller)
5. Poll remaining agents with `shouldWake()` (respects activation mode + exclusion)
6. `[PASS]` → agent excluded from next poll cycle
7. Nobody wakes → back to user

## Key Dependencies

- `github.com/coder/acp-go-sdk` — ACP protocol (Claude Code integration)
- `github.com/modelcontextprotocol/go-sdk` — MCP server/client (furniture wrapping)
- `github.com/labstack/echo/v4` — HTTP server for MCP endpoints
- `github.com/spf13/cobra` — CLI framework
- `github.com/charmbracelet/bubbletea` — TUI framework
- `gopkg.in/yaml.v3` — Blueprint parsing

## Blueprint Schema

```yaml
name: my-floor
description: "..."
defaults:
  endpoint: http://localhost:11434/v1
  model: some-model
furniture:
  - name: tasks
    type: taskboard                    # built-in
  - name: fs
    type: mcp                          # external MCP subprocess
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "./workspace"]
agents:
  - id: "@planner"
    activation: always                 # always wakes when polled
    furniture: [tasks]                 # can access task board
    prompt: |
      You are @planner...
  - id: "@coder"
    type: acp                          # ACP agent (Claude Code)
    command: claude-code-acp           # subprocess to spawn
    activation: mention                # only wakes when @mentioned
    furniture: [tasks, fs]             # can access task board + filesystem
    prompt: |
      You are @coder...
```

## Design Docs

- `FURNITURE.md` — Furniture architecture (proxy principle, two agent paths, MCP transport)
- `BLUEPRINT.md` — Blueprint YAML reference
- `TESTING.md` — Testing guide (`--json`, `ofc eval`, `ofctest` package)
- `ROADMAP.md` — Project roadmap
- `BUILDING-BLOCKS.md` — Research notes on ACP, MCP, KAOS protocol stack
- `history/PROTOCOL.md` — Early protocol specification (historical)
