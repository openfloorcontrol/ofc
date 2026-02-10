#!/usr/bin/env python3
"""Multi-Agent Chatroom - Main entry point."""

from .models import Message
from .agents import Room, create_default_agents, next_recipient, get_agent
from .sandbox import Sandbox
from .llm import get_agent_response
from . import ui


def main_loop(room: Room):
    """Main conversation loop."""
    while True:
        try:
            # Styled user prompt
            print()
            ui.print_agent_label("@user")
            user_input = input().strip()
        except (EOFError, KeyboardInterrupt):
            ui.print_system("Interrupted")
            break

        if not user_input:
            continue

        if user_input == "/quit":
            break

        if user_input == "/clear":
            room.messages.clear()
            ui.print_system("Conversation cleared")
            continue

        # Add user message
        user_msg = Message.create("@user", user_input)
        room.messages.append(user_msg)

        # Agent loop - keep going until it's the user's turn again
        current_msg = user_msg
        passed_agents: set[str] = set()  # Track who passed for this turn

        while True:
            next_agent_id = next_recipient(current_msg, room, exclude=passed_agents)

            if next_agent_id is None:
                break  # Back to user

            agent = get_agent(next_agent_id, room)
            if agent is None:
                ui.print_system(f"Unknown agent {next_agent_id}")
                break

            # Show thinking indicator, then get response
            # (llm.py will clear it and print the actual response)
            print()
            ui.print_agent_label(agent.id)
            ui.print_thinking()

            response = get_agent_response(agent, room)

            # Check for pass (case-insensitive)
            if "[pass]" in response.content.strip().lower():
                passed_agents.add(agent.id)
                continue

            # Agent responded - clear passed set for new context
            passed_agents.clear()
            room.messages.append(response)
            current_msg = response


def main():
    """Entry point."""
    agents = create_default_agents()

    print(f"{ui.BOLD}{'=' * 50}{ui.RESET}")
    print(f"{ui.BOLD}Multi-Agent Chatroom v0.0.1{ui.RESET}")
    print(f"Agents: {', '.join(ui.get_agent_color(a.id) + a.id + ui.RESET for a in agents)}")
    print(f"Type {ui.BOLD}/quit{ui.RESET} to exit, {ui.BOLD}/clear{ui.RESET} to reset")
    print(f"{ui.BOLD}{'=' * 50}{ui.RESET}")

    with Sandbox(workspace_dir="./workspace") as sandbox:
        room = Room(agents=agents, sandbox=sandbox)
        main_loop(room)

    print(f"\n{ui.DIM}Goodbye! 👋{ui.RESET}")


if __name__ == "__main__":
    main()
