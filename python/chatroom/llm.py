"""LLM client for OpenAI-compatible APIs."""

import json
from uuid import uuid4

from openai import OpenAI

from .models import AgentConfig, Message, ToolCall
from .agents import Room
from .config import MAX_CONTEXT_MESSAGES
from . import ui


# Tool definition for bash
TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "bash",
            "description": "Run a bash command in the workspace container. Use for ALL file operations: reading (cat, head), writing (cat << EOF), editing (sed, or rewrite file), listing (ls, find, fd), searching (grep, rg), running code (python, node), etc.",
            "parameters": {
                "type": "object",
                "properties": {
                    "cmd": {
                        "type": "string",
                        "description": "The bash command to execute"
                    }
                },
                "required": ["cmd"]
            }
        }
    }
]


def summarize_lines(text: str, max_lines: int = 3) -> str:
    """Summarize text to first N lines."""
    lines = text.strip().split('\n')
    if len(lines) <= max_lines:
        return text.strip()
    return '\n'.join(lines[:max_lines]) + f"\n... ({len(lines) - max_lines} more lines)"


def format_tool_calls(msg: Message, level: str) -> str:
    """Format tool calls based on detail level."""
    if not msg.tool_calls or level == "none":
        return ""

    tool_parts = []
    for i, tc in enumerate(msg.tool_calls):
        cmd = tc.args.get("cmd", "?")
        result = msg.tool_results[i] if i < len(msg.tool_results) else ""

        if level == "summary":
            # Summarize: first line of cmd, first 3 lines of result
            cmd_short = cmd.split('\n')[0]
            if len(cmd_short) > 80:
                cmd_short = cmd_short[:80] + "..."
            result_short = summarize_lines(result, max_lines=3)
            tool_parts.append(f"$ {cmd_short}\n{result_short}")
        else:  # "full"
            # Full output but still cap at reasonable length
            if len(result) > 500:
                result = result[:500] + "..."
            tool_parts.append(f"$ {cmd}\n{result}")

    return "\n\n".join(tool_parts)


def build_context(agent: AgentConfig, messages: list[Message]) -> list[dict]:
    """Build OpenAI-compatible message list for an agent."""
    context = [{"role": "system", "content": agent.system_prompt}]

    # Take last N messages
    recent = messages[-MAX_CONTEXT_MESSAGES:]

    for msg in recent:
        # Strip @ from name for the name field
        name = msg.from_id.lstrip("@")

        if msg.from_id == agent.id:
            # My own messages are "assistant" - always show full tool context for own calls
            if msg.tool_calls:
                # Add assistant message with tool calls
                for i, tc in enumerate(msg.tool_calls):
                    # Assistant message requesting tool call
                    context.append({
                        "role": "assistant",
                        "content": msg.content if i == 0 else None,
                        "tool_calls": [{
                            "id": f"call_{i}",
                            "type": "function",
                            "function": {
                                "name": tc.name,
                                "arguments": json.dumps(tc.args)
                            }
                        }]
                    })
                    # Tool result
                    result = msg.tool_results[i] if i < len(msg.tool_results) else ""
                    context.append({
                        "role": "tool",
                        "tool_call_id": f"call_{i}",
                        "content": result
                    })
            else:
                # Simple assistant message
                context.append({"role": "assistant", "content": msg.content})
        else:
            # Other participants use "user" role with "name" field
            content = msg.content

            # Add tool call summary based on agent's tool_context setting
            if msg.tool_calls:
                tool_summary = format_tool_calls(msg, agent.tool_context)
                if tool_summary:
                    content += "\n\n" + tool_summary

            context.append({"role": "user", "name": name, "content": content})

    return context


def get_agent_response(agent: AgentConfig, room: Room) -> Message:
    """Get a complete response from an agent, handling tool calls with streaming."""
    messages = build_context(agent, room.messages)
    all_tool_calls: list[ToolCall] = []
    all_tool_results: list[str] = []
    full_content = ""

    client = OpenAI(
        base_url=agent.endpoint,
        api_key=agent.api_key or "dummy"
    )

    first_response = True
    max_iterations = 10
    iteration = 0

    while iteration < max_iterations:
        iteration += 1
        try:
            # Use streaming
            stream = client.chat.completions.create(
                model=agent.model,
                messages=messages,
                tools=TOOLS if agent.can_use_tools else None,
                temperature=agent.temperature,
                stream=True,
            )
        except Exception as e:
            if first_response:
                ui.clear_thinking()
                ui.print_agent_label(agent.id)
            ui.print_error(str(e))
            return Message(
                id=str(uuid4()),
                from_id=agent.id,
                content=f"[ERROR: {e}]",
                tool_calls=[],
                tool_results=[],
            )

        # Collect streamed response
        collected_content = ""
        collected_tool_calls = []
        current_tool_call = None
        finish_reason = None
        bytes_received = 0

        for chunk in stream:
            if not chunk.choices:
                continue

            choice = chunk.choices[0]
            delta = choice.delta

            # Track finish reason
            if choice.finish_reason:
                finish_reason = choice.finish_reason

            if not delta:
                continue

            # Handle content tokens
            if delta.content:
                bytes_received += len(delta.content)
                if first_response:
                    ui.clear_thinking()
                    ui.print_agent_label(agent.id)
                    first_response = False
                ui.print_streaming_token(delta.content)
                collected_content += delta.content

            # Handle tool calls (streamed in chunks)
            if delta.tool_calls:
                # Update byte counter while building tool calls
                for tc in delta.tool_calls:
                    if tc.function and tc.function.arguments:
                        bytes_received += len(tc.function.arguments)
                if first_response:
                    ui.update_thinking_bytes(bytes_received)
                for tc_chunk in delta.tool_calls:
                    if tc_chunk.index is not None:
                        # New or continuing tool call
                        while len(collected_tool_calls) <= tc_chunk.index:
                            collected_tool_calls.append({
                                "id": "",
                                "name": "",
                                "arguments": ""
                            })
                        current_tool_call = collected_tool_calls[tc_chunk.index]

                    if tc_chunk.id:
                        current_tool_call["id"] = tc_chunk.id
                    if tc_chunk.function:
                        if tc_chunk.function.name:
                            current_tool_call["name"] = tc_chunk.function.name
                        if tc_chunk.function.arguments:
                            current_tool_call["arguments"] += tc_chunk.function.arguments

        # Add collected content
        if collected_content:
            full_content += collected_content

        # If no tool calls, or finish_reason indicates stop, we're done
        if not collected_tool_calls or finish_reason in ("stop", "end_turn"):
            break

        # Clear thinking if we haven't printed anything yet
        if first_response:
            ui.clear_thinking()
            ui.print_agent_label(agent.id)
            first_response = False

        # Execute tool calls
        for tc in collected_tool_calls:
            try:
                args = json.loads(tc["arguments"])
            except json.JSONDecodeError:
                args = {"cmd": tc["arguments"]}

            cmd = args.get("cmd", "")

            # Show the command being run
            ui.print_tool_call(cmd)

            # Execute
            result = room.sandbox.execute(cmd) if room.sandbox else "[ERROR: No sandbox]"

            # Show result
            ui.print_tool_result(result)

            all_tool_calls.append(ToolCall(name=tc["name"], args=args))
            all_tool_results.append(result)

            # Append to messages for next iteration
            messages.append({
                "role": "assistant",
                "content": None,
                "tool_calls": [{
                    "id": tc["id"],
                    "type": "function",
                    "function": {
                        "name": tc["name"],
                        "arguments": tc["arguments"]
                    }
                }]
            })
            messages.append({
                "role": "tool",
                "tool_call_id": tc["id"],
                "content": result
            })

        # Show we're waiting for next LLM response
        print(f"  {ui.DIM}...{ui.RESET}", flush=True)

    if iteration >= max_iterations:
        ui.print_error(f"Max iterations ({max_iterations}) reached")

    # End with newline
    print()

    return Message(
        id=str(uuid4()),
        from_id=agent.id,
        content=full_content,
        tool_calls=all_tool_calls,
        tool_results=all_tool_results,
    )
