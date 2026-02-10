"""Blueprint loading and parsing."""

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

# Reuse existing models
from chatroom.models import AgentConfig


@dataclass
class WorkstationConfig:
    """Configuration for a workstation (MCP)."""
    type: str  # "sandbox", "filesystem", etc.
    name: str
    # Type-specific config
    image: str | None = None
    dockerfile: str | None = None
    mount: str | None = None
    path: str | None = None


@dataclass
class Blueprint:
    """A complete floor configuration."""
    name: str
    description: str
    agents: list[AgentConfig]
    workstations: list[WorkstationConfig]
    defaults: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_file(cls, path: str | Path) -> "Blueprint":
        """Load a blueprint from a YAML file."""
        path = Path(path)
        with open(path) as f:
            data = yaml.safe_load(f)
        return cls.from_dict(data, base_path=path.parent)

    @classmethod
    def from_dict(cls, data: dict, base_path: Path | None = None) -> "Blueprint":
        """Parse a blueprint from a dictionary."""
        defaults = data.get("defaults", {})
        default_endpoint = defaults.get("endpoint", "http://localhost:11434/v1")
        default_model = defaults.get("model", "llama3")

        # Parse agents
        agents = []
        for agent_data in data.get("agents", []):
            agent = AgentConfig(
                id=agent_data["id"],
                model=agent_data.get("model", default_model),
                endpoint=agent_data.get("endpoint", default_endpoint),
                api_key=agent_data.get("api_key"),
                system_prompt=agent_data.get("prompt", ""),
                activation=agent_data.get("activation", "mention"),
                can_use_tools=agent_data.get("can_use_tools", False),
                temperature=agent_data.get("temperature", 0.7),
                tool_context=agent_data.get("tool_context", "full"),
            )
            agents.append(agent)

        # Parse workstations
        workstations = []
        for ws_data in data.get("workstations", []):
            ws = WorkstationConfig(
                type=ws_data.get("type", "sandbox"),
                name=ws_data.get("name", "default"),
                image=ws_data.get("image"),
                dockerfile=ws_data.get("dockerfile"),
                mount=ws_data.get("mount"),
                path=ws_data.get("path"),
            )
            workstations.append(ws)

        return cls(
            name=data.get("name", "unnamed"),
            description=data.get("description", ""),
            agents=agents,
            workstations=workstations,
            defaults=defaults,
        )


def load_blueprint(path: str = "blueprint.yaml") -> Blueprint:
    """Load a blueprint from file."""
    return Blueprint.from_file(path)
