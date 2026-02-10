#!/usr/bin/env python3
"""OFC CLI - Open Floor Control command line interface."""

import argparse
import sys
from pathlib import Path

from . import __version__
from .floor import run_blueprint


def main():
    parser = argparse.ArgumentParser(
        prog="ofc",
        description="OFC - Open Floor Control. Compose and run multi-agent teams.",
    )
    parser.add_argument(
        "--version", "-v",
        action="version",
        version=f"ofc {__version__}",
    )

    subparsers = parser.add_subparsers(dest="command", help="Commands")

    # ofc run
    run_parser = subparsers.add_parser("run", help="Run a floor")
    run_parser.add_argument(
        "prompt",
        nargs="?",
        help="Initial prompt to run (optional)",
    )
    run_parser.add_argument(
        "-f", "--file",
        default="blueprint.yaml",
        help="Blueprint file (default: blueprint.yaml)",
    )
    run_parser.add_argument(
        "--debug",
        action="store_true",
        help="Enable debug output",
    )

    # ofc init
    init_parser = subparsers.add_parser("init", help="Create a new blueprint")
    init_parser.add_argument(
        "name",
        nargs="?",
        default="my-floor",
        help="Name for the blueprint",
    )

    # ofc list (future: list agents from hub)
    # ofc pull (future: pull agent from hub)

    args = parser.parse_args()

    if args.command == "run":
        blueprint_path = Path(args.file)
        if not blueprint_path.exists():
            print(f"Error: Blueprint not found: {blueprint_path}")
            print(f"Create one with: ofc init")
            sys.exit(1)
        run_blueprint(str(blueprint_path), initial_prompt=args.prompt, debug=args.debug)

    elif args.command == "init":
        create_blueprint(args.name)

    else:
        parser.print_help()


def create_blueprint(name: str):
    """Create a new blueprint file."""
    filename = "blueprint.yaml"
    if Path(filename).exists():
        print(f"Error: {filename} already exists")
        sys.exit(1)

    template = f'''# OFC Blueprint - {name}
# Run with: ofc run

name: {name}
description: "Describe your floor here"

defaults:
  endpoint: http://localhost:11434/v1
  model: llama3

agents:
  - id: "@assistant"
    name: "Assistant"
    activation: always
    can_use_tools: true
    temperature: 0.7
    prompt: |
      You are a helpful assistant.
      Keep responses concise and helpful.

workstations:
  - type: sandbox
    name: python-sandbox
    image: python:3.11-slim
    mount: ./workspace:/workspace
'''
    with open(filename, "w") as f:
        f.write(template)

    print(f"Created {filename}")
    print(f"Run with: ofc run")


if __name__ == "__main__":
    main()
