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
  cmd/                        # Cobra CLI commands (run, init, version)
  blueprint/                  # YAML blueprint schema + loader
  floor/                      # Core orchestration engine
    controller.go             # Pure-logic state machine (event→event, no I/O)
    coordinator.go            # Wiring layer (lifecycle, agent dispatch, furniture)
    runner.go                 # LLMRunner + ACPRunner (agent execution)
    events.go                 # All event types (sealed interface)
    floor.go                  # Domain types (FloorMessage, Frame, ToolInteraction)
    frontend.go               # Frontend + StreamSink interfaces
    cli.go                    # CLI frontend (stdin/stdout)
    tui.go                    # Bubble Tea TUI frontend
    output.go                 # Output multiplexer (terminal + log file)
    api.go                    # Echo HTTP server for MCP endpoints (SSE + Streamable HTTP)
  acp/                        # Agent Client Protocol integration
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
examples/                     # Blueprint examples
  taskboard-acp/              # ACP agent (Claude Code) + taskboard + filesystem MCP
  everything/                 # External MCP test server demo
  taskboard/                  # LLM agents with shared task board
  data-analysis/              # LLM agents with Docker sandbox
  acp-test/                   # ACP agent with sandbox
  chaindepth/                 # Delegation chain depth test
```

## Architecture

**Event-driven** with clean separation:

```
User Input → Frontend.ReadInput() → Controller.HandleEvent() → Coordinator.processEvents()
                                         ↓                            ↓
                                    Pure logic:                  Dispatches to:
                                    Messages[], CallStack[]      LLMRunner or ACPRunner
                                    Turn-taking decisions        Frontend.Render()
```

**Controller** (`floor/controller.go`) is the heart — a pure function: event in, events out. No I/O, no goroutines. Fully testable (12 tests, zero mocks).

**Coordinator** (`floor/coordinator.go`) owns lifecycle: sandbox, ACP sessions, furniture, API server. Main loop reads input → HandleEvent → processEvents (recursive dispatch).

## Two Agent Paths

- **LLM agents**: Controller builds `[]llm.Message` context. LLMRunner calls OpenAI-compatible API. Furniture tools injected as function calls namespaced `{furniture}__{tool}`. Direct `Furniture.Call()`.
- **ACP agents** (e.g. Claude Code via `claude-code-acp`): Controller builds `[]acpsdk.ContentBlock` context. ACPRunner sends via `AgentSession.Prompt()` over stdio. Furniture exposed as MCP server URLs (SSE preferred). ACP agents also have built-in file read/write and terminal execution via FloorClient callbacks (separate from furniture MCP).

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

- `PROTOCOL.md` — Full OFC protocol specification
- `FURNITURE.md` — Furniture architecture (proxy principle, two agent paths, MCP transport)
- `BLUEPRINT.md` — Blueprint YAML reference
- `ROADMAP.md` — 6-phase roadmap
- `BUILDING-BLOCKS.md` — Research notes on ACP, MCP, KAOS protocol stack
