#!/usr/bin/env python3
"""Parse an OFC blueprint.yaml file."""

import yaml
import sys
from pathlib import Path


def parse_blueprint(path: str = "blueprint.yaml") -> dict:
    """Parse a blueprint YAML file and return the parsed structure."""
    with open(path) as f:
        blueprint = yaml.safe_load(f)
    return blueprint


def main():
    """Load and print a summary of a blueprint file given as CLI argument."""
    path = sys.argv[1] if len(sys.argv) > 1 else "blueprint.yaml"
    if not Path(path).exists():
        print(f"Error: {path} not found", file=sys.stderr)
        sys.exit(1)

    bp = parse_blueprint(path)
    print(f"Blueprint: {bp['name']}")
    print(f"Description: {bp['description']}")
    print(f"Agents ({len(bp.get('agents', []))}):")
    for agent in bp.get("agents", []):
        print(f"  - {agent['id']} ({agent['name']}): type={agent['type']}, activation={agent['activation']}")


if __name__ == "__main__":
    main()
