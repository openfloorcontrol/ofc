# OFC - Open Floor Control

An open protocol for multi-agent conversation.

## What is OFC?

OFC (Open Floor Control) enables multiple AI agents to collaborate in structured conversations. Think of it like a meeting room where agents take turns, mention each other with `@`, use shared tools ("furniture"), and can break out into private rooms for focused work.

## Quick Start

### Install

Build from source:
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

## Commands

During a conversation:

| Command | Description |
|---------|-------------|
| `/quit` | Exit |
| `/clear` | Clear conversation history |
| `/room #name @agent1 @agent2 [prompt]` | Create a room — agents work together, auto-return when done |
| `/room close #name` | Manually close a room |

## Architecture

```
cli/
├── cmd/           # CLI commands (run, init)
├── floor/         # Core floor engine
│   ├── floor.go           # Floor: shared state, rooms, lifecycle
│   ├── controller.go      # Controller: turn-taking logic, command handling
│   ├── chat.go            # Chat: event bus, message history, subscribers
│   ├── agent_context.go   # AgentContext: per-agent message streams
│   ├── room.go            # Room: isolated sub-conversations
│   ├── agent_llm.go       # LLM agent (OpenAI-compatible)
│   ├── agent_acp.go       # ACP agent (Claude Code, etc.)
│   ├── api.go             # HTTP API: messages, SSE events, MCP
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
| `/api/v1/floors/{f}/mcp/{name}/` | Streamable HTTP MCP for furniture |
| `/api/v1/floors/{f}/sse/{name}/` | SSE MCP for furniture |

## Examples

| Example | Description |
|---------|-------------|
| `data-analysis/` | LLM analyst + LLM coder with Docker sandbox |
| `data-analysis-acp/` | LLM analyst + Claude Code coder with filesystem MCP |
| `taskboard/` | LLM agents with shared task board furniture |
| `taskboard-acp/` | LLM planner + Claude Code coder with task board |

## Links

- Protocol spec (aspirational): [history/PROTOCOL.md](history/PROTOCOL.md)
- Blueprint reference: [BLUEPRINT.md](BLUEPRINT.md)
- Furniture architecture: [FURNITURE.md](FURNITURE.md)
- Building blocks: [BUILDING-BLOCKS.md](BUILDING-BLOCKS.md)
- Roadmap: [ROADMAP.md](ROADMAP.md)

---

ofc.
