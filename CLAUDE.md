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
    floor.go                  # Floor: Chat, Furniture, Sandbox, ACP sessions, Rooms
    chat.go                   # Chat: thread-safe message store + event channel + listeners
    controller.go             # Controller: pure-logic turn-taking (Decide → Decision, no I/O)
    agent.go                  # Agent interface + dispatch
    agent_llm.go              # LLMAgent: builds context from AgentContext, calls LLM API
    agent_acp.go              # ACPAgent: sends deltas via AgentContext, ACP over stdio
    agent_context.go          # AgentContext: per-agent message accumulator (Entries/Delta/MarkSent)
    room.go                   # Room: isolated sub-conversation with own Chat + Controller
    events.go                 # ChatEvent types + TaggedEvent for unified channel
    cli.go                    # CLI frontend (stdin/stdout, unified event loop)
    tui.go                    # Bubble Tea TUI frontend
    output.go                 # Output multiplexer (terminal + log file)
    api.go                    # Echo HTTP server: MCP endpoints + Floor REST API
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
  data-analysis-acp/          # LLM analyst (fs MCP) + Claude Code coder
  data-analysis-opencode/     # LLM analyst (fs MCP) + OpenCode coder
  acp-test/                   # ACP agent with sandbox
  chaindepth/                 # Delegation chain depth test
```

## Architecture

**Event-driven** with clean separation:

```
User Input → Chat.PostUserInput() → ChatEvent → Frontend event loop
                                                      ↓
                                              Controller.Decide(chat, event) → Decision
                                                      ↓
                                              Agent.Run(ctx, floor) — goroutine
                                                      ↓
                                              Chat.Post() → next event
```

**Floor** (`floor/floor.go`) owns shared state: Chat, Furniture, Sandbox, ACP sessions, Rooms, AgentContexts.

**Controller** (`floor/controller.go`) is the heart — a pure function: event in, Decision out. No I/O, no goroutines. Fully testable.

**Agents** run as goroutines. LLMAgent builds context from AgentContext and calls the LLM API. ACPAgent sends deltas over stdio. Both post results back to Chat.

## Two Agent Paths

- **LLM agents** (`agent_llm.go`): Builds `[]llm.Message` context from `AgentContext.Entries()`. Calls OpenAI-compatible API. Furniture tools injected as function calls namespaced `{furniture}__{tool}`. Direct `Furniture.Call()`. `can_use_sandbox` controls bash tool access (requires Docker sandbox).
- **ACP agents** (`agent_acp.go`, e.g. Claude Code, OpenCode): Sends delta context via `AgentContext.Delta()` / `MarkSent()`. Communicates over stdio via `AgentSession.Prompt()`. Furniture exposed as MCP server URLs (SSE preferred). ACP agents also have built-in file read/write and terminal execution via FloorClient callbacks.

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
- `ROADMAP.md` — Project roadmap
- `BUILDING-BLOCKS.md` — Research notes on ACP, MCP, KAOS protocol stack
- `history/PROTOCOL.md` — Early protocol specification (historical)
