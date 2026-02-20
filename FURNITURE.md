# Furniture Architecture

> How agents interact with shared objects on the floor

## Core Idea

Furniture are shared interactive objects that live on the floor (or in a room) — task boards, file systems, databases, external APIs. Agents interact with them via tool calls. OFC sits in the middle of every interaction for access control and observability.

## Design Decisions

- **"Furniture"** is the term — shared objects on the floor, like furniture in a room
- **MCP is the internal transport** — all furniture (built-in and external) gets wrapped as MCP servers
- **OFC always proxies** — agents never talk directly to furniture; OFC mediates for access control and observability
- **Access control is per-tool** — not read/write semantics, but which specific tools an agent can call (planned, not yet implemented)
- **Two agent paths** — LLM agents get tools injected directly; ACP agents get MCP server URLs

## Architecture

```
                        ┌──────────────────────────┐
                        │       Blueprint          │
                        │                          │
                        │  furniture:              │
                        │    - tasks (taskboard)   │ built-in
                        │    - fs (mcp, stdio)     │ external
                        │    - github (mcp, url)   │ external
                        │                          │
                        │  agents:                 │
                        │    - @planner [tasks]    │
                        │    - @coder [tasks, fs]  │
                        └────────────┬─────────────┘
                                     │
                                     ▼
                        ┌──────────────────────────┐
                        │     Furniture Map         │
                        │                          │
                        │  "tasks" → TaskBoard     │ Furniture interface
                        │  "fs"    → ExternalMCP   │ Furniture interface
                        │  "github"→ ExternalMCP   │ Furniture interface
                        └──┬───────────────────┬───┘
                           │                   │
              LLM path     │                   │  ACP path
              (direct)     │                   │  (via MCP server)
                           │                   │
                           ▼                   ▼
              ┌──────────────────┐  ┌──────────────────────────┐
              │   LLM Agents    │  │    MCP Wrapper Layer      │
              │                 │  │                          │
              │  Runner calls   │  │  WrapAsMCP() per         │
              │  Furniture.Call │  │  Furniture instance       │
              │  directly.      │  └────────────┬─────────────┘
              │                 │               │
              │  Tools injected │               ▼
              │  as function    │  ┌──────────────────────────┐
              │  calls,         │  │    Echo API Server        │
              │  namespaced:    │  │                          │
              │  tasks__add_task│  │  /api/v1/floors/{f}/     │
              │  fs__read_file  │  │    mcp/{name}/  (HTTP)   │
              │                 │  │    sse/{name}/  (SSE)    │
              └──────────────────┘  └────────────┬─────────────┘
                                                 │
                                                 ▼
                                    ┌─────────────────┐
                                    │   ACP Agents    │
                                    │                 │
                                    │  Get MCP server │
                                    │  URL (SSE/HTTP) │
                                    │  in session     │
                                    │  request.       │
                                    │                 │
                                    │  Transport      │
                                    │  auto-selected  │
                                    │  from agent     │
                                    │  capabilities.  │
                                    └─────────────────┘
```

## The Proxy Principle

OFC always sits between agents and furniture:

```
Agent → OFC (access control, logging) → Furniture
```

This applies to both built-in and external furniture. Even when the furniture is an external MCP server, OFC connects to it first, then re-exposes it through its own API server. This gives OFC:

1. **Access control** — per-agent, per-tool filtering (which tools each agent can see/call)
2. **Observability** — all tool calls logged, streamed to frontend, included in conversation context
3. **Uniform interface** — agents don't need to know if furniture is built-in or external

## Built-in Furniture

Built-in furniture implements the `Furniture` interface directly in Go:

```go
type Furniture interface {
    Name() string
    Tools() []Tool
    Call(toolName string, args map[string]interface{}) (interface{}, error)
}
```

Currently implemented:
- **TaskBoard** (`furniture/taskboard.go`) — in-memory task board with `list_tasks`, `add_task`, `update_task`, `get_task`

## External MCP Servers

External MCP servers are existing MCP-compatible services wrapped as `Furniture`:

```yaml
furniture:
  # Stdio-based: OFC spawns the process, connects via stdio
  - name: fs
    type: mcp
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]

  # URL-based: OFC connects to an already-running server
  - name: github
    type: mcp
    url: http://localhost:3000/sse
```

The `ExternalMCP` implementation:
1. Connects to the external server (stdio subprocess or SSE/HTTP URL)
2. Calls `tools/list` to discover available tools
3. Implements `Furniture.Tools()` from the discovered tool list
4. Implements `Furniture.Call()` by proxying to `tools/call`

The external server's tools then flow through the same pipeline as built-in furniture — MCP wrapper, API server, agent injection.

```
                    Built-in                          External (stdio)
                    ────────                          ────────────────
                    TaskBoard                         npx mcp-server-filesystem
                       │                                    │
                       │ implements                         │ stdio
                       │ Furniture                          │
                       ▼                                    ▼
                    Furniture ◄──────────────── ExternalMCP (Furniture impl)
                       │                         connects as MCP client,
                       │                         proxies Tools()/Call()
                       ▼
                    WrapAsMCP()
                       │
                       ▼
                    API Server (SSE + HTTP)
                       │
                       ▼
                    Agents
```

## MCP Transport Selection

ACP agents report their MCP capabilities during initialization. OFC checks these and picks the right transport:

| Agent reports | OFC sends | Endpoint |
|---|---|---|
| `sse: true` | `McpServerSse` | `/api/v1/floors/{f}/sse/{name}` |
| `http: true` | `McpServerHttp` | `/api/v1/floors/{f}/mcp/{name}/` |
| neither | warning (stdio bridge planned) | — |

SSE is preferred over HTTP because claude-code-acp (the primary ACP runtime) only validates SSE in practice, despite advertising HTTP support.

## Blueprint Schema

```yaml
furniture:
  - name: tasks          # name used in agent furniture lists
    type: taskboard      # built-in type

  - name: fs             # external MCP via stdio
    type: mcp
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]

  - name: github         # external MCP via URL
    type: mcp
    url: http://localhost:3000/sse

agents:
  - id: "@planner"
    furniture: [tasks]           # can only access task board
  - id: "@coder"
    furniture: [tasks, fs]       # can access task board and filesystem
```

## What's Implemented

- [x] `Furniture` interface and `Tool` type
- [x] TaskBoard (built-in, in-memory)
- [x] MCP wrapping via go-sdk (`WrapAsMCP`)
- [x] Echo API server with Streamable HTTP + SSE endpoints
- [x] LLM agent tool injection (namespaced as `{furniture}__{tool}`)
- [x] ACP agent MCP pass-through (capability-based transport selection)
- [x] Blueprint `furniture:` and `agents[].furniture:` fields
- [x] Coordinator lifecycle (init, start, stop)

## What's Next

- [ ] External MCP servers (`type: mcp` with `command` or `url`)
- [ ] Per-agent access control at the tool level
- [ ] Furniture persistence (TaskBoard is in-memory only)
- [ ] Stdio bridge for ACP agents that only support stdio MCP

ofc. 🎤
