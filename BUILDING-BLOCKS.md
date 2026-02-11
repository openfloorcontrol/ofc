# OFC Building Blocks

Research notes on existing protocols and projects that OFC can build on.
The goal: OFC should be a thin, focused coordination layer. Everything below
it — agent communication, tool access, deployment — should leverage existing work.

## The Protocol Stack

```
┌─────────────────────────────────────────┐
│  OFC                                    │  Conversational coordination
│  Turn-taking, @mentions, should-wake,   │  ← this is what we build
│  [PASS], floors, blueprints             │
├─────────────────────────────────────────┤
│  ACP (Agent Client Protocol)            │  Agent communication
│  Sessions, streaming, capabilities,     │  ← adopt
│  permissions, rich content types        │
├─────────────────────────────────────────┤
│  MCP (Model Context Protocol)           │  Tool access
│  Tool discovery, execution,             │  ← adopt
│  resources, transports                  │
├─────────────────────────────────────────┤
│  KAOS (K8s Agent Orchestration)         │  Deployment & scaling
│  K8s-native agent lifecycle,            │  ← potential integration
│  service discovery, observability       │
└─────────────────────────────────────────┘
```

## ACP — Agent Client Protocol

**What it is:** Standardized communication between clients and AI agents.
Originally designed for editor ↔ coding agent communication (the "LSP for AI agents"),
but the core machinery is general-purpose.

- **Created by:** Zed (open source, Apache 2.0)
- **Supported agents:** Claude Code, Gemini CLI, Goose, Codex CLI, others
- **Supported clients:** Zed, Neovim, JetBrains, Marimo
- **Website:** https://agentclientprotocol.com
- **GitHub:** https://github.com/agentclientprotocol/agent-client-protocol

### What ACP provides

| Feature | Details |
|---------|---------|
| Transport | JSON-RPC over stdio (local) or HTTP/WebSocket (remote, WIP) |
| Session lifecycle | `initialize` → `session/new` → `session/prompt` → streaming notifications |
| Capability negotiation | Client and agent declare what they support (fs, terminal, audio, MCP transports) |
| Streaming | Rich notifications: `ContentChunk`, `ToolCallUpdate`, `ToolCallResult`, `PlanUpdate` |
| MCP passthrough | `session/new` accepts MCP server configs — agents receive tool access at session start |
| Permissions | `session/request_permission` — agent asks client before sensitive operations |
| Extensions | `ExtRequest`/`ExtNotification` — custom methods for protocol-specific additions |
| Content types | Text, images, audio, resource links, embedded resources, diffs |

### How OFC uses ACP

The OFC floor server acts as an **ACP client**. Instead of defining custom agent endpoints
(`/ofc/respond`, `/ofc/info`, etc.), the floor server speaks ACP to each agent:

| OFC concept (PROTOCOL.md) | ACP equivalent |
|----------------------------|----------------|
| `GET /ofc/info` | `initialize` (capability negotiation) |
| `POST /ofc/respond` | `session/prompt` (with streaming notifications) |
| `POST /ofc/mcp/grant` | MCP servers in `session/new` |
| `GET /ofc/mcp/provides` | Agent capabilities in `initialize` |

OFC-specific concepts can ride on ACP's extension mechanism:

```
_ofc/should-wake    → lightweight wake check (custom extension)
_ofc/floor-context  → multi-party context injection (custom extension)
_ofc/pass           → agent declining to respond (custom extension)
```

### What ACP doesn't cover (OFC's unique value)

- **Turn-taking** — who speaks next, @mention routing, delegation chains
- **should-wake** — lightweight "do you want to respond?" polling
- **Multi-party awareness** — ACP is 1:1; OFC stitches multi-agent context
- **[PASS] behavior** — agent declining to respond
- **Floor/blueprint abstractions** — team composition and workstation setup

### Opportunity: Generalizing ACP beyond coding

ACP currently assumes "user in an IDE" — `fs/read_text_file`, `terminal/create`,
session modes like `ask, architect, code`. But the core machinery (sessions, streaming,
capabilities, permissions) is domain-agnostic. Non-coding agents (project managers,
analysts, designers) would benefit from ACP but don't need the IDE-specific parts.

A conversation with the ACP community about factoring out a general core
with domain-specific extensions could benefit both projects.

### Go SDK

- **Package:** https://github.com/coder/acp-go-sdk (by Coder)
- **Version:** 0.6.3 (Nov 2025)
- **Features:** Typed request/response, session management, terminal support, extension methods
- **Examples:** Claude Code bridge, Gemini CLI integration, stdio agent, client

---

## MCP — Model Context Protocol

**What it is:** Standardized protocol for tool access. Defines how agents discover
and call tools, read resources, and interact with external systems.

- **Maintained by:** Anthropic, in collaboration with Google
- **Website:** https://modelcontextprotocol.io
- **GitHub:** https://github.com/modelcontextprotocol

### What MCP provides

| Feature | Details |
|---------|---------|
| Tool discovery | Agents list available tools and their schemas |
| Tool execution | Structured call/response for tool invocation |
| Resources | Read-access to data sources |
| Transports | stdio, HTTP, SSE |
| Auth | OAuth primitives |

### How OFC uses MCP

MCP servers ARE workstations. The floor metaphor maps directly:

| Workstation | MCP server type |
|-------------|----------------|
| File cabinet | Filesystem MCP |
| Terminal/sandbox | Code execution MCP |
| Whiteboard | Shared state MCP |
| Task board | Task management MCP |

The floor server can:
1. Spin up MCP servers for built-in workstation types
2. Pass MCP server configs to agents via ACP's `session/new`
3. Mediate access control through @floor-manager

### Open gap: MCP type taxonomy

MCP defines *how* to connect to tools but not *what kinds* of tools exist.
There's no standard way to say "this is a filesystem" vs "this is a task board."
Every MCP server is a bag of opaque tool names until you list them.

This matters because @floor-manager needs to reason about workstations semantically
("I need somewhere to store files" → pick the filesystem workstation).

**Possible approaches:**
- OFC defines workstation types as a lightweight convention on top of MCP
- Contribute a type taxonomy upstream to the MCP ecosystem
- Use tool naming conventions + discovery to infer types

### Standard workstations (OFC built-ins)

OFC should ship reference MCP implementations for common primitives:

```yaml
workstations:
  - type: filesystem    # read/write shared files
    path: ./workspace
  - type: sandbox       # execute code in Docker
    image: python:3.11
  - type: taskboard     # track work items
  - type: notepad       # shared scratchpad
```

These are the "default furniture" — every floor can use them with zero config.
Specialized workstations (Trello, Slack, Postgres, etc.) come from
the community or third-party MCP server implementations.

### Go SDK

- **Package:** https://github.com/modelcontextprotocol/go-sdk (official)
- **Version:** 1.3.0 (Feb 2026)
- **Features:** Full MCP spec, server and client, multiple spec versions, OAuth
- **Maintained by:** Anthropic + Google

---

## KAOS — K8s Agent Orchestration System

**What it is:** Kubernetes-native framework for deploying, managing, and
orchestrating AI agents at scale.

- **Created by:** Alejandro Saucedo (https://github.com/axsaucedo/kaos)
- **License:** Open source

### What KAOS provides

| Feature | Details |
|---------|---------|
| Agent lifecycle | K8s operator managing Agent, MCPServer, ModelAPI resources |
| Service discovery | Agents find each other via K8s networking |
| MCP integration | MCPServer as a K8s resource |
| OpenAI API compat | Agents expose `/v1/chat/completions` |
| CLI + Dashboard | `kaos-cli` for management, web UI for monitoring |
| Observability | SigNoz integration for telemetry |
| Multi-agent | Hierarchical delegation, coordinator/specialist patterns |

### How OFC could use KAOS

KAOS maps to OFC's "distributed" deployment mode:

| OFC concept | KAOS equivalent |
|-------------|-----------------|
| Agent | KAOS Agent (K8s pod) |
| Workstation (MCP) | KAOS MCPServer resource |
| Floor | Agentic graph |
| Blueprint | KAOS YAML manifests |

The deployment progression:

```
Local (ofc run)     → everything in-process, great for dev
Hybrid              → local floor server, remote LLM APIs
Distributed (KAOS)  → agents as K8s services, full scaling
```

### Gap: ACP support

KAOS currently uses OpenAI-compatible `/v1/chat/completions` for agent communication.
Adding ACP support would enable richer interactions (streaming notifications,
capability negotiation, sessions, permissions) and interoperability with
the broader ACP ecosystem.

---

## Security Model

The workstation/floor metaphor provides a natural capability-based security model.
This is a key differentiator vs approaches that give agents blanket access.

### Principles

| Principle | How OFC implements it |
|-----------|----------------------|
| Least privilege | Agents only get workstations explicitly placed on the floor |
| Granular permissions | Read vs write vs execute, per workstation |
| Scoped access | Filesystem but only `./data/`, not your home directory |
| Conversational audit | Every access request is a message in the conversation log |
| User in the loop | @floor-manager asks @user for sensitive grants |
| Isolation | Sandbox = Docker container, no host access |
| No implicit access | Nothing by default, everything is explicitly granted |

### Access control is conversational

```
@code: @floor-manager? I need write access to the sandbox
@floor-manager: @user? @code is requesting write access
  to the sandbox. Approve?
@user: yes, go ahead
@floor-manager: Done. @code now has write access.
```

The conversation IS the audit log. No hidden config files, no silent privilege escalation.

### Policies

@floor-manager can have policies for common cases to avoid micromanaging:

```yaml
floor_manager:
  policies:
    filesystem_read: auto_approve
    filesystem_write: ask_user
    sandbox_execute: auto_approve
    sandbox_network: always_deny
    external_apis: always_ask_user
```

---

## What OFC Needs to Build

Given these building blocks, OFC's unique implementation scope is:

### Core (the protocol)
- Turn-taking engine (who speaks next)
- `@mention` parsing and routing (`@name?` triggers, `@name` references)
- `should-wake` polling logic
- `[PASS]` handling and exclusion sets
- Delegation chains (initiator tracking, return-to-sender)
- Floor lifecycle (create, add/remove agents, teardown)

### @floor-manager
- Conversational workstation setup ("add a sandbox")
- Access control mediation (policies, user approval)
- Agent introduction when joining a floor

### Blueprints
- YAML loading and validation
- Agent resolution (inline, local file, hub reference)
- Workstation setup from type declarations
- Blueprint export from running floor state

### Standard workstations (built-in MCP servers)
- Filesystem (scoped read/write)
- Sandbox (Docker-based code execution)
- Task board (create/update/assign tasks)
- Notepad (shared scratchpad)

### CLI / TUI
- Interactive terminal UI with streaming, colors, @mentions
- `ofc run` — run a blueprint
- `ofc init` — create a new blueprint

### Integration glue
- ACP client (via acp-go-sdk) — talk to agents
- MCP server/client (via mcp go-sdk) — provide and consume workstations
- Blueprint → ACP session setup (pass MCP servers to agents)

---

## Go CLI Dependencies

```
cli/
├── ofc/                         ← our code
│   ├── floor.go                 (turn-taking, @mentions, should-wake)
│   ├── blueprint.go             (YAML loading, agent/workstation config)
│   ├── manager.go               (@floor-manager logic)
│   └── workstations/            (built-in MCP server implementations)
│
├── github.com/coder/acp-go-sdk  ← agent communication
│
└── github.com/modelcontextprotocol/go-sdk  ← tool/workstation access
```
