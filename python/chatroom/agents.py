"""Agent configurations and activation logic."""

import re
from dataclasses import dataclass, field

from .models import AgentConfig, Message
from .config import DEFAULT_ENDPOINT, DEFAULT_MODEL
from .sandbox import Sandbox

# Debug flag - set by ofc CLI
DEBUG = False


def debug(msg: str):
    """Print debug message if DEBUG is enabled."""
    if DEBUG:
        print(f"  [debug] {msg}")


@dataclass
class Room:
    """The chatroom state."""
    agents: list[AgentConfig]
    messages: list[Message] = field(default_factory=list)
    sandbox: Sandbox | None = None


def extract_mentions(content: str) -> list[str]:
    """
    Extract @mentions that request a response (with ?).
    "@code?" → triggers response
    "@code" → just a reference, no trigger
    """
    pattern = r'@(\w+)\?'
    matches = re.findall(pattern, content)
    return [f"@{m}" for m in matches]


def is_mentioned(agent_id: str, content: str) -> bool:
    """Check if an agent is mentioned in content."""
    return agent_id in extract_mentions(content)


def get_last_message_from(agent_id: str, messages: list[Message]) -> Message | None:
    """Get the most recent message from a specific agent."""
    for msg in reversed(messages):
        if msg.from_id == agent_id:
            return msg
    return None


def get_agent_ids(room: Room) -> set[str]:
    """Get all agent IDs in the room."""
    return {a.id for a in room.agents}


def should_wake(agent: AgentConfig, message: Message, room: Room) -> bool:
    """Determine if an agent should respond to a message."""
    # Never respond to own messages
    if message.from_id == agent.id:
        return False

    # Check if message explicitly @mentions any agents
    mentions = set(extract_mentions(message.content))
    mentions.discard(message.from_id)  # Ignore self-mentions
    agent_mentions = mentions & get_agent_ids(room)  # Only mentions that are actual agents

    # If specific agents are mentioned, ONLY those agents respond
    # This lets user bypass "always" agents by directly addressing someone
    if agent_mentions:
        return agent.id in agent_mentions

    # "Awaiting response" - if I mentioned the speaker in my last message,
    # I'm interested in their reply (but ignore self-mentions)
    my_last_msg = get_last_message_from(agent.id, room.messages)
    if my_last_msg:
        my_mentions = set(extract_mentions(my_last_msg.content))
        my_mentions.discard(agent.id)  # Ignore if I mentioned myself
        if message.from_id in my_mentions:
            return True

    # No specific mentions - check activation mode
    if agent.activation == "always":
        return True

    # "mention" agents only wake if explicitly mentioned (already handled above)
    return False


def find_initiator(msg: Message, room: Room) -> str | None:
    """Find who @mentioned the speaker, triggering this response."""
    for prev_msg in reversed(room.messages[:-1]):
        if msg.from_id in extract_mentions(prev_msg.content):
            return prev_msg.from_id
        if prev_msg.from_id == msg.from_id:
            # Hit speaker's previous message, stop looking
            break
    return None


def mentions_user(content: str) -> bool:
    """Check if content mentions @user."""
    return "@user" in content.lower()


def next_recipient(completed_msg: Message, room: Room, exclude: set[str] | None = None) -> str | None:
    """After a message completes, who should respond next?"""
    exclude = exclude or set()
    debug(f"next_recipient: from={completed_msg.from_id}, mentions={extract_mentions(completed_msg.content)}, exclude={exclude}")

    # 0. If the message mentions @user, wait for user input
    if completed_msg.from_id != "@user" and mentions_user(completed_msg.content):
        debug("→ pausing for @user")
        return None  # Pause for user

    # 1. Who initiated this turn?
    initiator = find_initiator(completed_msg, room)
    if initiator and initiator != "@user":
        debug(f"→ initiator: {initiator}")
        return initiator

    # 2. Check if any agent wants to wake
    for agent in room.agents:
        if agent.id in exclude:
            debug(f"should_wake({agent.id}): skipped (passed)")
            continue
        wake = should_wake(agent, completed_msg, room)
        debug(f"should_wake({agent.id}): {wake}")
        if wake:
            return agent.id

    # 3. Nobody wants it, back to user
    debug("→ back to user")
    return None


def get_agent(agent_id: str, room: Room) -> AgentConfig | None:
    """Get an agent by ID."""
    for agent in room.agents:
        if agent.id == agent_id:
            return agent
    return None


# Default agent configurations
def create_default_agents() -> list[AgentConfig]:
    """Create the default set of agents."""
    return [
        AgentConfig(
            id="@data",
            model=DEFAULT_MODEL,
            endpoint=DEFAULT_ENDPOINT,
            api_key=None,
            system_prompt="""You are @data, a senior data analyst in a multi-agent chatroom.

Other participants:
- @user: The human you're helping
- @code: A programmer for complex coding tasks

If you want someone to respond, write "@name?" (with the question mark), e.g.: "Hey @code? can you build a visualization for this?"

Your role:
- Lead data analysis conversations
- Understand what @user wants to achieve
- Break down analysis into steps
- Interpret results and guide next steps
- Be SKEPTICAL of results - check for empty data, NaNs, suspicious patterns

Tools:
You have a bash tool. Use it for QUICK exploration:
- Peek at files: head, cat, wc -l
- Quick queries: simple pandas one-liners, duckdb SQL
- Check structure: ls, file

For COMPLEX tasks, delegate to @code?:
- Multi-step analysis
- Building visualizations
- Writing scripts or files
- Anything over ~10 lines of code

Keep responses concise. Think out loud briefly, then act.

You're listening in to all conversations. If you have nothing to add, respond with exactly: [PASS]
""",
            activation="always",
            can_use_tools=True,
            temperature=0.7,
            tool_context="summary"  # Don't need full tool output, just summaries
        ),
        AgentConfig(
            id="@code",
            model=DEFAULT_MODEL,
            endpoint=DEFAULT_ENDPOINT,
            api_key=None,
            system_prompt="""You are @code, an expert programmer in a multi-agent chatroom.

Other participants:
- @user: The human
- @data: Data analyst who guides the analysis

Your role:
- Implement what's asked with code
- You have ONE tool: bash
- Use bash for EVERYTHING: read files (cat), write files (cat << 'EOF'), run code (python -c or scripts), list dirs (ls, fd, find), search (grep, rg)
- Show your commands clearly
- If something fails, try to fix it or report the error

You have pandas, numpy, matplotlib, duckdb at your disposal.

Keep responses SHORT. Do the work, show the result, done.
Don't explain what you're going to do - just do it.""",
            activation="mention",
            can_use_tools=True,
            temperature=0.2
        ),
    ]
