# Blueprint Reference

A blueprint is a YAML file that defines a floor — the agents, their configuration, and the furniture (tools) available to them. Think of it as `docker-compose.yaml` for AI teams. How a floor is run — frontend, port, session store — is set by ofc's flags, environment variables or a config file; see [Settings](README.md#settings).

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

models:
  local:
    endpoint: http://localhost:11434/v1
    model: llama3

defaults:
  model: local

agents:
  - id: "@assistant"
    # ...

furniture:
  - name: tasks
    # ...
```

## Top-level fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Floor name, shown in the header |
| `description` | no | Short description of the floor |
| `models` | no | The blueprint's own models, by name — see [Models](#models) |
| `defaults` | no | Default `model` and `thinking` for all agents |
| `agents` | yes | List of agents on this floor |
| `furniture` | no | List of shared furniture (task boards, MCP servers, sandboxes) |

A key that isn't a field of the blueprint, an agent or a furniture is an error.

## Environment variables

Any string value in a blueprint can reference an environment variable with
`${VAR}` — not a select few fields, but every string, anywhere in the file.
Endpoints, API keys, commands, args, MCP headers: if it's a
string, it expands. Braces are required — a bare `$VAR` stays as-is, so
prices and shell snippets survive intact. The syntax follows bash:

| Form | If the variable is unset | If it's set but empty |
|------|--------------------------|-----------------------|
| `${VAR}` | **error** — the floor doesn't start | used as-is (empty) |
| `${VAR-default}` | `default` | used as-is (empty) |
| `${VAR:-default}` | `default` | `default` |

```yaml
models:
  openai:
    endpoint: ${OPENAI_BASE_URL:-https://api.openai.com/v1}   # optional
    model: gpt-5
    api_key: ${OPENAI_API_KEY}                                # required
```

A bare `${VAR}` that isn't set is a hard error, and every missing variable is
listed at once:

```
Error loading blueprint: unset environment variables:
  models[openai].api_key: ${OPENAI_API_KEY}
  furniture[0].headers[Authorization]: ${MCP_TOKEN}
```

Since `${VAR-}` says "optional" explicitly, an unadorned reference means the
value is genuinely required.

Expansion happens once, when the blueprint loads, and the results are baked
into the running floor. Changing a variable afterwards doesn't affect a floor
that's already running.

Variables come from the environment, which includes those of ofc's config
file (`--config prod.env`, see [Settings](README.md#settings)). One blueprint
can so be run with different values per deployment:

```yaml
agents:
  - id: "@assistant"
    prompt: |
      You are running in the ${DEPLOY_ENVIRONMENT:-dev} environment.
```

## Agents

Each agent is a participant on the floor.

### LLM agents (default)

LLM agents talk to an OpenAI-compatible API endpoint (like Ollama):

```yaml
agents:
  - id: "@data"
    name: "Data Analyst"
    activation: always
    furniture: [sandbox]
    tool_context: summary
    temperature: 0.7
    prompt: |
      You are @data, a senior data analyst.
      If you want someone to respond, write "@name?" with a question mark.
      If you have nothing to add, respond with exactly: [PASS]
```

### Models

An LLM agent's `model:` names a model; `defaults.model` names it for every
agent that doesn't. The name is looked up in the blueprint's own `models:`
first, and otherwise in [mf](https://github.com/mikiobraun/mf)'s registry:

```yaml
models:
  local:
    endpoint: http://localhost:11434/v1
    model: qwen3:8b                       # the provider's model ID
  openai:
    endpoint: https://api.openai.com/v1
    model: gpt-5
    api_key: ${OPENAI_API_KEY}

defaults:
  model: local

agents:
  - id: "@planner"                        # local, from defaults
  - id: "@coder"
    model: openai                         # from models:
  - id: "@critic"
    model: "@coding"                      # not in models: → mf
```

A blueprint model has an `endpoint` (an OpenAI-compatible API base URL), the
provider's `model` ID and, for hosted endpoints, an `api_key`, sent as
`Authorization: Bearer <key>`. Keep the key in the environment — see
[Environment variables](#environment-variables). With no key the header is
omitted, which is what lets local endpoints like Ollama work.

A name that isn't in `models:` is an mf address — an alias, `endpoint/model`,
or `@tag` — resolved in mf's registry (`$MF_CONFIG_DIR`, else `~/.config/mf`),
with the key from `MF_KEY_<ENDPOINT>` or mf's secrets. ofc reads the registry
only when a blueprint uses it, so a blueprint with its own models needs no mf.
A name that resolves nowhere stops the floor from starting.

When the floor starts it prints where each agent's model came from:

```
[System]: Model of @planner: local (blueprint) → qwen3:8b
[System]: Model of @critic: @coding (mf) → gw/anthropic/claude-haiku-5-5
```

`model` is for LLM agents; an ACP agent chooses its own model.

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

An ACP agent reads and writes files and runs terminal commands by asking ofc,
which carries the requests out on the host, rooted at `./workspace`. With
`sandbox:` naming a [sandbox furniture](#sandbox), ofc carries them out in that
container instead, rooted at its workspace:

```yaml
agents:
  - id: "@claude"
    type: acp
    command: claude-code-acp
    sandbox: box
```

The agent process itself runs on the host either way, and an agent may use its
own built-in tools without asking ofc.

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
| `tool_context` | `"full"` | How much of other agents' tool output to include: `"full"`, `"summary"`, or `"none"` |
| `furniture` | `[]` | The furniture this agent can use (e.g. `[tasks, fs]`) — see [Agent access](#agent-access) |
| `temperature` | `0.7` | LLM temperature |

**LLM-only fields:**

| Field | Default | Description |
|-------|---------|-------------|
| `model` | `defaults.model` | A model from the blueprint's `models:`, else an mf address — see [Models](#models) |
| `thinking` | `defaults.thinking`, else `auto` | How reasoning is separated: `auto`, `field`, `tags`, `none` — see [Thinking](#thinking) |
| `thinking_tags` | `["<think>", "</think>"]` | Inline tag pair to scan for |

**ACP-only fields:**

| Field | Default | Description |
|-------|---------|-------------|
| `command` | *required for ACP* | Command to launch the ACP agent process |
| `args` | `[]` | Arguments for the command |
| `env` | `{}` | Environment variables for the agent process |
| `sandbox` | | Sandbox furniture in which the agent's file and terminal requests run; without it they run on the host |

## Furniture

Furniture are shared interactive objects on the floor — task boards, file systems, external APIs. Agents interact with them via tool calls. OFC proxies all interactions for access control and observability.

### Built-in types

```yaml
furniture:
  - name: tasks
    type: taskboard    # in-memory task board (list, add, update, get)
```

### Sandbox

A Docker container for running code. Its one tool, `bash`, runs a command in the container:

```yaml
furniture:
  - name: box
    type: sandbox
    image: python:3.11-slim
    dockerfile: ./sandbox/Dockerfile   # optional: build `image` from this
    workspace: ./workspace
```

The `workspace` directory on the host is mounted into the container at the same absolute path, and commands run there. With `dockerfile` (a Dockerfile or its directory), ofc builds `image` from it when the image is missing or older than the Dockerfile; otherwise `image` is used as is. An LLM agent gets `bash` by listing the sandbox in `furniture`; an ACP agent names it in `sandbox` (see [ACP agents](#acp-agents)).

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

**OAuth** — for MCP servers behind OAuth, add an `oauth:` block instead of a fixed header:

```yaml
furniture:
  - name: kb
    type: mcp
    url: https://kb.example.com/mcp
    oauth: {}                             # discover, register (DCR), consent once
    # oauth:
    #   scopes: [kb.read]                 # default: what the server advertises
    #   client_id: ${KB_CLIENT_ID}        # a pre-registered client instead of DCR
    #   client_secret: ${KB_CLIENT_SECRET}
    #   grant: client_credentials         # no person involved; needs client_id + client_secret
```

With the default `authorization_code` grant, a person consents once: run `ofc auth -f blueprint.yaml kb` and open the printed URL, or let an interactive `ofc run` (a terminal, not `--json`) ask. The token goes to `~/.ofc/oauth/<floor>/kb.json` (or `--oauth-dir`), and every ofc using the furniture refreshes it there — including ones already running. A run nobody can answer, such as a cron job, never asks: it fails with a hint to run `ofc auth kb`.

### Furniture fields

| Field | Default | Description |
|-------|---------|-------------|
| `name` | *required* | Identifier used in agent `furniture` lists |
| `type` | *required* | `"taskboard"` (built-in), `"mcp"` (external MCP server) or `"sandbox"` (Docker container) |
| `command` | | Command to launch the MCP server (stdio transport) |
| `args` | `[]` | Arguments for the command |
| `url` | | URL of an already-running MCP server (HTTP transport) |
| `headers` | `{}` | HTTP headers for URL connections |
| `oauth` | | OAuth for URL connections (see above) |
| `config` | `{}` | Type-specific key-value configuration |
| `image` | `"python:3.11-slim"` | Sandbox: Docker image |
| `dockerfile` | | Sandbox: Dockerfile to build `image` from |
| `workspace` | `./workspace` | Sandbox: host directory mounted into the container; relative to the blueprint |

### Agent access

Agents declare which furniture they can use via the `furniture` field:

```yaml
agents:
  - id: "@planner"
    furniture: [tasks]           # can only access task board
  - id: "@coder"
    furniture: [tasks, fs]       # can access task board and filesystem
```

LLM agents get furniture tools injected as function calls, namespaced as `{furniture}__{tool}` (e.g. `tasks__add_task`), and a call to furniture the agent doesn't list fails. ACP agents receive MCP server URLs and connect themselves, with a token of their own that opens only the furniture they list. Every name in `furniture` must be a furniture of the floor.

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

models:
  glm:
    endpoint: http://localhost:11434/v1
    model: glm-4.7:cloud

defaults:
  model: glm

agents:
  - id: "@data"
    name: "Data Analyst"
    activation: always
    temperature: 0.7
    tool_context: summary
    furniture: [sandbox]
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
    temperature: 0.2
    tool_context: full
    furniture: [tasks, sandbox]
    prompt: |
      You are @code, an expert programmer.
      Implement what's asked. Keep responses SHORT.

furniture:
  - name: tasks
    type: taskboard

  - name: sandbox
    type: sandbox
    image: python:3.11-slim
    dockerfile: ./sandbox/Dockerfile
    workspace: ./workspace
```
