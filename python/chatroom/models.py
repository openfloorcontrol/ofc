"""Data classes for the multi-agent chatroom."""

from dataclasses import dataclass, field
from datetime import datetime
from typing import Literal
from uuid import uuid4


@dataclass
class ToolCall:
    """A tool invocation made by an agent."""
    name: str  # "bash"
    args: dict  # {"cmd": "..."}


@dataclass
class Message:
    """A message in the chatroom."""
    id: str
    from_id: str  # "@user", "@data", "@code", etc.
    content: str
    tool_calls: list[ToolCall] = field(default_factory=list)
    tool_results: list[str] = field(default_factory=list)
    timestamp: datetime = field(default_factory=datetime.now)

    @classmethod
    def create(cls, from_id: str, content: str) -> "Message":
        return cls(
            id=str(uuid4()),
            from_id=from_id,
            content=content,
        )


@dataclass
class AgentConfig:
    """Configuration for an LLM agent."""
    id: str  # "@data", "@code" - must start with @
    model: str  # "gpt-4o", "llama3", etc.
    endpoint: str  # OpenAI-compatible API endpoint
    api_key: str | None  # None for local models
    system_prompt: str
    activation: Literal["always", "mention"]
    can_use_tools: bool
    temperature: float = 0.7
    # How much tool call detail to show in context:
    # "full" = show everything (for oversight agents)
    # "summary" = first few lines of cmd/result (for coordinators like @data)
    # "none" = just show the text response, no tool details
    tool_context: Literal["full", "summary", "none"] = "full"
