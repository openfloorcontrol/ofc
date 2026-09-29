// Package eval provides LLM-based evaluation of text and conversations.
package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/agents"
	"github.com/openfloorcontrol/ofc/api"
)

// maxAttempts caps retries on transient empty-content responses. Callers see
// one final error only after all attempts fail; each retry is logged to
// stderr so we can gauge how often this hits real workloads.
const maxAttempts = 3

// errEmptyContent is the specific case we retry: RunOnce returned no
// assistant content. Other errors are reported straight through.
var errEmptyContent = errors.New("agent returned empty response")

// Result holds the structured evaluation output.
type Result struct {
	Score     int    `json:"score"`
	Reasoning string `json:"reasoning"`
	Input     string `json:"input"` // the text that was evaluated
}

const systemPromptTemplate = `You are an evaluator. You will be given text to evaluate and a set of evaluation criteria.

Evaluation criteria:
%s

Read the provided text carefully and evaluate it according to the criteria above.
Respond with ONLY a JSON object in this exact format, nothing else:
{"score": <1-5>, "reasoning": "<brief explanation>"}

Where 1 = very poor, 2 = poor, 3 = acceptable, 4 = good, 5 = excellent.`

// Run evaluates the input text using an LLM agent from the blueprint.
// The evalPrompt describes what to evaluate. The agent's system prompt is
// replaced with evaluation instructions.
//
// If the agent returns empty content — a known transient behaviour that we're
// still characterising — Run retries up to maxAttempts times, logging each
// attempt to stderr.
func Run(input string, evalPrompt string, agentID string, bp *blueprint.Blueprint) (*Result, error) {
	systemPrompt := fmt.Sprintf(systemPromptTemplate, evalPrompt)

	// Find the agent definition; override its prompt with the eval system prompt.
	var agentDef *blueprint.Agent
	var available []string
	for i := range bp.Agents {
		available = append(available, bp.Agents[i].ID)
		if bp.Agents[i].ID == agentID {
			agentDef = &bp.Agents[i]
		}
	}
	if agentDef == nil {
		return nil, fmt.Errorf("agent %q is not defined in the blueprint. Available agents: %s",
			agentID, strings.Join(available, ", "))
	}
	agentDef.Prompt = systemPrompt

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		parsed, err := runOnce(input, agentDef, bp)
		if err == nil {
			if attempt > 1 {
				fmt.Fprintf(os.Stderr, "ofc eval: recovered after %d attempts\n", attempt)
			}
			return parsed, nil
		}
		lastErr = err
		if !errors.Is(err, errEmptyContent) || attempt == maxAttempts {
			break
		}
		fmt.Fprintf(os.Stderr, "ofc eval: %v (attempt %d/%d); retrying\n", err, attempt, maxAttempts)
	}
	return nil, lastErr
}

// runOnce does one eval attempt. Separated so Run can retry cleanly.
func runOnce(input string, agentDef *blueprint.Agent, bp *blueprint.Blueprint) (*Result, error) {
	result, err := floor.RunOnce(floor.RunOnceConfig{
		Blueprint: bp,
		AgentID:   agentDef.ID,
		Input:     input,
		APIServer: api.New(),
	}, agents.NewLLM(agentDef))
	if err != nil {
		return nil, fmt.Errorf("eval run: %w", err)
	}

	if result.Content == "" {
		logEmpty(result)
		return nil, errEmptyContent
	}

	parsed, err := parseResult(result.Content)
	if err != nil {
		return nil, err
	}
	parsed.Input = input
	return parsed, nil
}

// logEmpty warns when Content is empty. The message distinguishes two cases:
// the judge reasoned but skipped the answer (a known failure mode of some
// reasoning models — switch judge to fix), versus nothing at all (endpoint
// or model probably unresponsive).
func logEmpty(result *floor.RunOnceResult) {
	var (
		reasoning  strings.Builder
		content    strings.Builder
		toolTitles []string
	)
	for _, ev := range result.Events {
		se, ok := ev.(floor.StreamEvent)
		if !ok {
			continue
		}
		switch s := se.Event.(type) {
		case floor.ThoughtStreamed:
			reasoning.WriteString(s.Token)
		case floor.TokenStreamed:
			content.WriteString(s.Token)
		case floor.ToolCallStarted:
			toolTitles = append(toolTitles, s.Title)
		}
	}

	if reasoning.Len() > 0 {
		fmt.Fprintf(os.Stderr,
			"ofc eval: judge returned empty response after thinking (%d chars of reasoning) — maybe switch to another model.\n",
			reasoning.Len())
		fmt.Fprintf(os.Stderr, "  reasoning preview: %s\n", truncate(reasoning.String(), 500))
	} else {
		fmt.Fprintln(os.Stderr,
			"ofc eval: judge returned nothing — no answer, no reasoning. Endpoint or model may be unresponsive.")
	}
	if content.Len() > 0 {
		fmt.Fprintf(os.Stderr, "  partial content: %s\n", truncate(content.String(), 500))
	}
	if len(toolTitles) > 0 {
		fmt.Fprintf(os.Stderr, "  tool calls: %v\n", toolTitles)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// parseResult extracts a Result from the LLM response.
// Handles raw JSON and JSON wrapped in markdown code fences.
func parseResult(response string) (*Result, error) {
	// Strip markdown code fences if present
	text := strings.TrimSpace(response)
	if strings.HasPrefix(text, "```") {
		// Remove opening fence (```json or ```)
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(text, "```"); idx >= 0 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	var result Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("parse eval response: %w\nraw response: %s", err, response)
	}

	return &result, nil
}
