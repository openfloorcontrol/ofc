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
| `workstations` | no | List of workstations (tools) available |

## Agents

Each agent is a participant on the floor.

### LLM agents (default)

LLM agents talk to an OpenAI-compatible API endpoint (like Ollama):

```yaml
agents:
  - id: "@data"
    name: "Data Analyst"
    activation: always
    can_use_tools: true
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

### Agent fields

| Field | Default | Description |
|-------|---------|-------------|
| `id` | *required* | Unique ID, must start with `@` (e.g. `"@data"`) |
| `name` | | Human-readable name |
| `type` | `"llm"` | `"llm"` for OpenAI-compatible API, `"acp"` for Agent Client Protocol |
| `prompt` | | System prompt defining the agent's role and behavior |
| `activation` | `"mention"` | When the agent wakes up: `"mention"` (only on `@id?`) or `"always"` (listens to everything) |
| `can_use_tools` | `false` | Whether the agent can use workstation tools (sandbox, etc.) |
| `tool_context` | `"full"` | How much of other agents' tool output to include: `"full"`, `"summary"`, or `"none"` |
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

## Workstations

Workstations are shared tools available to agents on the floor.

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
    can_use_tools: true
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
    can_use_tools: true
    temperature: 0.2
    tool_context: full
    prompt: |
      You are @code, an expert programmer.
      Implement what's asked. Keep responses SHORT.

workstations:
  - type: sandbox
    name: python-sandbox
    image: python:3.11-slim
    dockerfile: ./sandbox/Dockerfile
    mount: ./workspace:/workspace
```
