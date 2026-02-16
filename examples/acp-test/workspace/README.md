# ACP Test Workspace

This is the shared workspace for the `acp-test` OFC example. It tests ACP (Agent Communication Protocol) integration with Claude Code.

## Project Structure

- `blueprint.yaml` - OFC blueprint defining agents and workstations
- `parse_blueprint.py` - Utility script to parse and summarize blueprint files
- `sandbox/Dockerfile` - Python 3.11 sandbox with data science packages (pandas, numpy, matplotlib, duckdb, scikit-learn)
- `workspace/` - Shared mount point between host and sandbox container

## Usage

Run the floor with:

```sh
ofc run
```

This starts the `@claude` agent (via `claude-code-acp`) and mounts this workspace into the Python sandbox at `/workspace`.
