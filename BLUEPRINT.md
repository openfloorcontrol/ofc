# Blueprint Reference

A blueprint is a YAML file that defines a floor — the agents, their configuration, and the workstations (tools) available to them. Think of it as `docker-compose.yaml` for AI teams.

## Quick start

```bash
ofc init my-floor    # creates blueprint.yaml
ofc run              # runs it
ofc run "Do the thing"  # runs with an initial prompt
```

## Structure

```yaml
name: my-floor
description: "What this floor does"

defaults:
  endpoint: http://localhost:11434/v1
  model: llama3

agents:
  - id: "@assistant"
    # ...

workstations:
  - type: sandbox
    # ...
```

## Top-level fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Floor name, shown in the header |
| `description` | no | Short description of the floor |
| `defaults` | no | Default `endpoint` and `model` for all agents |
| `agents` | yes | List of agents on this floor |
| `furniture` | no | List of shared furniture (task boards, MCP servers) |
| `workstations` | no | List of workstations (sandboxed environments) |

## Agents

Each agent is a participant on the floor.

### LLM agents (default)

LLM agents talk to an OpenAI-compatible API endpoint (like Ollama):

```yaml
agents:
  - id: "@data"
    name: "Data Analyst"
    activation: always
    can_use_sandbox: true
    tool_context: summary
    temperature: 0.7
    prompt: |
      You are @data, a senior data analyst.
      If you want someone to respond, write "@name?" with a question mark.
      If you have nothing to add, respond with exactly: [PASS]
```

### ACP agents

ACP agents are external processes that speak the [Agent Client Protocol](https://agentclientprotocol.com). The floor launches them and communicates over stdio:

```yaml
agents:
  - id: "@claude"
    type: acp
    command: claude-code-acp
    activation: mention
    prompt: "You are @claude, a coding expert on this floor..."
    env:
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
```

Most agents need an ACP adapter — for example, Claude Code uses
[claude-code-acp](https://github.com/zed-industries/claude-code-acp)
(`npm i -g @zed-industries/claude-code-acp`).

### External prompt files

For longer prompts, use `prompt_file` to keep the blueprint clean:

```yaml
agents:
  - id: "@data"
    name: "Data Analyst"
    activation: always
    prompt_file: prompts/analyst.txt
```

The path is relative to the blueprint file's directory. Absolute paths also work.

### Agent fields

| Field | Default | Description |
|-------|---------|-------------|
| `id` | *required* | Unique ID, must start with `@` (e.g. `"@data"`) |
| `name` | | Human-readable name |
| `type` | `"llm"` | `"llm"` for OpenAI-compatible API, `"acp"` for Agent Client Protocol |
| `prompt` | | System prompt defining the agent's role and behavior |
| `prompt_file` | | Path to a file containing the system prompt (relative to blueprint dir). Cannot be used together with `prompt`. |
| `activation` | `"mention"` | When the agent wakes up: `"mention"` (only on `@id?`) or `"always"` (listens to everything) |
| `can_use_sandbox` | `false` | Whether the agent can use workstation tools (sandbox, etc.) |
| `tool_context` | `"full"` | How much of other agents' tool output to include: `"full"`, `"summary"`, or `"none"` |
| `furniture` | `[]` | List of furniture names this agent can access (e.g. `[tasks, fs]`) |
| `temperature` | `0.7` | LLM temperature |

**LLM-only fields:**

| Field | Default | Description |
|-------|---------|-------------|
| `model` | `defaults.model` | LLM model name |
| `endpoint` | `defaults.endpoint` | OpenAI-compatible API URL |

**ACP-only fields:**

| Field | Default | Description |
|-------|---------|-------------|
| `command` | *required for ACP* | Command to launch the ACP agent process |
| `args` | `[]` | Arguments for the command |
| `env` | `{}` | Environment variables (supports `${VAR}` expansion) |

## Furniture

Furniture are shared interactive objects on the floor — task boards, file systems, external APIs. Agents interact with them via tool calls. OFC proxies all interactions for access control and observability.

### Built-in types

```yaml
furniture:
  - name: tasks
    type: taskboard    # in-memory task board (list, add, update, get)
```

### External MCP servers

External MCP servers are wrapped as furniture. OFC spawns the process, discovers tools, and proxies calls:

```yaml
furniture:
  - name: fs
    type: mcp
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "./workspace"]
```

### Furniture fields

| Field | Default | Description |
|-------|---------|-------------|
| `name` | *required* | Identifier used in agent `furniture` lists |
| `type` | *required* | `"taskboard"` (built-in) or `"mcp"` (external MCP server) |
| `command` | | Command to launch the MCP server (required for `type: mcp`) |
| `args` | `[]` | Arguments for the command |
| `config` | `{}` | Type-specific key-value configuration |

### Agent access

Agents declare which furniture they can use via the `furniture` field:

```yaml
agents:
  - id: "@planner"
    furniture: [tasks]           # can only access task board
  - id: "@coder"
    furniture: [tasks, fs]       # can access task board and filesystem
```

LLM agents get furniture tools injected as function calls, namespaced as `{furniture}__{tool}` (e.g. `tasks__add_task`). ACP agents receive MCP server URLs and connect themselves.

## Workstations

Workstations are sandboxed environments for code execution.

### Sandbox

A Docker-based sandbox for code execution:

```yaml
workstations:
  - type: sandbox
    name: python-sandbox
    image: python:3.11-slim
    dockerfile: ./sandbox/Dockerfile
    mount: ./workspace:/workspace
```

If `dockerfile` is specified, the image is built automatically (and rebuilt when the Dockerfile changes). Otherwise, the `image` is pulled directly.

### Workstation fields

| Field | Default | Description |
|-------|---------|-------------|
| `type` | *required* | Workstation type (currently: `"sandbox"`) |
| `name` | | Human-readable name |
| `image` | `"python:3.11-slim"` | Docker image to use |
| `dockerfile` | | Path to Dockerfile (builds image automatically) |
| `mount` | | Host:container mount path |

## Turn-taking

Agents interact through conversation:

- **`@name?`** (with question mark) — asks that agent to respond next. The asking agent gets called back with the response.
- **`@name`** (without question mark) — informational mention, doesn't trigger a response.
- **`[PASS]`** — agent has nothing to add, skips its turn.
- **`activation: always`** — agent is polled after every message (should use `[PASS]` when it has nothing to say).
- **`activation: mention`** — agent only responds when explicitly mentioned with `@id?`.

Delegation chains work like a call stack: if `@user` asks `@data?`, and `@data` asks `@code?`, then `@code`'s response goes back to `@data`, and `@data`'s response goes back to `@user`.

## Rooms

Rooms are isolated sub-conversations. Agents in a room have their own Chat and Controller — messages don't leak to the main floor.

### Creating a room

```
/room #analysis @data @code Analyze the sales data and produce a summary
```

This creates room `#analysis`, moves `@data` and `@code` into it, and posts the prompt. The agents collaborate in the room, and their output is tagged with the room name (e.g. `[#analysis/@data]:`).

### Auto-close

When all agents in a room are done (no more agents to trigger), the room auto-closes:
- Agents return to the main floor
- A summary is posted to the main floor chat
- Agents receive a system message about the transition

### Manual close

```
/room close #analysis
```

### Commands

| Command | Description |
|---------|-------------|
| `/quit` | Exit the floor |
| `/clear` | Clear conversation history and reset turn state |
| `/room #name @agent1 @agent2 [prompt]` | Create a room with agents and optional prompt |
| `/room close #name` | Close a room manually |

## Full example

```yaml
name: data-analysis
description: "Data analysis team with analyst and coder"

defaults:
  endpoint: http://localhost:11434/v1
  model: glm-4.7:cloud

agents:
  - id: "@data"
    name: "Data Analyst"
    activation: always
    can_use_sandbox: true
    temperature: 0.7
    tool_context: summary
    prompt: |
      You are @data, a senior data analyst.
      - @code: A programmer for complex coding tasks
      If you want @code to do something, say "@code? <request>".
      For QUICK exploration, use your bash tool.
      For COMPLEX tasks, delegate to @code.
      If you have nothing to add: [PASS]

  - id: "@code"
    name: "Coder"
    activation: mention
    can_use_sandbox: true
    temperature: 0.2
    tool_context: full
    furniture: [tasks]
    prompt: |
      You are @code, an expert programmer.
      Implement what's asked. Keep responses SHORT.

furniture:
  - name: tasks
    type: taskboard

workstations:
  - type: sandbox
    name: python-sandbox
    image: python:3.11-slim
    dockerfile: ./sandbox/Dockerfile
    mount: ./workspace:/workspace
```
