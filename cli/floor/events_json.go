package floor

import (
	"encoding/json"
	"errors"
	"fmt"
)

// EventJSON converts a ChatEvent to a JSON-serializable map.
// Returns nil for events that should not be serialized (e.g.
// UserCommandEvent). Used by the SSE endpoint and the JSON frontend.
func EventJSON(ev ChatEvent) map[string]interface{} {
	switch e := ev.(type) {
	case MessagePosted:
		return map[string]interface{}{
			"type": "message_posted",
			"seq":  e.Seq,
			"message": map[string]interface{}{
				"from":              e.Message.From,
				"content":           e.Message.Content,
				"reasoning":         e.Message.Reasoning,
				"tool_interactions": e.Message.ToolInteractions,
			},
		}
	case StreamEvent:
		switch se := e.Event.(type) {
		case TokenStreamed:
			return map[string]interface{}{
				"type":     "token",
				"agent_id": se.AgentID,
				"token":    se.Token,
			}
		case ToolCallStarted:
			return map[string]interface{}{
				"type":     "tool_call_started",
				"agent_id": se.AgentID,
				"id":       se.ID,
				"title":    se.Title,
			}
		case ToolCallOutput:
			return map[string]interface{}{
				"type":     "tool_call_output",
				"agent_id": se.AgentID,
				"id":       se.ID,
				"output":   se.Output,
			}
		case ToolCallResult:
			return map[string]interface{}{
				"type":     "tool_call_result",
				"agent_id": se.AgentID,
				"id":       se.ID,
				"title":    se.Title,
				"output":   se.Output,
			}
		case AgentLabel:
			return map[string]interface{}{
				"type":     "agent_label",
				"agent_id": se.AgentID,
			}
		case ThoughtStreamed:
			return map[string]interface{}{
				"type":     "thought",
				"agent_id": se.AgentID,
				"token":    se.Token,
			}
		case FurnitureUpdated:
			return map[string]interface{}{
				"type": "furniture_updated",
				"name": se.Name,
			}
		default:
			return nil
		}
	case AgentFinished:
		return map[string]interface{}{
			"type":     "agent_finished",
			"agent_id": e.AgentID,
		}
	case AgentPassedEvent:
		return map[string]interface{}{
			"type":     "agent_passed",
			"agent_id": e.AgentID,
		}
	case AgentErrorEvent:
		return map[string]interface{}{
			"type":     "agent_error",
			"agent_id": e.AgentID,
			"error":    e.Err.Error(),
		}
	case AgentStarted:
		return map[string]interface{}{
			"type":     "agent_started",
			"agent_id": e.AgentID,
		}
	case AwaitingInput:
		return map[string]interface{}{"type": "awaiting_input"}
	case InfoEvent:
		return map[string]interface{}{
			"type": "system_info",
			"text": e.Text,
		}
	case SessionCleared:
		return map[string]interface{}{"type": "session_cleared"}
	case SessionStopped:
		return map[string]interface{}{"type": "session_stopped"}
	default:
		return nil
	}
}

// eventJSON holds every field EventJSON writes, for ParseEventJSON.
type eventJSON struct {
	Type    string `json:"type"`
	RoomID  string `json:"room_id"`
	Seq     uint64 `json:"seq"`
	Message *struct {
		From             string            `json:"from"`
		Content          string            `json:"content"`
		Reasoning        string            `json:"reasoning"`
		ToolInteractions []ToolInteraction `json:"tool_interactions"`
	} `json:"message"`
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Output  string `json:"output"`
	Name    string `json:"name"`
	Error   string `json:"error"`
	Text    string `json:"text"`
}

// ParseEventJSON is the inverse of EventJSON: it decodes one serialized
// event, including the room_id the SSE stream adds for sub-rooms.
// AgentErrorEvent comes back with only its message (Partial is not
// serialized).
func ParseEventJSON(data []byte) (TaggedEvent, error) {
	var j eventJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return TaggedEvent{}, err
	}
	var ev ChatEvent
	switch j.Type {
	case "message_posted":
		if j.Message == nil {
			return TaggedEvent{}, fmt.Errorf("message_posted without message")
		}
		ev = MessagePosted{Seq: j.Seq, Message: ChatMessage{
			From:             j.Message.From,
			Content:          j.Message.Content,
			Reasoning:        j.Message.Reasoning,
			ToolInteractions: j.Message.ToolInteractions,
		}}
	case "token":
		ev = StreamEvent{Event: TokenStreamed{AgentID: j.AgentID, Token: j.Token}}
	case "thought":
		ev = StreamEvent{Event: ThoughtStreamed{AgentID: j.AgentID, Token: j.Token}}
	case "tool_call_started":
		ev = StreamEvent{Event: ToolCallStarted{AgentID: j.AgentID, ID: j.ID, Title: j.Title}}
	case "tool_call_output":
		ev = StreamEvent{Event: ToolCallOutput{AgentID: j.AgentID, ID: j.ID, Output: j.Output}}
	case "tool_call_result":
		ev = StreamEvent{Event: ToolCallResult{AgentID: j.AgentID, ID: j.ID, Title: j.Title, Output: j.Output}}
	case "agent_label":
		ev = StreamEvent{Event: AgentLabel{AgentID: j.AgentID}}
	case "furniture_updated":
		ev = StreamEvent{Event: FurnitureUpdated{Name: j.Name}}
	case "agent_finished":
		ev = AgentFinished{AgentID: j.AgentID}
	case "agent_passed":
		ev = AgentPassedEvent{AgentID: j.AgentID}
	case "agent_error":
		ev = AgentErrorEvent{AgentID: j.AgentID, Err: errors.New(j.Error)}
	case "agent_started":
		ev = AgentStarted{AgentID: j.AgentID}
	case "awaiting_input":
		ev = AwaitingInput{}
	case "system_info":
		ev = InfoEvent{Text: j.Text}
	case "session_cleared":
		ev = SessionCleared{}
	case "session_stopped":
		ev = SessionStopped{}
	default:
		return TaggedEvent{}, fmt.Errorf("unknown event type %q", j.Type)
	}
	return TaggedEvent{RoomID: j.RoomID, Event: ev}, nil
}
