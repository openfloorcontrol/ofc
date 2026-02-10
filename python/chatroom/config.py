"""Configuration and settings."""

import os

# LLM endpoints
OLLAMA_ENDPOINT = "http://localhost:11434/v1"
GEMINI_ENDPOINT = "https://generativelanguage.googleapis.com/v1beta/openai/"

# API keys (from environment)
GEMINI_API_KEY = os.environ.get("GEMINI_API_KEY")

# Default endpoint to use
DEFAULT_ENDPOINT = OLLAMA_ENDPOINT
DEFAULT_MODEL = "glm-4.7:cloud"

# Sandbox settings
SANDBOX_TIMEOUT = 30  # seconds
SANDBOX_IMAGE = "mac-sandbox:latest"
SANDBOX_DOCKERFILE_DIR = "./sandbox"

# Context settings
MAX_CONTEXT_MESSAGES = 50
