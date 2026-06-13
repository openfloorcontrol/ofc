// Package eval provides LLM-based evaluation of text and conversations.
package eval

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/floor/agents/llm"
	"github.com/openfloorcontrol/ofc/api"
)

// Result holds the structured evaluation output.
type Result struct {
	Score     int    `json:"score"`
	Reasoning string `json:"reasoning"`
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
func Run(input string, evalPrompt string, agentID string, bp *blueprint.Blueprint) (*Result, error) {
	systemPrompt := fmt.Sprintf(systemPromptTemplate, evalPrompt)

	// Find the agent definition; override its prompt with the eval system prompt.
	var agentDef *blueprint.Agent
	for i := range bp.Agents {
		if bp.Agents[i].ID == agentID {
			agentDef = &bp.Agents[i]
			break
		}
	}
	if agentDef == nil {
		return nil, fmt.Errorf("agent %q not found in blueprint", agentID)
	}
	agentDef.Prompt = systemPrompt

	result, err := floor.RunOnce(floor.RunOnceConfig{
		Blueprint: bp,
		AgentID:   agentID,
		Input:     input,
		APIServer: api.New(),
	}, llm.New(agentDef))
	if err != nil {
		return nil, fmt.Errorf("eval run: %w", err)
	}

	if result.Content == "" {
		return nil, fmt.Errorf("eval: agent returned empty response")
	}

	return parseResult(result.Content)
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
