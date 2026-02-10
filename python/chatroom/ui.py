"""Terminal UI helpers with colors and formatting."""

import sys

# ANSI color codes
RESET = "\033[0m"
BOLD = "\033[1m"
DIM = "\033[2m"

# Colors
RED = "\033[31m"
GREEN = "\033[32m"
YELLOW = "\033[33m"
BLUE = "\033[34m"
MAGENTA = "\033[35m"
CYAN = "\033[36m"
WHITE = "\033[37m"

# Agent colors (consistent per agent)
AGENT_COLORS = {
    "@user": CYAN,
    "@data": MAGENTA,
    "@code": GREEN,
    "@reviewer": YELLOW,
}


def get_agent_color(agent_id: str) -> str:
    """Get consistent color for an agent."""
    return AGENT_COLORS.get(agent_id, WHITE)


def print_agent_label(agent_id: str, end: str = ""):
    """Print a styled agent label like [@data]:"""
    color = get_agent_color(agent_id)
    sys.stdout.write(f"{BOLD}{color}[{agent_id}]:{RESET} {end}")
    sys.stdout.flush()


def print_streaming_token(token: str):
    """Print a token during streaming."""
    sys.stdout.write(token)
    sys.stdout.flush()


def print_tool_call(cmd: str):
    """Print a tool call being executed."""
    print(f"\n  {DIM}${RESET} {BOLD}{cmd}{RESET}")


def print_tool_result(result: str, max_lines: int = 15):
    """Print tool result with truncation."""
    lines = result.split('\n')
    if len(lines) > max_lines:
        shown = lines[:max_lines]
        print(f"  {DIM}" + f"\n  ".join(shown) + f"{RESET}")
        print(f"  {DIM}... ({len(lines) - max_lines} more lines){RESET}")
    else:
        print(f"  {DIM}" + f"\n  ".join(lines) + f"{RESET}")


def print_thinking():
    """Print thinking indicator."""
    sys.stdout.write(f"{DIM}thinking...{RESET}")
    sys.stdout.flush()


def update_thinking_bytes(byte_count: int):
    """Update thinking indicator with byte count."""
    if byte_count < 1024:
        size_str = f"{byte_count}b"
    else:
        size_str = f"{byte_count / 1024:.1f}kb"
    sys.stdout.write(f"\r\033[K{DIM}receiving... {size_str}{RESET}")
    sys.stdout.flush()


def clear_thinking():
    """Clear the thinking indicator."""
    # Move back and clear
    sys.stdout.write("\r\033[K")
    sys.stdout.flush()


def print_system(msg: str):
    """Print a system message."""
    print(f"{DIM}[System]: {msg}{RESET}")


def print_error(msg: str):
    """Print an error message."""
    print(f"{RED}{BOLD}[Error]: {msg}{RESET}")
