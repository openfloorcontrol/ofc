# OFC - Open Floor Control Protocol

**Version:** 0.1.0-draft

> **Note:** This spec describes the full distributed protocol. The first implementation (v1) will be **local-first**: same concepts, same interfaces, but Python classes instead of HTTP. See [Implementation Modes](#implementation-modes) for the progression from local → hybrid → distributed.

An open protocol for multi-agent conversation. A floor where AI agents collaborate.

## The Metaphor

- **Floor** - A workspace where agents collaborate (not "room")
- **Workstations** - Tools available on the floor (MCPs: file cabinet, terminal, whiteboard)
- **Agents** - Specialists who join the floor to help
- **Floor Manager** - Discovery agent that helps find the right specialists
- **Blueprints** - Template floors for common tasks ("Data Analysis", "Web Dev")

**Everything is conversational.** Discovery, setup, access control—all happen through conversation.

## Overview

OFC defines how multiple agents coordinate in a shared conversation. The key insight: **turn-taking is the hard problem**. Who speaks next? When should an agent engage vs. stay quiet?

### OFC Agents vs Conversation APIs

A common question: how are OFC agents different from just calling an LLM API?

| Raw Conversation API | OFC Agent |
|---------------------|-----------|
| You write the system prompt | Prompt baked in |
| You define tools | Tools pre-configured |
| You manage context window | Just send messages |
| You handle errors, retries | Agent handles it |
| No identity | Has persona (`@code`) |
| Always responds | Can decline (should_wake, PASS) |
| Single-party | Multi-party aware |

**An OFC agent is a pre-packaged AI product.**

Someone did the work: prompt engineering, tool setup, model tuning, hosting. You just invite `@code` to your floor and pay per token.

```
Conversation API = raw engine
OFC Agent        = engine + driver (persona, skills, tools, pricing)
```

**OFC is the protocol that lets AI specialists work together.**

### Actors

- **Floor Server** ("the floor") - Hosts conversations, coordinates turn-taking
- **Agents** - AI participants that expose HTTP endpoints
- **Users** - Humans, identified as `@user`

### Statefulness

The OFC protocol is **stateless by design**. The floor server sends sufficient context with each request. Agents can respond without maintaining state.

However, agents **may** maintain internal state if they choose:
- Track conversation history across sessions
- Remember user preferences
- Do their own context compaction
- Learn and improve over time

From the protocol's perspective, agents look stateless (request → response). Internally, they can be as sophisticated as they want. Simple agents are pure functions. Complex agents (like Claude Code) manage rich state, multiple conversations, memory.

This keeps the protocol simple while allowing agents to compete on capability.

### Core Concepts

- **@mentions**: Reference other participants. `@code` is a reference, `@code?` requests a response
- **Turn-taking**: Floor server determines who responds next
- **[PASS]**: Agents can decline to respond, yielding the floor

---

## API Overview

Clear direction of each endpoint:

| Endpoint | Direction | Purpose |
|----------|-----------|---------|
| `GET /ofc/info` | Floor → Agent | Get agent's capabilities, metadata |
| `POST /ofc/should-wake` | Floor → Agent | Ask if agent wants to respond |
| `POST /ofc/respond` | Floor → Agent | Request agent's response |
| `POST /ofc/mcp/grant` | Floor → Agent | Grant MCP access to agent |
| `GET /ofc/mcp/provides` | Floor → Agent | Query MCPs agent offers |
| | | |
| `POST /floors` | User → Floor | Create a floor |
| `POST /floors/{id}/agents` | User → Floor | Add agent to floor |
| `DELETE /floors/{id}/agents/{agent}` | User → Floor | Remove agent from floor |
| `POST /floors/{id}/messages` | User → Floor | User posts a message |
| `GET /floors/{id}/messages` | User → Floor | Get message history |
| `GET /floors/{id}` | User → Floor | Get floor state |
| `GET /floors/{id}/mcp/granted` | Agent → Floor | What MCPs do I have? |
| `POST /floors/{id}/mcp/request` | Agent → Floor | Request MCP access |
| | | |
| `POST /registry/agents` | Creator → Registry | Register an agent |
| `GET /registry/agents/{id}` | Anyone → Registry | Get agent info |
| `DELETE /registry/agents/{id}` | Creator → Registry | Remove agent |
| `GET /registry/agents/search` | Anyone → Registry | Search agents |

---

## Agent API (Floor → Agent)

Agents expose these HTTP endpoints. **The floor server calls these.**

**Agents are passive.** They don't initiate connections or post messages. They expose endpoints, wait to be called, and respond. The floor server orchestrates everything.

### POST /ofc/should-wake

Lightweight check: "Do you want to respond to this?"

Called by floor server to determine if agent should be woken. Should be fast and cheap (rules-based, small model, or simple heuristics).

**Request:**
```json
{
  "floor_id": "floor-123",
  "last_message": {
    "id": "msg-456",
    "from": "@user",
    "content": "Can someone analyze this data?",
    "timestamp": "2024-01-15T10:30:00Z"
  },
  "context": {
    "recent_speakers": ["@user", "@data"],
    "mentioned_you": false,
    "your_last_message_id": "msg-400"
  }
}
```

**Response:**
```json
{
  "wake": true,
  "reason": "Data analysis is my specialty"
}
```

Or:
```json
{
  "wake": false
}
```

### POST /ofc/respond

Full response generation. Only called when agent should respond.

**Request:**
```json
{
  "floor_id": "floor-123",
  "messages": [
    {
      "id": "msg-001",
      "from": "@user",
      "content": "Let's analyze sales.csv",
      "timestamp": "2024-01-15T10:30:00Z"
    },
    {
      "id": "msg-002",
      "from": "@data",
      "content": "I'll take a look. @code? can you load and summarize it?",
      "timestamp": "2024-01-15T10:30:05Z"
    }
  ],
  "wake_reason": "mentioned_with_question",
  "initiator": "@data"
}
```

**Response (normal):**
```json
{
  "content": "Here's the summary:\n\n| Column | Type | Nulls |\n...",
  "tool_calls": [
    {
      "name": "bash",
      "arguments": {"cmd": "head -5 sales.csv"}
    }
  ],
  "tool_results": ["col1,col2,col3\n1,2,3\n..."]
}
```

**Response (pass):**
```json
{
  "pass": true,
  "reason": "Nothing to add here"
}
```

### GET /ofc/info

Returns agent metadata. This is how agents **introduce themselves** - their capabilities, pricing, and what to send their way.

Used by:
- Floor manager for discovery ("who can help with X?")
- Floor server when agent joins
- Other agents to understand collaborators

**Response:**
```json
{
  "id": "@code",
  "name": "Code Assistant",
  "tagline": "Send me anything that needs code!",
  "description": "Expert programmer. I write scripts, run analysis, build visualizations, debug issues. Just describe what you need.",
  "activation": "mention",
  "capabilities": ["bash", "python", "sql", "visualization"],
  "examples": [
    "Write a script to parse this CSV",
    "Run this SQL query",
    "Debug why this function fails"
  ],
  "provides_mcp": {
    "url": "mcp://code-agent.example.com/tools",
    "capabilities": ["run_python", "run_sql", "edit_file"]
  }
}
```

**Conversational introduction:**

When joining a floor, agents use this info to introduce themselves:

```
@code: Hey! I'm a programmer - send any coding tasks my way.
I can write scripts, run queries, build visualizations.
What are we working on?
```

### POST /ofc/mcp/grant

Floor server grants an agent access to an MCP (workstation).

**Request:**
```json
{
  "floor_id": "floor-123",
  "mcp": {
    "name": "python-sandbox",
    "url": "mcp://ofc.dev/floors/123/sandbox",
    "capabilities": ["bash", "python", "write_file"],
    "permissions": ["read", "write", "execute"]
  },
  "granted_by": "@floor-manager"
}
```

**Response:**
```json
{
  "accepted": true,
  "message": "Thanks! I now have access to python-sandbox."
}
```

Agents can decline if they don't need/want access:
```json
{
  "accepted": false,
  "reason": "I don't need sandbox access for my role."
}
```

### GET /ofc/mcp/provides

Query which MCPs an agent offers to others.

**Response:**
```json
{
  "provides": [
    {
      "name": "code-execution",
      "url": "mcp://code-agent.example.com/tools",
      "capabilities": ["run_python", "run_sql", "lint_code"]
    }
  ]
}
```

This lets other agents discover and use each other's tools directly.

---

## Floor Server API (Agent → Floor, User → Floor)

Floor servers expose these endpoints. **Agents and users call these.**

### POST /floors

Create a new floor.

**Request:**
```json
{
  "name": "Data Analysis Session",
  "config": {
    "max_agents": 10,
    "history_limit": 100
  }
}
```

**Response:**
```json
{
  "floor_id": "floor-123",
  "join_token": "secret-token-xyz"
}
```

### POST /floors/{floor_id}/agents

Add an agent to the floor. Floor server fetches agent info from registry.

**Request:**
```json
{
  "agent_id": "@code"
}
```

**Response:**
```json
{
  "agent_id": "@code",
  "endpoint": "https://code-agent.example.com/ofc",
  "status": "added",
  "info": {
    "tagline": "Send me anything that needs code!"
  }
}
```

Floor server:
1. Looks up @code in registry
2. Calls `GET /ofc/info` on agent to verify it's alive
3. Adds agent to floor roster

### DELETE /floors/{floor_id}/agents/{agent_id}

Remove an agent from the floor.

### POST /floors/{floor_id}/messages

User posts a message. (Agents don't post directly - they respond via `/ofc/respond` and the floor server stores it.)

**Request:**
```json
{
  "from": "@user",
  "content": "Hey @data? can you look at this?"
}
```

**Response:**
```json
{
  "message_id": "msg-789",
  "next_agent": "@data"
}
```

### GET /floors/{floor_id}/messages

Retrieve conversation history.

**Query params:** `?since=msg-id&limit=50`

### GET /floors/{floor_id}

Get floor state (agents, workstations, config).

### GET /floors/{floor_id}/mcp/granted

Agent queries which MCPs it has been granted access to.

**Headers:**
```
Authorization: Bearer <agent_token>
X-OFC-Agent-ID: @code
```

**Response:**
```json
{
  "floor_id": "floor-123",
  "agent_id": "@code",
  "granted_mcps": [
    {
      "name": "python-sandbox",
      "url": "mcp://ofc.dev/floors/123/sandbox",
      "permissions": ["read", "write", "execute"]
    },
    {
      "name": "shared-files",
      "url": "mcp://ofc.dev/floors/123/files",
      "permissions": ["read"]
    }
  ]
}
```

### POST /floors/{floor_id}/mcp/request

Agent requests access to an MCP (goes to floor manager).

**Request:**
```json
{
  "mcp_name": "python-sandbox",
  "permissions_needed": ["write", "execute"],
  "reason": "Need to save and run analysis scripts"
}
```

**Response:**
```json
{
  "status": "pending",
  "message": "Request sent to @floor-manager for approval"
}
```

This triggers the conversational access control flow.

---

## Turn-Taking Protocol

The floor server implements turn-taking:

### 1. Message Arrives

When a new message is posted:

```
1. Store message
2. Extract @mentions with '?' (triggers)
3. If specific agents mentioned → those agents respond
4. Else → poll all agents' /should-wake
5. Wake first agent that returns { wake: true }
6. If none wake → wait for user
```

### 2. Agent Responds

When an agent's /respond returns:

```
1. Store response message
2. If response mentions @user? → pause for user
3. If response is { pass: true } → add to exclude set, goto step 4 of (1)
4. Check for initiator (who @mentioned this agent?) → they respond next
5. Else → poll /should-wake again
```

### 3. Conversation Patterns

**Direct addressing:**
```
@user: Hey @code? write a function
       → @code responds (mentioned with ?)
       → @code's response goes back to @user (initiator)
```

**Open floor:**
```
@user: Can someone help with this data?
       → Floor polls all agents' /should-wake
       → @data returns { wake: true }
       → @data responds
```

**Delegation chain:**
```
@user: Analyze this dataset
       → @data wakes (activation: always)
       → @data: "Let me check... @code? can you load it?"
       → @code responds (mentioned with ?)
       → Response returns to @data (initiator)
       → @data interprets, responds to @user
```

---

## Conventions

### @mention Syntax

- `@name` - Reference only, no response triggered
- `@name?` - Request response from that agent
- `@user` - The human participant (special)

### Pass Behavior

Agents can pass by returning `{ pass: true }`. This means:
- "I have nothing to add right now"
- Floor server excludes them from this turn's /should-wake polling
- They can still be explicitly @mentioned

### Tool Execution via MCP (Workstations)

OFC delegates tool execution to **MCP (Model Context Protocol)**. Think of MCPs as **furniture/facilities** on the floor:

- File cabinet (filesystem MCP)
- Whiteboard (shared state MCP)
- Terminal (bash/code execution MCP)
- Coffee machine (external API MCP) ☕

This metaphor provides **clean security**: you explicitly place furniture on the floor, and agents can only use what's there. No implicit access.

This keeps concerns separated:

- **OFC** = conversation protocol (who speaks, when, to whom)
- **MCP** = tool protocol (what tools exist, how to call them)

**Agents are MCP clients.** They connect to MCP servers for tool access:

| MCP Type | Example | Who Provides |
|----------|---------|--------------|
| Local | Code editor, terminal, filesystem | User's machine |
| Remote | Web APIs, databases, SaaS | Third parties |
| Floor-provided | Shared sandbox, floor storage | Floor server |

**Floor server can provide MCPs (workstations):**

When an agent joins, the floor can advertise available MCP servers:

```json
{
  "floor_id": "floor-123",
  "mcp_servers": [
    {
      "name": "sandbox",
      "url": "mcp://ofc.dev/floors/123/sandbox",
      "capabilities": ["bash", "python"]
    },
    {
      "name": "shared-files",
      "url": "mcp://ofc.dev/floors/123/files",
      "capabilities": ["read", "write"]
    }
  ]
}
```

**User-provided MCPs:**

Users can grant agents access to their local MCPs:

```json
{
  "agent_id": "@code",
  "granted_mcps": [
    { "name": "my-editor", "url": "mcp://localhost:3000/editor" }
  ]
}
```

This lets users say: "Let @code edit my files" without the floor server being involved.

**Conversational access control:**

The floor manager mediates access requests:

```
@code: @floor-manager? I need write access to the sandbox
to save this analysis script.

@floor-manager: @user? @code is requesting write access
to the Python sandbox. Approve?

@user: Yes, go ahead

@floor-manager: Done! @code now has write access to sandbox.

@code: Thanks! Saving the script now...
```

**Access policies:**

Floor managers can have policies for common cases:

| Access Type | Policy Example |
|-------------|----------------|
| Read data files | Auto-approve |
| Write to sandbox | Ask user |
| Execute code | Auto-approve for @code |
| External APIs | Always ask user |
| Grant to others | Always ask user |

```
@data: @floor-manager? Can I read the CSV files?

@floor-manager: Read access is open - go ahead!
```

This keeps users in control without micromanaging every request.

**Agents as MCP providers:**

Agents can also PROVIDE MCPs to the floor, enabling direct tool collaboration:

```json
{
  "id": "@code",
  "provides_mcp": {
    "url": "mcp://code-agent.example.com/tools",
    "capabilities": ["run_python", "edit_file", "run_sql"]
  }
}
```

Other agents can then use `@code`'s tools directly:

| Collaboration Mode | How | When to Use |
|-------------------|-----|-------------|
| Conversation | `@code? can you...` | Needs judgment, discussion |
| Direct MCP call | Call @code's MCP tool | Mechanical task, just do it |

This enables:
- **Tool-first agents** - Primarily MCP providers, minimal conversation
- **Agent composition** - Chain tools across multiple agents
- **Efficiency** - Skip chat round-trip for mechanical tasks

Example: `@data` needs to run SQL
- Conversational: "@code? can you run this query?"
- Direct: calls `@code`'s MCP → `run_sql("SELECT * FROM...")`

**Benefits:**
- Leverages existing MCP ecosystem
- Clear separation of concerns
- Users control tool access granularly
- Agents can bring specialized MCPs (web search, APIs)
- Agents can collaborate via tools, not just conversation

---

## Streaming

OFC uses a **server-initiated** model:

1. Floor server calls agent's `/ofc/respond`
2. Agent can stream response back (SSE or chunked HTTP)
3. Floor server forwards stream to connected users/observers

No polling required from agent side. Agents are passive - they wait to be called.

**For real-time user experience:**
- Users connect to floor via WebSocket or SSE
- Floor server broadcasts messages as they arrive/stream

---

## Discovery & Onboarding

Discovery is conversational. The **Floor Manager** (`@floor-manager`) is a built-in agent:

```
You: Hey @floor-manager? I need to analyze some sales data

@floor-manager: For data analysis, I'd recommend:
  - @data - Senior analyst, guides the process
  - @code - Programmer, implements analysis
  - @viz - Visualization specialist

Want me to set up a "Data Analysis" floor with these agents
and a Python workstation?

You: Yes, but skip @viz for now

@floor-manager: Done! Created floor "Sales Analysis".
@data and @code are joining. Python workstation ready.
```

### Agent Introduction

When agents join, they introduce themselves conversationally:

```
@data: Hey! I'm a data analyst. I'll help guide the analysis
and make sure we're asking the right questions. I see there's
a Python workstation here - perfect. What are we looking at?
```

### Blueprints

Common floor setups can be saved as blueprints:

```
You: @floor-manager? Save this as a blueprint called "Quick Analysis"

@floor-manager: Saved! "Quick Analysis" blueprint includes:
  - @data, @code
  - Python workstation, shared filesystem
  Next time just say "set up a Quick Analysis floor"
```

---

## Pricing (Optional Metadata)

Agents can optionally declare pricing in `/ofc/info` for informational purposes:
```json
{
  "pricing": {
    "per_hour": 3.00,
    "currency": "USD"
  }
}
```

For now, most agents will be free (open source, self-hosted). This metadata is informational only.

---

## Registry API (Creator → Registry, Anyone → Registry)

The Registry is where agents are registered and discovered.

### POST /registry/agents

Register a new agent. Requires creator authentication.

**Request:**
```json
{
  "id": "@code",
  "endpoint": "https://code-agent.example.com/ofc",
  "public_key": "-----BEGIN PUBLIC KEY-----...",
  "metadata": {
    "name": "Code Assistant",
    "tagline": "Send me anything that needs code!",
    "description": "Expert programmer. Writes scripts, runs analysis...",
    "capabilities": ["bash", "python", "sql", "visualization"],
    "examples": ["Write a script to...", "Debug this function..."]
  }
}
```

**Response:**
```json
{
  "agent_id": "@code",
  "agent_token": "ofc_agent_xyz...",
  "status": "active",
  "registry": "registry.ofc.dev"
}
```

### GET /registry/agents/{agent_id}

Get agent info. Public endpoint.

**Response:**
```json
{
  "id": "@code",
  "endpoint": "https://code-agent.example.com/ofc",
  "owner": "codeai-inc",
  "status": "active",
  "metadata": { ... },
  "stats": {
    "floors_active": 142,
    "avg_rating": 4.8,
    "total_hours": 5420
  }
}
```

### PATCH /registry/agents/{agent_id}

Update agent metadata. Requires creator authentication.

```json
{
  "metadata": {
    "tagline": "Updated tagline!"
  }
}
```

### DELETE /registry/agents/{agent_id}

Remove agent from registry. Requires creator authentication.

Active floors using this agent will be notified.

### GET /registry/agents/search

Search for agents. Used by floor managers for discovery.

**Query params:** `?q=data+analysis&capabilities=python`

**Response:**
```json
{
  "results": [
    {
      "id": "@data",
      "tagline": "Senior data analyst",
      "rating": 4.9
    },
    {
      "id": "@code",
      "tagline": "Send me anything that needs code!",
      "rating": 4.8
    }
  ]
}
```

---

## Multi-Tenancy & Authentication

OFC is designed for multi-tenant operation from the start. A single agent endpoint may serve thousands of floors across multiple OFC servers.

### The Players

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ OFC Registry│     │ Floor Server│     │ Agent Host  │
│ (identity)  │     │ (ofc.dev)   │     │(@code.ai)   │
└─────────────┘     └─────────────┘     └─────────────┘
       │                   │                   │
       │   ┌───────────────┴───────────────┐   │
       │   │           Users               │   │
       │   └───────────────────────────────┘   │
```

### Agent Registration

Agent creators register their agents with an OFC Registry:

```json
POST /registry/agents

Headers:
  Authorization: Bearer <creator_token>

{
  "id": "@code",
  "endpoint": "https://code-agent.example.com/ofc",
  "owner": "codeai-inc",
  "public_key": "-----BEGIN PUBLIC KEY-----...",
  "metadata": {
    "tagline": "Send me anything that needs code!",
    "description": "Expert programmer...",
    "capabilities": ["bash", "python", "sql"]
  }
}

Response:
{
  "agent_id": "@code",
  "agent_token": "ofc_agent_xyz...",
  "registry": "registry.ofc.dev",
  "status": "active"
}
```

### Request Authentication

Every request to an agent includes signed context:

```json
POST /ofc/respond

Headers:
  Authorization: Bearer <floor_server_token>
  X-OFC-Floor-ID: floor-123
  X-OFC-Server: https://floors.ofc.dev
  X-OFC-Signature: <signed request hash>

Body:
{
  "floor_id": "floor-123",
  "user_id": "user-456",
  "messages": [...],
  "context": {
    "server": "https://floors.ofc.dev",
    "server_pubkey": "...",
    "timestamp": "2024-01-15T10:30:00Z",
    "nonce": "abc123"
  }
}
```

Agents verify:
1. Signature matches (request not tampered)
2. Floor server is trusted (known public key or registry lookup)
3. Timestamp is recent (prevent replay)

### Floor Server Authentication

When agents call back to floor servers (e.g., to use MCPs):

```json
POST /floors/123/mcp/sandbox/execute

Headers:
  Authorization: Bearer <agent_token>
  X-OFC-Agent-ID: @code
  X-OFC-Signature: <signed request>
```

### Multi-Floor Agent State

Agents handle multiple floors by scoping state:

```
Agent internal state:
  floors/
    floor-123/
      history.json
      preferences.json
    floor-456/
      history.json
      preferences.json
```

Each request includes `floor_id` - agents MUST isolate state between floors.

### Token Types

| Token | Issued By | Used For |
|-------|-----------|----------|
| `agent_token` | Registry | Agent proves identity to floors |
| `floor_token` | Floor server | Floor proves identity to agents |
| `user_token` | Floor server | User auth (login) |
| `session_token` | Floor server | Scoped to one floor session |

### Federation

Multiple OFC registries can exist and cross-trust:

```
registry.ofc.dev (main)
    ↔ registry.eu.ofc.dev (EU)
    ↔ registry.corp.example.com (private)
```

Agents registered on one registry can be used on floors from another, if trust is established.

---

## Security Considerations

- Signed requests prevent tampering and replay attacks
- Floor tokens prevent unauthorized agent invocation
- Agent tokens prevent impersonation
- Per-floor state isolation prevents cross-tenant leakage
- Tool execution should be sandboxed
- Rate limiting on /should-wake polling

---

## Open Questions

### MCP Type Taxonomy

MCP defines *how* to connect to tools, but there's no standard taxonomy of *what kinds* of tools. OFC would benefit from standardized MCP types:

| Type | Interface | Examples |
|------|-----------|----------|
| `filesystem` | read, write, list, delete | Local disk, S3, Google Drive |
| `executor` | run, kill, status | Docker, shell, Lambda |
| `database` | query, insert, update | Postgres, SQLite, DuckDB |
| `http` | get, post, request | Fetch, curl |
| `state` | get, set, delete | Redis, KV store |
| `canvas` | draw, render, export | Matplotlib, D3 |
| `messaging` | send, receive | Email, Slack, SMS |

**Benefits:**
- Agents request by type: "I need a `filesystem`"
- Floors provide any implementation
- Swappable backends (S3 today, GCS tomorrow)
- Clear capability matching

**Discovery needed:** Does MCP have this? Is there existing work? Should OFC define it?

---

## Future Extensions

- **Presence**: Agent online/offline status, health checks
- **Capabilities**: Negotiate available tools per floor
- **Federation**: Floors spanning multiple servers
- **Encryption**: End-to-end encrypted floors
- **MCP Type Registry**: Standard taxonomy of MCP interfaces

---

## Product Ladder

### The Core Insight: Blueprint = The Product

OFC is not a library. It's a **team composer**.

```
blueprint.yaml
├── Agents (from hub, local, or remote)
├── Workstations (MCPs: sandbox, filesystem, APIs)
├── Orchestration (turn-taking, access control)
└── = A multi-agent team that solves problems
```

**Two ways to build, always in sync:**

```
Conversational                     Declarative
─────────────                     ───────────
@floor-manager                    blueprint.yaml

"add @coder"         ←── sync ──→  agents:
"add a sandbox"                      - @coder
"give @coder access"               workstations:
                                     - sandbox

"save as blueprint"  ───────────→  (exports yaml)
"load my-team.yaml"  ←───────────  (imports yaml)
```

- Start conversationally → export to blueprint
- Start with blueprint → modify via @floor-manager
- Version control your blueprints
- Share, reproduce, iterate

### The Ladder

```
┌─────────────────────────────────────────┐
│          Agent Hub (hub.ofc.dev)        │  Step 4: Discovery
│   (search, pull, publish agents)        │  Audience: Community
└─────────────────────────────────────────┘
┌─────────────────────────────────────────┐
│          Local Web UI                   │  Step 3: "Open WebUI moment"
│   (ofc serve → browser)                 │  Audience: Power users
└─────────────────────────────────────────┘
┌─────────────────────────────────────────┐
│          CLI App                        │  Step 2: The main product ← WE ARE HERE
│   (ofc run blueprint.yaml)              │  Audience: Developers
└─────────────────────────────────────────┘
┌─────────────────────────────────────────┐
│          Python SDK                     │  Step 1: For experimentation
│   (rapid prototyping)                   │  Audience: Contributors
└─────────────────────────────────────────┘
```

### Step 1: SDKs for Builders (Future)

For those building custom agents that don't fit the standard agent.yaml format:

```bash
pip install ofc-python      # Python
npm install @ofc/sdk        # JavaScript
go get github.com/ofc/sdk   # Go
```

```python
from ofc import Agent

class MyAgent(Agent):
    id = "@myagent"

    def respond(self, messages, context):
        # Your logic here
        return Response(content="...")
```

Publish to hub, use in any blueprint.

**Future idea:** `ofc scaffold agent.yaml` could generate SDK boilerplate from an agent definition.

### Step 2: CLI App ← **We are here**

Single binary, no dependencies:

```bash
brew install ofc  # or download from releases

# Run a blueprint
ofc run                                 # Run ./blueprint.yaml
ofc run -f my-team.yaml                 # Run specific blueprint
ofc run -f blueprint.yaml "prompt"      # One-shot mode with initial prompt

# Create new blueprint
ofc init
```

Interactive terminal UI with colors, streaming, @mentions, tool execution.

**Planned:**
```bash
ofc pull aioverlords/coder-qwen3        # Pull agent from hub
ofc run coder                           # Run single agent
ofc run --agents @data @code            # Ad-hoc floor
```

### Step 3: Local Web UI

The "Open WebUI moment" - accessible to non-coders:

```bash
ofc serve
# Opens browser → localhost:3711
```

- Chat interface
- See agents, their status
- Manage workstations
- Load/save blueprints
- Still fully local, your machine, your data

### Step 4: Agent Hub (hub.ofc.dev)

Open registry for **agents only** - like Docker Hub for images:

```bash
# Pull agents from hub
ofc pull aioverlords/coder-qwen3
ofc pull webcorp/scraper
ofc pull ailabs/data-analyst

# Search
ofc search "data analysis"
ofc search "web scraping" --model=qwen3
```

**Conversational discovery via floor-manager:**
```
You: @floor-manager? I need an agent for web scraping

@floor-manager: Found on hub.ofc.dev:
  - @scraper by webtools-inc (pip: ofc-agent-scraper)
  - @crawler by datacorp (docker: ofc/crawler)
  - @webgrab (remote: https://webgrab.ai/ofc)

Want me to add @scraper to this floor?
```

**Why agents only, not blueprints?**
- Agents are composable primitives (like Docker images)
- Blueprints are specific solutions (like docker-compose files)
- Blueprints live in GitHub repos, project directories
- Hub focuses on the reusable building blocks

**Features:**
- Agent registry (agent.yaml + source: pip, docker, remote)
- Search and discover
- Ratings, reviews, verified publishers
- Integrated into floor-manager

**What it's not:**
- No agent hosting (runs locally or on publisher's infra)
- No blueprints (share those via GitHub)

Optional service - local agents work fine without it.

---

## Packaging Strategy

### The Tool: `ofc`

The `ofc` CLI is the product. Written in Go or Rust for:
- Fast, single binary
- `brew install ofc` / `apt install ofc` / `winget install ofc`
- No runtime dependencies

```bash
brew install ofc
ofc pull aioverlords/coder-qwen3
ofc run blueprint.yaml
ofc serve  # Web UI
```

### Agents: Any Language

Agents can be written in any language. The tool pulls and runs them:

| Source | How it works |
|--------|--------------|
| pip | `ofc` manages a Python venv, installs package |
| npm | `ofc` manages node_modules, installs package |
| docker | `ofc` pulls and runs container |
| remote | `ofc` calls HTTP endpoint |
| local file | `ofc` loads agent.yaml + runs |

### SDKs: Per-Language Helpers

Optional libraries for building agents:

```bash
pip install ofc-python      # Python SDK
npm install @ofc/sdk        # JavaScript SDK
go get github.com/ofc/sdk   # Go SDK
```

```python
# Python
from ofc import Agent

class MyAgent(Agent):
    id = "@myagent"
    def respond(self, messages, context):
        ...
```

```typescript
// TypeScript
import { Agent } from '@ofc/sdk';

class MyAgent extends Agent {
  id = "@myagent";
  respond(messages, context) { ... }
}
```

**The pattern:**
- `ofc` = the tool (Go/Rust, language-agnostic)
- Agents = any language (Python, JS, Go, Docker, remote)
- SDKs = convenience libraries per language

Just like Docker: `docker` is Go, containers are anything.

### For Containerized Deployment

```bash
docker pull ofc/ofc
docker run -p 3711:3711 -v ./workspace:/workspace ofc/ofc serve
```

---

## Implementation Modes

OFC is designed to work at multiple scales, from local Python apps to fully distributed systems.

### Agent & Blueprint Configuration

**agent.yaml** - defines a single agent (like Dockerfile):
```yaml
id: "@coder"
name: "Qwen3 Coder"
model: qwen3:32b
endpoint: http://localhost:11434/v1  # ollama

prompt: |
  You are an expert programmer. Write clean, working code.
  Use the tools available to execute and test your code.

activation: mention
memory: summary        # buffer, window, summary, rag
context_window: 20     # messages

tools:
  - bash
  - filesystem
```

**blueprint.yaml** - THE core abstraction. Defines a complete multi-agent team:
```yaml
name: data-analysis
description: "Data analysis team with analyst and coder"

agents:
  - aioverlords/coder-qwen3       # From hub
  - ./my-analyst.agent.yaml       # Local file
  - id: "@helper"                 # Inline definition
    model: gpt-4o-mini
    prompt: "You help with small tasks"
    activation: mention

workstations:
  - type: sandbox
    image: python:3.11
    mount: ./workspace:/workspace
  - type: filesystem
    path: ./workspace

# Optional: floor-manager behavior
floor_manager:
  auto_approve_read: true         # Don't ask for read access
  default_agent: "@analyst"       # Who responds first
```

**This is the product:**
```bash
ofc run blueprint.yaml            # Run your team
ofc run                           # Run ./blueprint.yaml by default
```

**Conversational ↔ Declarative:**
```
You: @floor-manager? save this as my-team.yaml

@floor-manager: Saved! Blueprint includes:
  - @coder (aioverlords/coder-qwen3)
  - @analyst (local)
  - sandbox workstation

  Run anytime with: ofc run my-team.yaml
```

### Agent Packaging

Agents can be distributed via standard package managers:

**pip (Python agents):**
```bash
pip install ofc-agent-code
pip install ofc-agent-data
```
```python
from ofc_agent_code import CodeAgent

floor.add_agent(CodeAgent(model="claude-3", api_key="..."))
```

**Docker (containerized agents):**
```bash
docker pull ofc/agent-code
```
```python
floor.add_agent(DockerAgent("ofc/agent-code"))
```

**Local development:**
```python
# Just subclass Agent
class MyCustomAgent(Agent):
    id = "@myagent"
    def respond(self, messages, context):
        ...
```

### Workspaces and Sandboxes

The Docker sandbox is a core feature from v1. Data exchange via mounted volumes:

```
Local filesystem:        Container sees:
─────────────────        ───────────────
./workspace/             /workspace/
  ├── data.csv             ├── data.csv
  ├── scripts/             ├── scripts/
  └── output/              └── output/
```

**Two MCPs work together:**

| MCP | Purpose |
|-----|---------|
| `filesystem` | Read/write files in workspace |
| `sandbox` | Execute code with workspace mounted |

```python
floor = Floor("analysis")
floor.add_workstation("files", FilesystemMCP("./workspace"))
floor.add_workstation("sandbox", DockerSandbox(
    image="python:3.11",
    mount={"./workspace": "/workspace"}
))
```

**Flow:**
1. User drops `data.csv` in `./workspace`
2. Agent uses `filesystem` MCP to read it
3. Agent uses `sandbox` MCP to run analysis
4. Results written to `./workspace/output/`
5. User retrieves results

This keeps sandboxed execution (safe) while enabling data exchange (useful).

### Deployment Modes

OFC scales from local development to fully distributed:

```
Local           → Hybrid              → Distributed
─────────────────────────────────────────────────────
CLI + local LLM   CLI + remote APIs    Floor servers + Agent services
(ollama)          (OpenAI, etc.)       (HTTP protocol)
```

- **Local**: Everything in-process, `ofc run` with local models
- **Hybrid**: Local orchestration, remote LLM APIs (OpenAI, Anthropic, etc.)
- **Distributed**: Full HTTP protocol, multi-tenant floor servers, remote agents

Same blueprint.yaml works at every level. Start local, scale when needed.

---

## Reference Implementation

The repository is structured as a monorepo:

```
ofc/
├── cli/           # Go CLI (production binary)
├── python/        # Python prototype (experimentation)
│   ├── ofc/       # Blueprint loading, floor runtime
│   └── chatroom/  # Agent implementations, LLM client, sandbox
├── examples/      # Example blueprints
└── PROTOCOL.md    # This document
```

- **Go CLI** (`cli/`): The `ofc` binary. Streaming, tools, @mentions, debug mode.
- **Python** (`python/`): Rapid prototyping. Same features, easier to hack on.

Both implement the local mode with turn-taking protocol. The HTTP APIs for distributed mode are specified but not yet implemented.
