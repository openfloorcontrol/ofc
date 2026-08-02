package floor

// EventJSON converts a ChatEvent to a JSON-serializable map.
// Returns nil for events that should not be serialized (e.g.
// UserCommandEvent). Used by the SSE endpoint and the JSON frontend.
func EventJSON(ev ChatEvent) map[string]interface{} {
	switch e := ev.(type) {
	case MessagePosted:
		return map[string]interface{}{
			"type": "message_posted",
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
	default:
		return nil
	}
}
