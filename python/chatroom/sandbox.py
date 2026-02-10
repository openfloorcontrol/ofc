"""Docker-based sandbox for executing bash commands safely."""

import subprocess
from datetime import datetime
from pathlib import Path

from .config import SANDBOX_IMAGE, SANDBOX_TIMEOUT, SANDBOX_DOCKERFILE_DIR
from . import ui


def get_image_created_time() -> datetime | None:
    """Get the creation time of the Docker image."""
    result = subprocess.run(
        ["docker", "inspect", "-f", "{{.Created}}", SANDBOX_IMAGE],
        capture_output=True
    )
    if result.returncode != 0:
        return None

    # Parse ISO format: 2024-01-15T10:30:00.123456789Z
    created_str = result.stdout.decode().strip()
    try:
        # Handle nanoseconds by truncating to microseconds
        if "." in created_str:
            base, frac = created_str.replace("Z", "").split(".")
            frac = frac[:6]  # Keep only microseconds
            created_str = f"{base}.{frac}"
        return datetime.fromisoformat(created_str)
    except ValueError:
        return None


def get_dockerfile_modified_time() -> datetime | None:
    """Get the modification time of the Dockerfile."""
    dockerfile = Path(SANDBOX_DOCKERFILE_DIR) / "Dockerfile"
    if not dockerfile.exists():
        return None
    return datetime.fromtimestamp(dockerfile.stat().st_mtime)


def ensure_image_exists():
    """Build the sandbox image if it doesn't exist or Dockerfile changed."""
    dockerfile_dir = Path(SANDBOX_DOCKERFILE_DIR)
    if not dockerfile_dir.exists():
        ui.print_error(f"Dockerfile directory not found: {SANDBOX_DOCKERFILE_DIR}")
        return

    image_time = get_image_created_time()
    dockerfile_time = get_dockerfile_modified_time()

    needs_build = False
    if image_time is None:
        ui.print_system(f"Building sandbox image ({SANDBOX_IMAGE})...")
        needs_build = True
    elif dockerfile_time and dockerfile_time > image_time.replace(tzinfo=None):
        ui.print_system(f"Dockerfile changed, rebuilding sandbox image...")
        needs_build = True

    if not needs_build:
        return

    result = subprocess.run(
        ["docker", "build", "-t", SANDBOX_IMAGE, str(dockerfile_dir)],
        capture_output=True
    )
    if result.returncode == 0:
        ui.print_system("Sandbox image ready")
    else:
        ui.print_error(f"Failed to build image: {result.stderr.decode()}")


class Sandbox:
    """Docker-based sandbox for executing bash commands safely."""

    def __init__(self, workspace_dir: str | None = None):
        self.container_id: str | None = None
        self.workspace_dir = workspace_dir

    def start(self):
        """Start the sandbox container."""
        ensure_image_exists()

        cmd = [
            "docker", "run", "-d", "--rm",
            "-w", "/workspace",
            SANDBOX_IMAGE,
            "sleep", "infinity"
        ]
        result = subprocess.run(cmd, capture_output=True, check=True)
        self.container_id = result.stdout.decode().strip()
        ui.print_system(f"Sandbox started ({self.container_id[:12]})")

        # Copy workspace if provided
        if self.workspace_dir and Path(self.workspace_dir).exists():
            subprocess.run([
                "docker", "cp",
                f"{self.workspace_dir}/.",
                f"{self.container_id}:/workspace/"
            ], check=True)

    def execute(self, cmd: str, timeout: int = SANDBOX_TIMEOUT) -> str:
        """Execute a bash command and return output."""
        if not self.container_id:
            return "[ERROR: Sandbox not started]"

        try:
            result = subprocess.run(
                ["docker", "exec", self.container_id, "bash", "-c", cmd],
                capture_output=True,
                timeout=timeout
            )
            output = result.stdout.decode() + result.stderr.decode()

            # Truncate very long output
            if len(output) > 10000:
                output = output[:5000] + "\n... [truncated] ...\n" + output[-2000:]

            return output.strip() if output.strip() else "[no output]"
        except subprocess.TimeoutExpired:
            return f"[ERROR: Command timed out after {timeout}s]"
        except Exception as e:
            return f"[ERROR: {e}]"

    def stop(self):
        """Stop and remove the container."""
        if self.container_id:
            subprocess.run(
                ["docker", "kill", self.container_id],
                capture_output=True
            )
            self.container_id = None

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, *args):
        self.stop()
