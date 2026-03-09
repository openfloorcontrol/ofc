# Testing OFC Floors

> How to test blueprints, evaluate agent behavior, and build automated test suites

OFC provides three levels of testing, from quick command-line checks to full Go integration tests.

## 1. JSONL output (`ofc run --json`)

The `--json` flag outputs all floor events as JSONL (one JSON object per line) to stdout. This is the foundation — pipe it into `jq`, scripts, or `ofc eval`.

```bash
ofc run -f blueprint.yaml "add a task for testing" --json > run.jsonl
```

Each line is a self-contained event:

```jsonl
{"type":"floor_started","name":"my-floor","agents":["@planner","@coder"],"furniture":["tasks"]}
{"type":"message_posted","message":{"from":"@user","content":"add a task for testing"}}
{"type":"agent_label","agent_id":"@planner"}
{"type":"token","agent_id":"@planner","token":"Let me "}
{"type":"tool_call_started","agent_id":"@planner","id":"tc1","title":"tasks.add_task"}
{"type":"tool_call_result","agent_id":"@planner","id":"tc1","title":"tasks.add_task","output":"{...}"}
{"type":"agent_finished","agent_id":"@planner"}
{"type":"message_posted","message":{"from":"@planner","content":"I've added the task."}}
{"type":"floor_stopped"}
```

### Quick checks with jq

```bash
# Was a specific tool called?
jq -e 'select(.type == "tool_call_started" and (.title | contains("tasks")))' run.jsonl

# Did a specific agent speak?
jq -e 'select(.type == "message_posted" and .message.from == "@planner")' run.jsonl

# Any errors?
jq -e 'select(.type == "agent_error")' run.jsonl

# Extract all agent messages
jq -r 'select(.type == "message_posted") | "\(.message.from): \(.message.content)"' run.jsonl
```

## 2. LLM evaluation (`ofc eval`)

`ofc eval` reads text from stdin, sends it to an LLM with your evaluation prompt, and outputs a structured score.

```bash
echo "The agent analyzed data and found three trends" | \
  ofc eval "Was the analysis thorough and data-driven? Score 1-5"
```

Output:
```json
{"score": 4, "reasoning": "The agent identified multiple trends, suggesting systematic analysis..."}
```

### Piping from `ofc run`

The real power is combining `--json` with `eval`:

```bash
ofc run -f blueprint.yaml "analyze the sales data" --json | \
  ofc eval "Did the agent delegate coding tasks to @code rather than writing code itself?"
```

### How evaluation works

The eval command replaces the agent's system prompt with an evaluation prompt that instructs the LLM to score the input on a 1-5 scale and return structured JSON:

```
You are an evaluator. You will be given text to evaluate and a set of evaluation criteria.

Evaluation criteria:
<your prompt from the command line>

Read the provided text carefully and evaluate it according to the criteria above.
Respond with ONLY a JSON object in this exact format, nothing else:
{"score": <1-5>, "reasoning": "<brief explanation>"}

Where 1 = very poor, 2 = poor, 3 = acceptable, 4 = good, 5 = excellent.
```

The input text (from stdin) is sent as the user message. The LLM's response is parsed as JSON (markdown code fences are stripped if present).

### Configuration

`ofc eval` gets its LLM config from the project's blueprint:

```bash
# Uses first LLM agent's endpoint/model from blueprint.yaml
ofc eval "Score this"

# Use a specific agent's config
ofc eval --agent @eval "Score this"

# Use a specific blueprint
ofc eval -f path/to/blueprint.yaml "Score this"

# Standalone (no blueprint needed)
ofc eval --endpoint http://localhost:11434/v1 --model llama3 "Score this"
```

## 3. Go integration tests (`ofctest`)

The `ofctest` package lets you test floors from Go tests — mechanical assertions on events plus LLM-as-judge evaluation.

```go
package myfloor_test

import (
    "os"
    "testing"

    "github.com/openfloorcontrol/ofc/ofctest"
)

func TestTaskCreation(t *testing.T) {
    if os.Getenv("OFC_INTEGRATION") == "" {
        t.Skip("set OFC_INTEGRATION=1 to run (requires LLM endpoint)")
    }

    result := ofctest.RunFloor(t, "blueprint.yaml",
        "Add a task: write unit tests for the auth module")

    // Mechanical assertions
    result.AssertNoErrors(t)
    result.AssertAgentSpoke(t, "@planner")
    result.AssertToolCalled(t, "tasks.add_task")
    result.AssertContentContains(t, "@planner", "task")

    // LLM-as-judge evaluation
    ev := result.Eval(t, "Did the agent create a new task (not just list existing ones)?")
    ev.AssertMinScore(t, 3)
}
```

### Available assertions

| Method | Description |
|--------|-------------|
| `AssertAgentSpoke(t, agentID)` | Agent posted at least one message |
| `AssertToolCalled(t, toolSubstr)` | A tool call title contains the substring |
| `AssertNoErrors(t)` | No agent error events |
| `AssertContentContains(t, agentID, substr)` | Agent message contains substring |
| `Eval(t, prompt)` | Run LLM evaluation, returns `*EvalResult` |
| `EvalResult.AssertMinScore(t, min)` | Evaluation score >= minimum (1-5 scale) |

### Running tests

```bash
# Regular tests (integration tests skip automatically)
go test ./...

# With integration tests (requires running LLM endpoint)
OFC_INTEGRATION=1 go test ./... -timeout 120s
```

### How it works

`RunFloor` loads the blueprint, starts the full floor (including furniture), runs all agents through the controller, and collects events — same as `ofc run --json` but in-process. The `Eval` method uses the first LLM agent's config to run the evaluation.

## Choosing an approach

| Approach | Best for | Requires |
|----------|----------|----------|
| `--json` + `jq` | Quick ad-hoc checks, CI smoke tests | `jq` |
| `ofc eval` | Evaluating output quality, prompt iteration | LLM endpoint |
| `ofctest` | Automated test suites, regression testing | Go, LLM endpoint |

All three approaches compose: `ofctest` uses the same floor machinery as `ofc run --json`, and `Eval()` calls the same code as `ofc eval`.

ofc. 🎤
