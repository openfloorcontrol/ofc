#!/usr/bin/env python3
"""Countdown timer — pass minutes on the command line."""

import sys
import time


def parse_minutes():
    if len(sys.argv) != 2:
        print(f"Usage: {sys.argv[0]} <minutes>", file=sys.stderr)
        sys.exit(1)
    try:
        minutes = float(sys.argv[1])
    except ValueError:
        print("Error: argument must be a number", file=sys.stderr)
        sys.exit(1)
    if minutes <= 0:
        print("Error: minutes must be positive", file=sys.stderr)
        sys.exit(1)
    return minutes


# ANSI escape helpers
RED = "\033[91m"
RESET = "\033[0m"
FLASH = "\033[?5h"  # reverse video on
UNFLASH = "\033[?5l"  # reverse video off


def format_time(seconds_left):
    m, s = divmod(int(seconds_left), 60)
    return f"{m:02d}:{s:02d}"


def countdown(minutes):
    total = int(minutes * 60)
    try:
        for remaining in range(total, -1, -1):
            display = format_time(remaining)
            if remaining < 300:  # less than 5 minutes
                line = f"\r{RED}{display}{RESET} "
            else:
                line = f"\r{display} "
            sys.stdout.write(line)
            sys.stdout.flush()
            if remaining > 0:
                time.sleep(1)
    except KeyboardInterrupt:
        sys.stdout.write(RESET + "\n")
        print("Cancelled.")
        sys.exit(0)

    # Flash screen 5 times on completion
    sys.stdout.write("\n")
    for _ in range(5):
        sys.stdout.write(FLASH)
        sys.stdout.flush()
        time.sleep(0.15)
        sys.stdout.write(UNFLASH)
        sys.stdout.flush()
        time.sleep(0.15)
    print("Time's up!")


if __name__ == "__main__":
    minutes = parse_minutes()
    countdown(minutes)
