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
| `defaults` | no | Default `endpoint`, `model`, `api_key` and `thinking` for all agents |
| `config` | no | Runtime knobs (frontend, web, store, debug, log) — see [Config](#config) |
| `agents` | yes | List of agents on this floor |
| `furniture` | no | List of shared furniture (task boards, MCP servers) |
| `workstations` | no | List of workstations (sandboxed environments) |

## Environment variables

Any string value in a blueprint can reference an environment variable with
`${VAR}` — not a select few fields, but every string, anywhere in the file.
Endpoints, API keys, commands, args, MCP headers, the store DSN: if it's a
string, it expands. The syntax follows bash:

| Form | If the variable is unset | If it's set but empty |
|------|--------------------------|-----------------------|
| `${VAR}` | **error** — the floor doesn't start | used as-is (empty) |
| `${VAR-default}` | `default` | used as-is (empty) |
| `${VAR:-default}` | `default` | `default` |

```yaml
defaults:
  endpoint: ${OFC_ENDPOINT:-http://localhost:11434/v1}   # optional
  api_key: ${OPENAI_API_KEY}                             # required
```

A bare `${VAR}` that isn't set is a hard error, and every missing variable is
listed at once:

```
Error loading blueprint: unset environment variables:
  defaults.api_key: ${OPENAI_API_KEY}
  furniture[0].headers[Authorization]: ${MCP_TOKEN}
```

Since `${VAR-}` says "optional" explicitly, an unadorned reference means the
value is genuinely required.

Two things to know:

- **Only `${VAR}` is a reference.** A bare `$VAR` or a lone `$` is left alone,
  so prices, regexes and shell snippets survive intact.
- **The boundary is the file, not the field.** Everything written in
  `blueprint.yaml` expands, `prompt:` included. Text loaded by reference —
  `prompt_file` contents — is not blueprint text and is left alone; use
  [`<% env %>`](#prompt-templating) inside those.

Expansion happens once, when the blueprint loads, and the results are baked
into the running floor. Changing a variable afterwards doesn't affect a floor
that's already running.

## Config

The `config:` section carries runtime/deployment defaults for this project — what historically came from CLI flags or env vars. Per-invocation knobs (`--file`, `--session`, the initial prompt) stay on the command line.

```yaml
config:
  frontend: cli         # cli | tui | json
  debug: false
  log: ""               # path; empty = no log file
  web:
    enabled: false
    port: 8080
    hostname: ""        # external URL for printed link
  store:
    type: jsonl         # jsonl | postgres
    dsn: ${OFC_DATABASE_URL}  # keep secrets out of the file
```

Precedence: a CLI flag wins when explicitly passed (`cobra` `Changed()` semantics); otherwise the blueprint `config:` value is used; otherwise the built-in default. Profiles (`profiles:` overlays) and `~/.ofc/defaults.yaml` are noted but not yet implemented.

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

Hosted endpoints need an `api_key`, sent as `Authorization: Bearer <key>`:

```yaml
agents:
  - id: "@data"
    endpoint: https://api.openai.com/v1
    model: gpt-4o
    api_key: ${OPENAI_API_KEY}
```

Set `api_key` under `defaults:` to share one key across agents; a per-agent value
overrides it. Keep the secret in the environment rather than the file — see
[Environment variables](#environment-variables). With no key at all the header is
omitted entirely, which is what lets local endpoints like Ollama work.

### Thinking

Reasoning models emit their thinking either inline as `<think>…</think>` or as a
separate `reasoning_content` field on the stream, depending on the inference
server. OFC separates it from the answer either way: reasoning is streamed to
frontends as its own event and kept on the message, but it is **never** fed back
as context — not to other agents, and not to the model that produced it.

```yaml
agents:
  - id: "@thinker"
    thinking: auto                              # default
    thinking_tags: ["<think>", "</think>"]      # optional, this is the default pair
```

| Mode | Behavior |
|------|----------|
| `auto` | Both: read the `reasoning_content` field and scan for inline tags. |
| `field` | Only the `reasoning_content` / `reasoning` field. |
| `tags` | Only inline tags. |
| `none` | No separation — tags stay in the answer, for models where they're real output. |

Set `thinking` under `defaults:` to apply it to every agent. Tags may be split
across streaming chunks; that's handled. Use `thinking_tags` for models with a
different convention, e.g. `["◁think▷", "◁/think▷"]`.

The barebone CLI collapses reasoning to a single `thinking…` line that clears
when the answer starts, and never writes it to the log file. `--json` emits it
as `{"type":"thought",…}` so a frontend can show as much or as little as it
likes.

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

If credentials are required, set them through `env:` using whatever variable the
adapter expects.

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

### Prompt templating

Prompts (inline or from `prompt_file`) support Go templates with `<% %>` delimiters.
Useful for embedding generated content like product catalogs, environment-driven config,
or shared instruction blocks across agents.

Available functions:

| Function | Description |
|----------|-------------|
| `readfile "path"` | Read a file. Paths are relative to the blueprint directory; absolute paths also work. |
| `env "VAR"` | Read an environment variable. Unset is an error, matching [`${VAR}`](#environment-variables). |
| `env "VAR" "default"` | Read an environment variable, falling back to `default` when unset. |

Example:

```yaml
agents:
  - id: "@hiro"
    prompt: |
      You are an expert camera shop owner.

      Available products in the shop:
      <% readfile "data/catalog.md" %>

      Today's location: <% env "SHOP_LOCATION" "Tokyo" %>
```

The `<% %>` delimiters were chosen instead of the default `{{ }}` to avoid conflicts with
LLM prompt content (Ollama Modelfiles, Jinja examples, etc.). Prompts with no `<%` marker
are passed through unchanged — there's no overhead for non-templated prompts.

**Templates render on every turn.** `readfile` re-reads the file and `env`
re-reads the variable each time the agent is woken, so a catalog an agent edits
mid-session shows up in its own prompt on the next turn. This is the difference
between the two mechanisms:

| | `${VAR}` | `<% %>` |
|---|---|---|
| Applies to | any string in `blueprint.yaml` | prompts, wherever they came from |
| Resolved | once, at load | every turn |

Syntax errors still fail at startup — the template is parsed when the blueprint
loads, and only executed per turn. Errors from the functions themselves (a
missing `readfile` path, an unset `env` with no default) surface on the turn
that hits them, and end that agent's turn.

Worth knowing if you rely on prompt caching: a template whose output is
identical each turn keeps the prefix stable and stays cacheable. One that
varies invalidates it.

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
| `api_key` | `defaults.api_key` | Bearer token for `endpoint`. Omitted from the request when empty. |
| `thinking` | `defaults.thinking`, else `auto` | How reasoning is separated: `auto`, `field`, `tags`, `none` — see [Thinking](#thinking) |
| `thinking_tags` | `["<think>", "</think>"]` | Inline tag pair to scan for |

**ACP-only fields:**

| Field | Default | Description |
|-------|---------|-------------|
| `command` | *required for ACP* | Command to launch the ACP agent process |
| `args` | `[]` | Arguments for the command |
| `env` | `{}` | Environment variables for the agent process |

## Furniture

Furniture are shared interactive objects on the floor — task boards, file systems, external APIs. Agents interact with them via tool calls. OFC proxies all interactions for access control and observability.

### Built-in types

```yaml
furniture:
  - name: tasks
    type: taskboard    # in-memory task board (list, add, update, get)
```

### External MCP servers

External MCP servers are wrapped as furniture. Two connection modes:

**Subprocess (stdio)** — OFC spawns the process and connects via stdin/stdout:

```yaml
furniture:
  - name: fs
    type: mcp
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "./workspace"]
```

**URL (HTTP)** — OFC connects to an already-running MCP server:

```yaml
furniture:
  - name: remote-tools
    type: mcp
    url: http://localhost:3001/mcp
    headers:
      Authorization: "Bearer ${MCP_TOKEN}"
```

### Furniture fields

| Field | Default | Description |
|-------|---------|-------------|
| `name` | *required* | Identifier used in agent `furniture` lists |
| `type` | *required* | `"taskboard"` (built-in) or `"mcp"` (external MCP server) |
| `command` | | Command to launch the MCP server (stdio transport) |
| `args` | `[]` | Arguments for the command |
| `url` | | URL of an already-running MCP server (HTTP transport) |
| `headers` | `{}` | HTTP headers for URL connections |
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
