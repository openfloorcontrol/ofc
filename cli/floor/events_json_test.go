package floor

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestParseEventJSONRoundTrip(t *testing.T) {
	events := []ChatEvent{
		MessagePosted{Seq: 7, Message: ChatMessage{From: "@a", Content: "hi", Reasoning: "r",
			ToolInteractions: []ToolInteraction{{Command: "ls", Output: "x"}}}},
		StreamEvent{Event: TokenStreamed{AgentID: "@a", Token: "t"}},
		StreamEvent{Event: ThoughtStreamed{AgentID: "@a", Token: "th"}},
		StreamEvent{Event: ToolCallStarted{AgentID: "@a", ID: "1", Title: "bash"}},
		StreamEvent{Event: ToolCallOutput{AgentID: "@a", ID: "1", Output: "o"}},
		StreamEvent{Event: ToolCallResult{AgentID: "@a", ID: "1", Title: "bash", Output: "done"}},
		StreamEvent{Event: AgentLabel{AgentID: "@a"}},
		StreamEvent{Event: FurnitureUpdated{Name: "tasks"}},
		AgentFinished{AgentID: "@a"},
		AgentPassedEvent{AgentID: "@a"},
		AgentErrorEvent{AgentID: "@a", Err: errors.New("boom")},
		AgentStarted{AgentID: "@a"},
		AwaitingInput{},
		InfoEvent{Text: "room closed"},
		SessionCleared{},
		SessionStopped{},
	}
	for _, ev := range events {
		payload := EventJSON(ev)
		payload["room_id"] = "#sub"
		data, _ := json.Marshal(payload)
		got, err := ParseEventJSON(data)
		if err != nil {
			t.Errorf("%T: %v", ev, err)
			continue
		}
		if got.RoomID != "#sub" {
			t.Errorf("%T: room = %q", ev, got.RoomID)
		}
		if e, ok := ev.(AgentErrorEvent); ok {
			g, _ := got.Event.(AgentErrorEvent)
			if g.AgentID != e.AgentID || g.Err == nil || g.Err.Error() != e.Err.Error() {
				t.Errorf("AgentErrorEvent: got %+v", got.Event)
			}
			continue
		}
		if !reflect.DeepEqual(got.Event, ev) {
			t.Errorf("round trip: got %#v, want %#v", got.Event, ev)
		}
	}
}

func TestParseEventJSONRejectsUnknownType(t *testing.T) {
	if _, err := ParseEventJSON([]byte(`{"type":"nope"}`)); err == nil {
		t.Error("expected an error")
	}
}
