"""Floor - the runtime for a blueprint."""

from pathlib import Path

from chatroom.models import Message
from chatroom.agents import Room, next_recipient, get_agent
from chatroom.sandbox import Sandbox
from chatroom.llm import get_agent_response
from chatroom import ui

from .blueprint import Blueprint, load_blueprint


class Floor:
    """A running floor instance."""

    def __init__(self, blueprint: Blueprint, workspace_dir: str = "./workspace"):
        self.blueprint = blueprint
        self.workspace_dir = workspace_dir
        self.sandbox: Sandbox | None = None
        self.room: Room | None = None

    def __enter__(self):
        """Start the floor (initialize sandbox, etc.)."""
        # Find sandbox workstation config
        sandbox_config = None
        for ws in self.blueprint.workstations:
            if ws.type == "sandbox":
                sandbox_config = ws
                break

        if sandbox_config:
            # Parse mount if present (format: "./local:/container")
            workspace = self.workspace_dir
            if sandbox_config.mount:
                parts = sandbox_config.mount.split(":")
                if parts:
                    workspace = parts[0]

            # Note: Sandbox currently uses config-level constants for dockerfile
            # TODO: Allow passing dockerfile_dir to Sandbox
            self.sandbox = Sandbox(workspace_dir=workspace)
            self.sandbox.__enter__()

        self.room = Room(
            agents=self.blueprint.agents,
            sandbox=self.sandbox,
        )
        return self

    def __exit__(self, *args):
        """Cleanup."""
        if self.sandbox:
            self.sandbox.__exit__(*args)

    def run(self, initial_prompt: str | None = None):
        """Run the interactive floor loop."""
        bp = self.blueprint

        print(f"{ui.BOLD}{'=' * 50}{ui.RESET}")
        print(f"{ui.BOLD}OFC - {bp.name}{ui.RESET}")
        if bp.description:
            print(f"{ui.DIM}{bp.description}{ui.RESET}")
        print(f"Agents: {', '.join(ui.get_agent_color(a.id) + a.id + ui.RESET for a in bp.agents)}")
        print(f"Type {ui.BOLD}/quit{ui.RESET} to exit, {ui.BOLD}/clear{ui.RESET} to reset")
        print(f"{ui.BOLD}{'=' * 50}{ui.RESET}")

        self._main_loop(initial_prompt=initial_prompt)

        print(f"\n{ui.DIM}Goodbye! ofc. 🎤{ui.RESET}")

    def _main_loop(self, initial_prompt: str | None = None):
        """Main conversation loop."""
        one_shot = initial_prompt is not None
        first_iteration = True

        while True:
            # Handle initial prompt on first iteration
            if first_iteration and initial_prompt:
                user_input = initial_prompt
                print()
                ui.print_agent_label("@user")
                print(user_input)
                first_iteration = False
            else:
                # If we ran with a prompt, exit after it's done
                if one_shot:
                    break

                try:
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
                self.room.messages.clear()
                ui.print_system("Conversation cleared")
                continue

            # Add user message
            user_msg = Message.create("@user", user_input)
            self.room.messages.append(user_msg)

            # Agent loop
            current_msg = user_msg
            passed_agents: set[str] = set()

            while True:
                next_agent_id = next_recipient(current_msg, self.room, exclude=passed_agents)

                if next_agent_id is None:
                    break

                agent = get_agent(next_agent_id, self.room)
                if agent is None:
                    ui.print_system(f"Unknown agent {next_agent_id}")
                    break

                print()
                ui.print_agent_label(agent.id)
                ui.print_thinking()

                response = get_agent_response(agent, self.room)

                if "[pass]" in response.content.strip().lower():
                    passed_agents.add(agent.id)
                    continue

                passed_agents.clear()
                self.room.messages.append(response)
                current_msg = response


def run_blueprint(path: str = "blueprint.yaml", initial_prompt: str | None = None, debug: bool = False):
    """Load and run a blueprint."""
    # Set debug mode in agents module
    from chatroom import agents
    agents.DEBUG = debug

    blueprint = load_blueprint(path)
    with Floor(blueprint) as floor:
        floor.run(initial_prompt=initial_prompt)
