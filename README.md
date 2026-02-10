# OFC - Open Floor Control 🎤

An open protocol for multi-agent conversation.

## What is OFC?

OFC (Open Floor Control) enables multiple AI agents to collaborate in structured conversations. Think of it like a meeting room where agents take turns, mention each other with `@`, and use shared tools called "workstations".

## Repository Structure

```
ofc/
├── cli/           # Go CLI (production binary)
├── python/        # Python SDK (experimentation)
│   ├── ofc/       # Core runtime
│   └── chatroom/  # Agent implementations
├── examples/      # Example blueprints
├── site/          # Website (ofc.dev)
└── PROTOCOL.md    # Full protocol specification
```

## Quick Start

### Install

```bash
brew tap openfloorcontrol/tap
brew install ofc
```

Or build from source:
```bash
cd cli && go build -o ofc .
```

### Run the Example

The `data-analysis` example uses [Ollama](https://ollama.com) cloud models. You'll need:
1. Ollama running locally (`ollama serve`)
2. Signed in to Ollama (`ollama login`) - free tier works fine

```bash
cd examples/data-analysis
ofc run "Analyze the sales data"
```

### Using the Python SDK

```bash
cd python
pip install -e .
ofc run -f ../examples/data-analysis/blueprint.yaml "Analyze the sales data"
```

## Blueprint.yaml

The core abstraction is the `blueprint.yaml` - like `docker-compose.yaml` for AI teams:

```yaml
name: data-analysis
agents:
  - id: "@data"
    activation: always
    can_use_tools: true
  - id: "@code"
    activation: mention
    can_use_tools: true
workstations:
  - type: sandbox
    image: python:3.11-slim
```

## Key Concepts

- **Floor**: A workspace where agents collaborate
- **Agents**: AI participants with defined roles and capabilities
- **Workstations**: Tools/MCPs available to agents (sandbox, filesystem, etc.)
- **Turn-taking**: Agents use `@mentions?` to invoke others, `[PASS]` to decline
- **@floor-manager**: Built-in agent for discovery and access control

## Development

- **Go CLI** (`cli/`): Production-ready, single binary distribution
- **Python SDK** (`python/`): Rapid experimentation and prototyping

Both implementations maintain feature parity. Experiment in Python, ship in Go.

## Protocol

See [PROTOCOL.md](PROTOCOL.md) for the full specification covering:
- HTTP APIs
- Turn-taking mechanics
- MCP integration
- Multi-tenancy
- Agent packaging

## Links

- Website: [ofc.dev](https://ofc.dev)
- Registry: hub.ofc.dev (coming soon)

---

ofc. 🎤
