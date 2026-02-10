# Multi-Agent Chatroom - SPECS.md

## Overview

A multi-agent chatroom where LLM agents can converse, @mention each other,
and collaborate on tasks using a shared bash tool in a Docker sandbox.

**Version**: 0.0.1 (minimal viable prototype)

## Goals

- Test whether multiple LLM agents can naturally collaborate via conversation
- Validate the @mention activation pattern
- Keep it simple - prove the concept before adding infrastructure

## Non-Goals (for v0.0.1)

- Persistence / saving sessions
- Web UI (just print() TUI)
- Agent interruption (only user can interrupt via Ctrl+C)
- Smart context management (just truncate)
- Concurrent agent responses

---

## Core Data Structures

### Message

```python
@dataclass
class Message:
    id: str                    # UUID
    from_id: str               # "@user", "@data", "@code", etc.
    content: str               # Text content
    tool_calls: list[ToolCall] # Tool calls made (if any)
    tool_results: list[str]    # Results from tool calls
    timestamp: datetime
    
@dataclass
class ToolCall:
    name: str                  # "bash"
    args: dict                 # {"cmd": "..."}
```

### Agent Configuration

```python
@dataclass
class AgentConfig:
    id: str                    # "@data", "@code" - must start with @
    model: str                 # "gpt-4o", "ollama/llama3", etc.
    endpoint: str              # OpenAI-compatible API endpoint
    api_key: str | None        # None for local models
    system_prompt: str         # Agent's personality and instructions
    activation: Literal["always", "mention"]
    can_use_tools: bool        # Whether agent can call bash
    temperature: float         # 0.0 - 1.0
```

### Room

```python
@dataclass 
class Room:
    agents: list[AgentConfig]
    messages: list[Message]
    sandbox: Sandbox
```

---

## Activation Logic

```python
def should_wake(agent: AgentConfig, message: Message, room: Room) -> bool:
    """
    Determine if an agent should respond to a message.
    Designed to be extended with more complex logic later.
    """
    # Never respond to own messages
    if message.from_id == agent.id:
        return False
    
    # "Awaiting response" - if I mentioned the speaker in my last message,
    # I'm interested in their reply (like asking a question and waiting for answer)
    my_last_msg = get_last_message_from(agent.id, room.messages)
    if my_last_msg and message.from_id in extract_mentions(my_last_msg.content):
        return True
    
    # Check activation mode
    if agent.activation == "always":
        return True
    
    if agent.activation == "mention":
        return is_mentioned(agent.id, message.content)
    
    return False

def get_last_message_from(agent_id: str, messages: list[Message]) -> Message | None:
    """Get the most recent message from a specific agent."""
    for msg in reversed(messages):
        if msg.from_id == agent_id:
            return msg
    return None

def is_mentioned(agent_id: str, content: str) -> bool:
    """
    Check if an agent is mentioned in content.
    Simple regex for now, structured for future extension.
    """
    mentions = extract_mentions(content)
    return agent_id in mentions

def extract_mentions(content: str) -> list[str]:
    """
    Extract @mentions from content.
    Returns list of agent IDs (e.g., ["@data", "@code"])
    """
    pattern = r'@(\w+)'
    matches = re.findall(pattern, content)
    return [f"@{m}" for m in matches]
```

---

## Turn Flow

The core loop is the standard LLM loop, extended with "who goes next?":

```
Standard LLM loop:
  User → LLM → [tool_call → execute → LLM]* → response → User

Multi-agent extension:
  User → LLM_A → [tool_call → execute → LLM_A]* → response → next(?) → ...
```

**The only addition**: when a turn ends, decide who goes next.

```python
def next_recipient(completed_msg: Message, room: Room) -> str | None:
    """After a message completes, who should respond next?"""
    
    # 1. Who initiated this turn? (who @mentioned the speaker?)
    initiator = find_initiator(completed_msg, room)
    if initiator and initiator != "@user":
        # An agent asked for this, they get the response
        return initiator
    
    # 2. Check if any agent wants to wake based on their rules
    for agent in room.agents:
        if should_wake(agent, completed_msg, room):
            return agent.id
    
    # 3. Nobody wants it, back to user
    return None  # signals: wait for user input


def find_initiator(msg: Message, room: Room) -> str | None:
    """Find who @mentioned the speaker, triggering this response."""
    # Look at the message that triggered the speaker
    for prev_msg in reversed(room.messages[:-1]):
        if msg.from_id in extract_mentions(prev_msg.content):
            return prev_msg.from_id
        if prev_msg.from_id == msg.from_id:
            # Hit speaker's previous message, stop looking
            break
    return None
```

**Main loop**:

```python
async def main_loop(room: Room):
    while True:
        user_input = input("\n[@user]: ").strip()
        if user_input == "/quit":
            break
        
        # Add user message
        user_msg = create_message("@user", user_input)
        room.messages.append(user_msg)
        
        # Agent loop - keep going until it's the user's turn again
        current_msg = user_msg
        while True:
            next_agent_id = next_recipient(current_msg, room)
            
            if next_agent_id is None:
                break  # Back to user
            
            agent = get_agent(next_agent_id, room)
            response = await get_agent_response(agent, room)
            
            if response.content.strip() == "[pass]":
                continue  # Agent passed, check others
            
            room.messages.append(response)
            current_msg = response
```

**Future complexity** (not v0.0.1):
- Agents reading tool calls and deciding to interrupt
- Agents interjecting mid-turn
- Parallel agent evaluation

---

## Agent Response Flow

When an agent responds, it may make multiple tool calls before finishing:

```
1. Build context from room.messages (see Context Building)
2. Call LLM (streaming optional - just affects UX, not logic)
3. Process response:
   a. If tool_call in response:
      - Print: f"[{agent.id}] Running: {cmd}"
      - Execute in sandbox
      - Print: f"[result]: {output}"
      - Append tool call + result to messages
      - Call LLM again with updated context (loop back to step 2)
   b. If no tool_call (just text):
      - Print the text response
      - Create final Message with all content + tool calls/results
      - Append to room.messages
      - Agent turn complete, return
```

**Note on streaming**: Streaming (SSE) only affects how text tokens are displayed - 
incrementally vs all at once. The tool call flow is identical either way. When the LLM
decides to call a tool, it stops generating (stop_reason: tool_use). You execute the
tool, append the result, and make a new LLM request. SSE is not bidirectional.

---

## Context Building

```python
def build_context(agent: AgentConfig, messages: list[Message], max_messages: int = 50) -> list[dict]:
    """
    Build OpenAI-compatible message list for an agent.
    Simple truncation for v0.0.1.
    """
    context = []
    
    # Take last N messages
    recent = messages[-max_messages:]
    
    for msg in recent:
        # Determine role from agent's perspective
        if msg.from_id == agent.id:
            role = "assistant"
        else:
            role = "user"
        
        # Format content to show speaker
        content = f"[{msg.from_id}]: {msg.content}"
        
        # Append tool results if any
        for i, result in enumerate(msg.tool_results):
            tool_call = msg.tool_calls[i] if i < len(msg.tool_calls) else None
            cmd = tool_call.args.get("cmd", "?") if tool_call else "?"
            content += f"\n[ran: {cmd}]\n[result]: {result}"
        
        context.append({"role": role, "content": content})
    
    return context
```

---

## Tools

Single tool for v0.0.1:

```python
TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "bash",
            "description": "Run a bash command in the workspace container. Use for ALL file operations: reading (cat, head), writing (cat << EOF), editing (sed, or rewrite file), listing (ls, find), searching (grep), running code (python, node), etc.",
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
```

---

## Docker Sandbox

```python
class Sandbox:
    """
    Docker-based sandbox for executing bash commands safely.
    """
    
    def __init__(self, workspace_dir: str | None = None):
        self.container_id: str | None = None
        self.workspace_dir = workspace_dir  # Optional: copy into container
    
    def start(self):
        """Start the sandbox container."""
        cmd = [
            "docker", "run", "-d", "--rm",
            "--network", "none",  # No network access
            "-w", "/workspace",
            "python:3.11-slim",
            "sleep", "infinity"
        ]
        result = subprocess.run(cmd, capture_output=True, check=True)
        self.container_id = result.stdout.decode().strip()
        
        # Copy workspace if provided
        if self.workspace_dir:
            subprocess.run([
                "docker", "cp", 
                f"{self.workspace_dir}/.", 
                f"{self.container_id}:/workspace/"
            ], check=True)
    
    def execute(self, cmd: str, timeout: int = 30) -> str:
        """Execute a bash command and return output."""
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
            return output
        except subprocess.TimeoutExpired:
            return f"[ERROR: Command timed out after {timeout}s]"
        except Exception as e:
            return f"[ERROR: {e}]"
    
    def stop(self):
        """Stop and remove the container."""
        if self.container_id:
            subprocess.run(["docker", "kill", self.container_id], 
                          capture_output=True)
            self.container_id = None
```

---

## LLM Client

Using OpenAI-compatible API. Streaming is optional (nice for UX, not required for logic):

```python
async def chat_completion(
    endpoint: str,
    api_key: str | None,
    model: str,
    messages: list[dict],
    tools: list[dict] | None,
    temperature: float,
    stream: bool = False,
) -> dict:
    """
    Call LLM and return response.
    
    Response contains either:
    - content: str (text response)
    - tool_calls: list[{name, args}] (tool invocations)
    
    If tool_calls present, caller should:
    1. Execute each tool
    2. Append tool results to messages
    3. Call chat_completion again
    4. Repeat until response has no tool_calls
    """
    client = OpenAI(base_url=endpoint, api_key=api_key or "dummy")
    
    response = client.chat.completions.create(
        model=model,
        messages=messages,
        tools=tools,
        temperature=temperature,
        stream=stream,
    )
    
    if stream:
        # Collect streamed tokens, print as they arrive
        # Return final assembled response
        ...
    else:
        return response.choices[0].message
```

**Tool call loop** (in calling code):

```python
async def get_agent_response(agent: AgentConfig, room: Room) -> Message:
    messages = build_context(agent, room.messages)
    all_tool_calls = []
    all_tool_results = []
    full_content = ""
    
    while True:
        response = await chat_completion(
            endpoint=agent.endpoint,
            api_key=agent.api_key,
            model=agent.model,
            messages=messages,
            tools=TOOLS if agent.can_use_tools else None,
            temperature=agent.temperature,
        )
        
        if response.content:
            full_content += response.content
            print(response.content, end="", flush=True)
        
        if not response.tool_calls:
            break  # Done, no more tool calls
        
        # Execute tool calls
        for tc in response.tool_calls:
            cmd = tc.function.arguments.get("cmd", "")
            print(f"\n[running]: {cmd}")
            result = room.sandbox.execute(cmd)
            print(f"[result]: {result[:500]}")
            
            all_tool_calls.append(ToolCall(name=tc.function.name, args=tc.function.arguments))
            all_tool_results.append(result)
            
            # Append to messages for next iteration
            messages.append({"role": "assistant", "content": None, "tool_calls": [tc]})
            messages.append({"role": "tool", "tool_call_id": tc.id, "content": result})
    
    return Message(
        id=str(uuid4()),
        from_id=agent.id,
        content=full_content,
        tool_calls=all_tool_calls,
        tool_results=all_tool_results,
        timestamp=datetime.now(),
    )
```

---

## Example Agent Configurations

```python
AGENTS = [
    AgentConfig(
        id="@data",
        model="gpt-4o",
        endpoint="https://api.openai.com/v1",
        api_key=os.environ.get("OPENAI_API_KEY"),
        system_prompt="""You are @data, a senior data analyst in a multi-agent chatroom.

Other participants:
- @user: The human you're helping
- @code: A programmer who can run code (bash tool)
- @reviewer: Watches for problems (optional)

Your role:
- Lead data analysis conversations
- Understand what @user wants to achieve
- Break down analysis into steps
- Delegate coding tasks to @code by @mentioning them
- Interpret results and guide next steps
- Be SKEPTICAL of results - check for empty data, NaNs, suspicious patterns

You cannot run code yourself. Ask @code to do it.

Keep responses concise. Think out loud briefly, then act.""",
        activation="always",
        can_use_tools=False,
        temperature=0.7
    ),
    
    AgentConfig(
        id="@code",
        model="gpt-4o",  # Or "ollama/deepseek-coder" for local
        endpoint="https://api.openai.com/v1",
        api_key=os.environ.get("OPENAI_API_KEY"),
        system_prompt="""You are @code, an expert programmer in a multi-agent chatroom.

Other participants:
- @user: The human
- @data: Data analyst who guides the analysis
- @reviewer: Watches for problems (optional)

Your role:
- Implement what's asked with code
- You have ONE tool: bash
- Use bash for EVERYTHING: read files (cat), write files (cat << 'EOF'), run code (python -c or scripts), list dirs (ls), search (grep)
- Show your commands clearly
- If something fails, try to fix it or report the error

Keep responses SHORT. Do the work, show the result, done.
Don't explain what you're going to do - just do it.""",
        activation="mention",  # Only responds when @mentioned
        can_use_tools=True,
        temperature=0.2
    ),
    
    AgentConfig(
        id="@reviewer",
        model="gpt-4o-mini",
        endpoint="https://api.openai.com/v1",
        api_key=os.environ.get("OPENAI_API_KEY"),
        system_prompt="""You are @reviewer, a quality checker in a multi-agent chatroom.

Your role:
- Watch the conversation silently
- ONLY speak when you spot a real problem:
  * Empty results accepted as valid
  * Same error repeated 3+ times (spiraling)
  * Suspicious commands (rm -rf, etc.)
  * Obviously wrong analysis logic
  * Data issues (all NaN, wrong columns, etc.)

If everything looks fine, say NOTHING. Respond with just:
[pass]

If there's a problem, be brief and specific:
"Hold on - that query returned 0 rows but @data is treating it as valid."

Do NOT be chatty. Do NOT offer suggestions unless there's an actual problem.""",
        activation="always",
        can_use_tools=False,
        temperature=0.3
    ),
]
```

---

## Main Entry Point

```python
async def main():
    # Setup
    room = Room(
        agents=AGENTS,
        messages=[],
        sandbox=Sandbox(workspace_dir="./workspace")
    )
    room.sandbox.start()
    
    print("=" * 50)
    print("Multi-Agent Chatroom v0.0.1")
    print("Agents:", ", ".join(a.id for a in room.agents))
    print("Type /quit to exit, /clear to reset")
    print("=" * 50)
    
    try:
        await main_loop(room)  # See Turn Flow section
    finally:
        room.sandbox.stop()
        print("\n[System]: Goodbye!")
```

---

## File Structure

```
chatroom/
├── main.py           # Entry point, main loop
├── models.py         # Data classes (Message, AgentConfig, Room)
├── agents.py         # Agent configs, should_wake, extract_mentions
├── llm.py            # LLM client, stream_chat_completion
├── sandbox.py        # Docker sandbox
├── config.py         # API keys, settings
└── workspace/        # Optional: files to copy into sandbox
    └── data.csv      # Example data for testing
```

---

## Running

```bash
# Prerequisites
pip install openai aiohttp

# Set API keys
export OPENAI_API_KEY=sk-...

# Optional: start ollama for local models
ollama serve

# Run
python main.py
```

---

## Example Session

```
==================================================
Multi-Agent Chatroom v0.0.1
Agents: @data, @code, @reviewer
Type /quit to exit, /clear to reset
==================================================

[@user]: Hey @data, I have a CSV with sales data. Can you help me find the top customers?

[@data]: Sure! Let me get @code to take a look at the data first.
@code can you show me the first few rows and columns of any CSV files in the workspace?

[@code]: Running: ls -la *.csv && head -20 sales.csv
[result]: 
-rw-r--r-- 1 root root 15234 Jan 15 10:00 sales.csv
customer_id,date,amount,product
C001,2024-01-15,150.00,Widget A
C002,2024-01-15,75.50,Widget B
...

[@data]: Good, we have customer_id and amount. @code can you group by customer_id, 
sum the amounts, and show top 10?

[@code]: Running: python3 << 'EOF'
import pandas as pd
df = pd.read_csv('sales.csv')
top = df.groupby('customer_id')['amount'].sum().sort_values(ascending=False).head(10)
print(top)
EOF
[result]:
customer_id
C045    12450.00
C012     8920.50
...

[@data]: There we go! Customer C045 is your top spender at $12,450 total. 
Want me to dig deeper - like what products they're buying or trends over time?

[@user]: Yeah, what's C045 buying?

[@data]: @code filter for customer C045 and show product breakdown with counts and totals

[@code]: Running: python3 << 'EOF'
import pandas as pd
df = pd.read_csv('sales.csv')
c045 = df[df['customer_id'] == 'C045']
breakdown = c045.groupby('product').agg({'amount': ['count', 'sum']}).sort_values(('amount', 'sum'), ascending=False)
print(breakdown)
EOF
[result]:
              amount       
               count    sum
product                    
Widget Pro        23  8500.0
Widget A          15  2950.0
Widget B           8  1000.0

[@data]: C045 loves the Widget Pro - 23 purchases totaling $8,500. That's 68% of their 
total spend. They're clearly a power user of your premium product. @user want me to 
check if there are other customers with similar patterns?
```

---

## Future Enhancements (not v0.0.1)

- [ ] Web UI with Vue + WebSocket
- [ ] Streaming to UI (SSE-style message chunks)
- [ ] Agent interruption
- [ ] Smarter context (summarization)
- [ ] Concurrent agent evaluation  
- [ ] Persistence (save/load sessions)
- [ ] More tools (dedicated file edit, web search)
- [ ] Remote agents over HTTP
- [ ] Agent-to-agent private channels
- [ ] Wake word classifiers (SLM-based activation)
- [ ] Standardized LLM interop protocol
- [ ] Federation (connect to external agent endpoints)
