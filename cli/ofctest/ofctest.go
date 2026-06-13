// Package ofctest provides test helpers for running OFC floors and asserting on results.
package ofctest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/eval"
	"github.com/openfloorcontrol/ofc/floor"
	acpagent "github.com/openfloorcontrol/ofc/floor/agents/acp"
	llmagent "github.com/openfloorcontrol/ofc/floor/agents/llm"
	"github.com/openfloorcontrol/ofc/floor/frontend"
)

// FloorResult holds the collected events and messages from a floor run.
type FloorResult struct {
	Events   []map[string]interface{} // serialized events (same format as --json)
	Messages []floor.ChatMessage      // final chat history
	bp       *blueprint.Blueprint
}

// RunFloor loads a blueprint, starts a full multi-agent floor, posts the prompt,
// and collects all events until agents are done (one-shot mode).
func RunFloor(t *testing.T, blueprintPath string, prompt string) *FloorResult {
	t.Helper()

	bp, err := blueprint.Load(blueprintPath)
	if err != nil {
		t.Fatalf("ofctest: load blueprint: %v", err)
	}

	return runFloor(t, bp, prompt)
}

func runFloor(t *testing.T, bp *blueprint.Blueprint, prompt string) *FloorResult {
	t.Helper()

	f := floor.NewFloor(bp)
	if err := f.Start(func(string) {}); err != nil {
		t.Fatalf("ofctest: start floor: %v", err)
	}
	defer f.Stop()

	ctrl := floor.NewController(f)

	agents := make(map[string]floor.Agent)
	for i := range bp.Agents {
		a := &bp.Agents[i]
		switch a.Type {
		case "acp":
			agents[a.ID] = acpagent.New(a)
		default:
			agents[a.ID] = llmagent.New(a)
		}
	}

	sess := f.DefaultSession()

	// Post initial prompt
	sess.MainRoom.PostUserInput(prompt)

	result := &FloorResult{bp: bp}
	unified := sess.StartUnified()

	onCloseInfo := func(info string) {
		result.Events = append(result.Events, map[string]interface{}{
			"type": "system_info",
			"text": info,
		})
	}

	for tagged := range unified {
		ec, ok := frontend.ResolveEventContext(sess, ctrl, tagged)
		if !ok {
			continue
		}
		ev := tagged.Event

		// Collect event
		if payload := floor.EventJSON(ev); payload != nil {
			if ec.RoomID != "" {
				payload["room_id"] = ec.RoomID
			}
			result.Events = append(result.Events, payload)
		}

		// Drive the controller
		switch ev.(type) {
		case floor.MessagePosted, floor.AgentPassedEvent, floor.AgentErrorEvent:
			decision := frontend.DecideAndAutoClose(ec, ev, sess, ctrl, onCloseInfo)

			switch decision.Action {
			case "trigger":
				agent, ok := agents[decision.AgentID]
				if !ok {
					continue
				}
				ctx := context.Background()
				turn := floor.NewAgentTurn(ec.Sess, ec.Sess.MainRoom, ec.Sess.Floor, decision.AgentID)
				go func() {
					agent.Run(ctx, turn)
				}()

			case "wait":
				if ec.RoomID == "" {
					// One-shot: agents are done
					result.Messages = sess.MainRoom.History()
					return result
				}

			case "stop":
				result.Messages = sess.MainRoom.History()
				return result
			}
		}
	}

	result.Messages = sess.MainRoom.History()
	return result
}

// --- Assertions ---

// AssertAgentSpoke fails if the given agent has no messages in the chat history.
func (r *FloorResult) AssertAgentSpoke(t *testing.T, agentID string) {
	t.Helper()
	for _, m := range r.Messages {
		if m.From == agentID {
			return
		}
	}
	t.Errorf("agent %s did not speak", agentID)
}

// AssertToolCalled fails if no tool call event matches the given substring.
// Matches against the tool call title (e.g. "bash", "tasks.add_task").
func (r *FloorResult) AssertToolCalled(t *testing.T, toolSubstr string) {
	t.Helper()
	for _, ev := range r.Events {
		if ev["type"] == "tool_call_started" || ev["type"] == "tool_call_result" {
			if title, ok := ev["title"].(string); ok && strings.Contains(title, toolSubstr) {
				return
			}
		}
	}
	t.Errorf("no tool call matching %q found", toolSubstr)
}

// AssertNoErrors fails if any agent error events were emitted.
func (r *FloorResult) AssertNoErrors(t *testing.T) {
	t.Helper()
	for _, ev := range r.Events {
		if ev["type"] == "agent_error" {
			t.Errorf("agent %s error: %v", ev["agent_id"], ev["error"])
		}
	}
}

// AssertContentContains fails if no message from the given agent contains the substring.
func (r *FloorResult) AssertContentContains(t *testing.T, agentID string, substr string) {
	t.Helper()
	for _, m := range r.Messages {
		if m.From == agentID && strings.Contains(m.Content, substr) {
			return
		}
	}
	t.Errorf("no message from %s contains %q", agentID, substr)
}

// --- LLM Evaluation ---

// EvalResult holds the LLM evaluation output.
type EvalResult struct {
	Score     int    `json:"score"`
	Reasoning string `json:"reasoning"`
}

// Eval runs an LLM-as-judge evaluation on the floor's conversation.
// Uses the first LLM agent in the blueprint for the LLM config.
func (r *FloorResult) Eval(t *testing.T, evalPrompt string) *EvalResult {
	t.Helper()

	// Find the first LLM agent for config
	var agentID string
	for _, a := range r.bp.Agents {
		if a.Type == "" || a.Type == "llm" {
			agentID = a.ID
			break
		}
	}
	if agentID == "" {
		t.Fatal("ofctest: no LLM agent found in blueprint for evaluation")
	}

	// Build conversation text from messages
	var sb strings.Builder
	for _, m := range r.Messages {
		fmt.Fprintf(&sb, "[%s]: %s\n", m.From, m.Content)
	}

	result, err := eval.Run(sb.String(), evalPrompt, agentID, r.bp)
	if err != nil {
		t.Fatalf("ofctest: eval failed: %v", err)
	}

	return &EvalResult{Score: result.Score, Reasoning: result.Reasoning}
}

// AssertMinScore fails if the evaluation score is below the minimum.
func (e *EvalResult) AssertMinScore(t *testing.T, min int) {
	t.Helper()
	if e.Score < min {
		t.Errorf("eval score %d < minimum %d: %s", e.Score, min, e.Reasoning)
	}
}
