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

- **LLM agents**: An OpenAI-compatible endpoint (Ollama, OpenRouter, etc.), in the blueprint's `models:` or in [mf](https://github.com/mikiobraun/mf)'s model registry
- **ACP agents**: The agent's ACP adapter installed (e.g. `npm i -g @anthropic-ai/claude-code-acp` for Claude Code)
- **Sandbox**: Docker (for sandbox furniture)

## Blueprint

The core abstraction is `blueprint.yaml` — like `docker-compose.yaml` for AI teams:

```yaml
name: data-analysis
description: "Data analysis team with analyst and coder"

models:
  local:
    endpoint: http://localhost:11434/v1
    model: llama3

defaults:
  model: local       # a name from models:, else an mf address

agents:
  - id: "@data"
    activation: always
    furniture: [sandbox]
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

  - name: sandbox
    type: sandbox
    image: python:3.11-slim
    workspace: ./workspace
```

See [BLUEPRINT.md](BLUEPRINT.md) for the full reference.

## Key Concepts

- **Floor**: A workspace where agents collaborate
- **Agents**: AI participants — LLM (OpenAI-compatible) or ACP (Claude Code, etc.)
- **Furniture**: Shared tools on the floor — task boards, MCP servers, file systems, Docker sandboxes. An agent can use the furniture it lists, and only that
- **Turn-taking**: The floor controller decides which agent to activate next, either through @-mentions or via an LLM. Agents can answer `[PASS]` to decline.
- **Rooms**: Isolated sub-conversations for focused work (`/room #name @agent1 @agent2 prompt`)
- **Sessions**: resumable sessions, stored in JSONL files or a backend.
- **Images**: Users attach images to messages (web UI, `/attach`, `--attach`); agents on vision models see them
- **AgentContext**: Per-agent message streams — each agent sees their own view of the conversation

## Floor control

Without configuration, agents call each other with `@name?`, and `activation: always` agents chime in after every message. For more natural conversation flow, a separate LLM decides who speaks next. Agents can define a `turn_prompt` to further specify when they activate.

```yaml
floor_control:
  model: groq/qwen38        # a model reference, like an agent's model:
  default_agent: "@tom"     # answers what the user says to nobody in particular
  followup_agent: "@tom"    # may add to another agent's answer, or pass
  max_agent_turns: 4        # agent turns before the user gets the floor back

agents:
  - id: "@andy"
    turn_prompt: "The technical specialist. Speaks when asked about specs; stays out while Tom talks to the customer."
```

There's testing for floor control via `ofc floor-control eval cases.yaml` See [BLUEPRINT.md](BLUEPRINT.md#floor-control) and [TESTING.md](TESTING.md).

## CLI

```
ofc run [prompt]                    Run a floor (optional initial prompt)
ofc auth <furniture>                Authorize an OAuth MCP furniture (consent once; tokens are refreshed)
ofc sessions ls|show|rm             Manage stored sessions
ofc floor-control eval <cases.yaml> Check floor control against cases (--model, --runs)
ofc check ping|models|ask           Check LLM endpoints and the blueprint's agents
ofc eval                            Evaluate stdin with an LLM judge (see TESTING.md)
ofc init [name]                     Create a new blueprint template
ofc version                         Print version info
```

### Flags for `ofc run`

| Flag | Description |
|------|-------------|
| `--blueprint`, `-f` | Blueprint file path (default: `blueprint.yaml`) |
| `--config <file>` | Env file of settings and blueprint variables (see [Settings](#settings)) |
| `--session <uuid>` | Resume a session by UUID (default: generate a new one) |
| `--attach <image>` | Attach an image to the initial prompt (repeatable; CLI and `--json`) |
| `--debug` | Enable debug output |
| `--log <file>` | Log output to file (plain text, no colors) |
| `--tui` | Terminal UI with split layout |
| `--json` | Output events as JSONL to stdout |
| `--web` | Web UI with chat, furniture panels, inline images |
| `--port` | Web UI port (default: `8080`) |
| `--bind <addr>` | Address the web UI listens on (default: `127.0.0.1`; `0.0.0.0` for all interfaces) |
| `--hostname <url>` | External URL for the printed web link (e.g. `https://ofc.example.com`) |
| `--token <token>` | API token for `--web` (default: a random one) and for `--remote` |
| `--db <dsn>` | Postgres DSN for session storage (default: JSONL files) |
| `--sessions-dir <dir>` | Directory of JSONL session files (default: `~/.ofc/sessions`) |
| `--deploy-version <v>` | Version reported by `GET /api/health`, e.g. the commit of the deployed blueprint |
| `--acp-idle-timeout <d>` | Close an ACP agent's subprocess after it is idle this long (e.g. `15m`); its next turn resumes the session. `0` keeps it until the session closes |
| `--oauth-dir`, `--oauth-callback`, `--oauth-callback-port` | OAuth furniture: token directory and consent callback (see `ofc auth --help`) |
| `--remote <url>` | Run against a session on an `ofc run --web` server instead of a local floor (CLI or `--json`). The URL may carry `?token=` and `?session=`; otherwise the token comes from `--token` and the session from `--session`, or a new one is started |

### Settings

You can configure through CLI flags or environment variables, or provide a env file as a config via `--config`. For example:

```sh
# prod.env
OFC_BLUEPRINT=/srv/floors/shop/blueprint.yaml
OFC_WEB=true
OFC_PORT=8080
OFC_HOSTNAME=https://ofc.example.com
OFC_DB=postgres://ofc@localhost/ofc
DEPLOY_ENVIRONMENT=prod
```

A setting comes from, in order: the flag when passed, the environment, the config file, the flag's default. When the environment overrides a different value from the config file, ofc prints a warning naming the variable.

The config file's variables join the environment, so the same file works as a systemd `EnvironmentFile=` or a `docker --env-file`. Variables other than `OFC_*` are available to the blueprint as `${VAR}` and to its subprocesses. An `OFC_*` variable in the file must be a setting, and `OFC_CONFIG` cannot appear in it.

### Commands during a conversation

| Command | Description |
|---------|-------------|
| `/quit` | Exit |
| `/clear` | Clear conversation history |
| `/room #name @agent1 @agent2 [prompt]` | Create a room — agents work together, auto-return when done |
| `/room close #name` | Manually close a room |
| `/attach <image>` | Attach an image to your next message; `/attach` alone lists the attached ones |
| `/detach` | Drop the attached images |

## Web UI

Launch with `ofc run --web` to open a browser-based interface:

- **Chat panel** with streaming agent responses and markdown rendering
- **Furniture sidebar** with live task board and file list panels
- **Inline images** — agents write standard markdown (`![chart](chart.png)`) and images render directly in chat
- **Image attachments** — paste, drop or pick images (📎) to send with a message
- **Responsive design** — works on desktop and mobile
- **Sessions** — each tab works in its own session (`?session=<id>`); `?sessions` lists and switches them
- **Auth** — a Bearer token: `--token` if set (stable across restarts), otherwise random; the printed URL carries it as `?token=`. It opens the whole API, including every furniture's MCP endpoint (`/api/v1/floors/<floor>/mcp/<furniture>/`); each ACP agent gets its own token that opens only the MCP endpoints of the furniture it lists

Other ofc processes can work in the server's sessions with `--remote`, e.g. a scheduled prompt whose session you open in the browser later:

```bash
# crontab: OFC_TOKEN=… set above
0 7 * * * ofc run --remote https://ofc.example.com "Summarize yesterday's notes"
```

## Architecture

```
cli/
├── cmd/           # CLI commands (run, auth, sessions, floor-control, check, eval, init, version)
├── blueprint/     # YAML loading, models (blueprint or mf), agent/furniture config
├── floor/         # Core engine
│   ├── floor.go           # Floor: agents, furniture, sandbox, ACP subprocesses, sessions
│   ├── session.go         # Session: one conversation — rooms, per-agent context, controller
│   ├── session_loop.go    # The turn-taking loop
│   ├── controller.go      # Controller: call stack and turn rules (pure logic)
│   ├── addressing.go      # Whom a message addresses: call / chime / nobody
│   ├── room.go            # Room: #main and sub-rooms
│   ├── agents/            # LLM agent (OpenAI-compatible) and ACP agent (Claude Code, OpenCode, …)
│   ├── floorcontrol/      # A small model decides whom a message addresses
│   └── sessionstore/      # JSONL and Postgres session storage
├── frontend/      # CLI, TUI (bubbletea) and JSON frontends
├── api/           # HTTP API: sessions, SSE events, attachments, MCP, files
├── furniture/     # Furniture interface, TaskBoard, ExternalMCP, Sandbox
├── sandbox/       # Docker sandbox management
├── acp/           # ACP client: subprocess, callbacks, terminals
├── llm/           # OpenAI-compatible client with streaming
└── webui/         # Embedded web UI (built from web/)
```

## HTTP API

The floor runs an HTTP API server for external integration:

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/sessions` | List sessions |
| `POST /api/v1/sessions` | Start a session |
| `POST /api/v1/sessions/:id/messages` | Post a message into a session (with `attachments`) |
| `GET /api/v1/sessions/:id/messages` | Read a session's messages |
| `GET /api/v1/sessions/:id/events` | SSE stream of a session's events |
| `POST /api/v1/sessions/:id/attachments` | Upload an image (raw body, `?name=`) to attach to a message |
| `GET /api/v1/sessions/:id/attachments/:aid` | Fetch an attached image |
| `GET /api/v1/agents` | Floor metadata and agent list |
| `GET /api/v1/furniture` | List furniture with their tools |
| `POST /api/v1/furniture/:name/call` | Proxy a tool call to furniture |
| `GET /api/v1/file/*` | Serve files (workspace or `:furniture/path`) |
| `/api/v1/floors/{f}/mcp/{name}/` | Streamable HTTP MCP for furniture |
| `/api/v1/floors/{f}/sse/{name}/` | SSE MCP for furniture |
| `GET /api/health` | Health and versions (`ofc`, and `version` from `--deploy-version`) |

### SSE Event Protocol

The `GET /api/v1/sessions/:id/events` endpoint streams Server-Sent Events as JSON. This is the integration point for building custom frontends. Each event has a `type` field:

**Message lifecycle:**

| Event | Fields | Description |
|-------|--------|-------------|
| `agent_started` | `agent_id` | Agent got the floor |
| `agent_label` | `agent_id` | Agent is about to stream — render its name label |
| `thought` | `agent_id`, `token` | Streaming reasoning token from an agent |
| `token` | `agent_id`, `token` | Streaming text token from an agent |
| `tool_call_started` | `agent_id`, `id`, `title` | Agent started a tool call |
| `tool_call_output` | `agent_id`, `id`, `output` | Incremental output from a running tool |
| `tool_call_result` | `agent_id`, `id`, `title`, `output` | Tool call completed with result |
| `agent_finished` | `agent_id` | Agent finished streaming (end of turn) |
| `message_posted` | `message` | Final message posted to chat (with `from`, `content`, `tool_interactions`, `attachments`) |

**Turn-taking:**

| Event | Fields | Description |
|-------|--------|-------------|
| `address_decided` | `kind`, `agent_id`, `reason` | Whom the last message addresses (`call`, `chime`, `nobody`), with floor control's reason |
| `agent_passed` | `agent_id` | Agent declined to respond (`[PASS]`) |
| `agent_error` | `agent_id`, `error` | Agent encountered an error |
| `awaiting_input` | | The floor is the user's |

**Session:**

| Event | Fields | Description |
|-------|--------|-------------|
| `system_info` | `text` | A note from the floor (room closed, `max_agent_turns` reached, …) |
| `session_cleared` | | `/clear` emptied the conversation |
| `session_stopped` | | The session ended (`/quit`) |

**Furniture:**

| Event | Fields | Description |
|-------|--------|-------------|
| `furniture_updated` | `name` | A furniture's state changed (refresh task boards, file lists, etc.) |

A typical agent turn produces: `agent_label` → `token`*→ (`tool_call_started` → `tool_call_output`* → `tool_call_result`)*→ `token`* → `agent_finished` → `message_posted`.

## Examples

| Example | Description |
|---------|-------------|
| `data-analysis/` | LLM analyst + LLM coder with Docker sandbox |
| `data-analysis-acp/` | LLM analyst + Claude Code coder with filesystem MCP |
| `data-analysis-opencode/` | LLM analyst + OpenCode coder with filesystem MCP |
| `images/` | An agent on a vision model that looks at the images you attach |
| `taskboard/` | LLM agents with shared task board furniture |
| `taskboard-acp/` | LLM planner + Claude Code coder with task board |
| `blog/` | Single LLM coder — swap prompt files to experiment |
| `everything/` | External MCP test server demo |
| `acp-test/` | ACP agent with sandbox |
| `chaindepth/` | Delegation chain depth test |

## Links

- Blueprint reference: [BLUEPRINT.md](BLUEPRINT.md)
- Testing and evaluation: [TESTING.md](TESTING.md)
- Furniture architecture: [FURNITURE.md](FURNITURE.md)
- Building blocks: [BUILDING-BLOCKS.md](BUILDING-BLOCKS.md)
- Roadmap: [ROADMAP.md](ROADMAP.md)

---

ofc. 🎤
