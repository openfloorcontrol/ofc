# OFC - Open Floor Control

An open protocol for multi-agent conversation.

## What is OFC?

OFC (Open Floor Control) enables multiple AI agents to collaborate in structured conversations. Think of it like a meeting room where agents take turns, mention each other with `@`, use shared tools ("furniture"), and can break out into private rooms for focused work.

## Quick Start

### Install

Via Homebrew:
```bash
brew install openfloorcontrol/tap/ofc
```

Or build from source:
```bash
cd cli && go build -o ofc .
```

### Run an Example

The `data-analysis` example uses an OpenAI-compatible endpoint (like [Ollama](https://ollama.com)):

```bash
cd examples/data-analysis
ofc run
```

Or with an initial prompt:
```bash
ofc run "Analyze the sales data"
```

The `data-analysis-acp` example uses an LLM analyst with a Claude Code coder (via [ACP](https://agentclientprotocol.com)):

```bash
cd examples/data-analysis-acp
ofc run
```

Add `--web` to open the web UI:
```bash
ofc run --web
```

### Requirements

- **LLM agents**: An OpenAI-compatible endpoint (Ollama, OpenRouter, etc.)
- **ACP agents**: The agent's ACP adapter installed (e.g. `npm i -g @anthropic-ai/claude-code-acp` for Claude Code)
- **Sandbox**: Docker (for workstation-based code execution)

## Blueprint

The core abstraction is `blueprint.yaml` — like `docker-compose.yaml` for AI teams:

```yaml
name: data-analysis
description: "Data analysis team with analyst and coder"

defaults:
  endpoint: http://localhost:11434/v1
  model: llama3

agents:
  - id: "@data"
    activation: always
    can_use_sandbox: true
    prompt: "You are @data, a senior data analyst..."

  - id: "@code"
    type: acp
    command: claude-code-acp
    activation: mention
    prompt: "You are @code, an expert programmer..."

furniture:
  - name: tasks
    type: taskboard

  - name: fs
    type: mcp
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "./workspace"]

workstations:
  - type: sandbox
    image: python:3.11-slim
    mount: ./workspace:/workspace
```

See [BLUEPRINT.md](BLUEPRINT.md) for the full reference.

## Key Concepts

- **Floor**: A workspace where agents collaborate
- **Agents**: AI participants — LLM (OpenAI-compatible) or ACP (Claude Code, etc.)
- **Furniture**: Shared tools on the floor — task boards, MCP servers, file systems
- **Workstations**: Sandboxed environments for code execution (Docker)
- **Turn-taking**: Agents use `@mentions?` to invoke others, `[PASS]` to decline
- **Rooms**: Isolated sub-conversations for focused work (`/room #name @agent1 @agent2 prompt`)
- **AgentContext**: Per-agent message streams — each agent sees their own view of the conversation

## CLI

```
ofc run [prompt]        Run a floor (optional initial prompt)
ofc init [name]         Create a new blueprint template
ofc version             Print version info
```

### Flags for `ofc run`

| Flag | Description |
|------|-------------|
| `--file`, `-f` | Blueprint file path (default: `blueprint.yaml`) |
| `--session <uuid>` | Resume a session by UUID (default: generate a new one) |
| `--debug` | Enable debug output |
| `--log <file>` | Log output to file (plain text, no colors) |
| `--tui` | Terminal UI with split layout |
| `--json` | Output events as JSONL to stdout |
| `--web` | Web UI with chat, furniture panels, inline images |
| `--port` | Web UI port (default: `8080`) |
| `--hostname <url>` | External URL for the printed web link (e.g. `https://ofc.example.com`) |
| `--db <dsn>` | Postgres DSN for session storage (overrides JSONL; falls back to `OFC_DATABASE_URL`) |

Most of these have blueprint-level defaults — see the `config:` section in [BLUEPRINT.md](BLUEPRINT.md#config). A CLI flag wins when explicitly passed; otherwise the blueprint's `config:` value is used.

### Commands during a conversation

| Command | Description |
|---------|-------------|
| `/quit` | Exit |
| `/clear` | Clear conversation history |
| `/room #name @agent1 @agent2 [prompt]` | Create a room — agents work together, auto-return when done |
| `/room close #name` | Manually close a room |

## Web UI

Launch with `ofc run --web` to open a browser-based interface:

- **Chat panel** with streaming agent responses and markdown rendering
- **Furniture sidebar** with live task board and file list panels
- **Inline images** — agents write standard markdown (`![chart](chart.png)`) and images render directly in chat
- **Responsive design** — works on desktop and mobile
- **Auth** — token-based, auto-injected for the local session

## Architecture

```
cli/
├── cmd/           # CLI commands (run, init, version)
├── floor/         # Core floor engine
│   ├── floor.go           # Floor: shared state, rooms, lifecycle
│   ├── controller.go      # Controller: turn-taking logic, command handling
│   ├── chat.go            # Chat: event bus, message history, subscribers
│   ├── agent_context.go   # AgentContext: per-agent message streams
│   ├── room.go            # Room: isolated sub-conversations
│   ├── agent_llm.go       # LLM agent (OpenAI-compatible)
│   ├── agent_acp.go       # ACP agent (Claude Code, etc.)
│   ├── api.go             # HTTP API: messages, SSE events, MCP, file serving
│   ├── cli.go             # CLI frontend
│   └── tui.go             # TUI frontend (bubbletea)
├── blueprint/     # YAML loading, agent/workstation config
├── furniture/     # Furniture interface, TaskBoard, ExternalMCP
├── sandbox/       # Docker sandbox management
└── acp/           # ACP client: session management, callbacks
```

## HTTP API

The floor runs an HTTP API server for external integration:

| Endpoint | Description |
|----------|-------------|
| `POST /api/v1/messages` | Inject a message into the floor |
| `GET /api/v1/messages` | Read message history |
| `GET /api/v1/events` | SSE stream of all chat events |
| `GET /api/v1/agents` | Floor metadata and agent list |
| `GET /api/v1/furniture` | List furniture with their tools |
| `POST /api/v1/furniture/:name/call` | Proxy a tool call to furniture |
| `GET /api/v1/file/*` | Serve files (workspace or `:furniture/path`) |
| `GET /api/v1/auth/token` | Auth token (loopback only) |
| `/api/v1/floors/{f}/mcp/{name}/` | Streamable HTTP MCP for furniture |
| `/api/v1/floors/{f}/sse/{name}/` | SSE MCP for furniture |

### SSE Event Protocol

The `GET /api/v1/events` endpoint streams Server-Sent Events as JSON. This is the integration point for building custom frontends. Each event has a `type` field:

**Message lifecycle:**

| Event | Fields | Description |
|-------|--------|-------------|
| `agent_label` | `agent_id` | Agent is about to stream — render its name label |
| `token` | `agent_id`, `token` | Streaming text token from an agent |
| `tool_call_started` | `agent_id`, `id`, `title` | Agent started a tool call |
| `tool_call_output` | `agent_id`, `id`, `output` | Incremental output from a running tool |
| `tool_call_result` | `agent_id`, `id`, `title`, `output` | Tool call completed with result |
| `agent_finished` | `agent_id` | Agent finished streaming (end of turn) |
| `message_posted` | `message` | Final message posted to chat (with `from`, `content`, `tool_interactions`) |

**Turn-taking:**

| Event | Fields | Description |
|-------|--------|-------------|
| `agent_passed` | `agent_id` | Agent declined to respond (`[PASS]`) |
| `agent_error` | `agent_id`, `error` | Agent encountered an error |

**Furniture:**

| Event | Fields | Description |
|-------|--------|-------------|
| `furniture_updated` | `name` | A furniture's state changed (refresh task boards, file lists, etc.) |

A typical agent turn produces: `agent_label` → `token`* → (`tool_call_started` → `tool_call_output`* → `tool_call_result`)* → `token`* → `agent_finished` → `message_posted`.

## Examples

| Example | Description |
|---------|-------------|
| `data-analysis/` | LLM analyst + LLM coder with Docker sandbox |
| `data-analysis-acp/` | LLM analyst + Claude Code coder with filesystem MCP |
| `taskboard/` | LLM agents with shared task board furniture |
| `taskboard-acp/` | LLM planner + Claude Code coder with task board |
| `blog/` | Single LLM coder — swap prompt files to experiment |
| `everything/` | External MCP test server demo |
| `acp-test/` | ACP agent with sandbox |
| `chaindepth/` | Delegation chain depth test |

## Links

- Blueprint reference: [BLUEPRINT.md](BLUEPRINT.md)
- Furniture architecture: [FURNITURE.md](FURNITURE.md)
- Building blocks: [BUILDING-BLOCKS.md](BUILDING-BLOCKS.md)
- Roadmap: [ROADMAP.md](ROADMAP.md)

---

ofc. 🎤
