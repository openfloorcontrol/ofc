package floor

import (
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
)

func testBlueprint() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@data", Activation: "always", ToolContext: "full"},
			{ID: "@code", Activation: "always", ToolContext: "full"},
			{ID: "@designer", Activation: "always", ToolContext: "full"},
		},
	}
}

func TestRoomCreation(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	defer floor.Chat.Close()

	room, err := floor.CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze the data")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	if room.ID != "#analysis" {
		t.Errorf("expected room ID #analysis, got %s", room.ID)
	}
	if room.Creator != "@user" {
		t.Errorf("expected creator @user, got %s", room.Creator)
	}
	if !room.AgentIDs["@data"] || !room.AgentIDs["@code"] {
		t.Errorf("expected @data and @code in room, got %v", room.AgentIDs)
	}
	if room.AgentIDs["@designer"] {
		t.Errorf("@designer should not be in room")
	}

	// Check agent room tracking
	if floor.AgentRoom("@data") != "#analysis" {
		t.Errorf("expected @data in #analysis, got %q", floor.AgentRoom("@data"))
	}
	if floor.AgentRoom("@code") != "#analysis" {
		t.Errorf("expected @code in #analysis, got %q", floor.AgentRoom("@code"))
	}
	if floor.AgentRoom("@designer") != "" {
		t.Errorf("expected @designer on main floor, got %q", floor.AgentRoom("@designer"))
	}
}

func TestRoomDuplicateCreation(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	defer floor.Chat.Close()

	_, err := floor.CreateRoom("#analysis", "@user", []string{"@data"}, "")
	if err != nil {
		t.Fatalf("first CreateRoom failed: %v", err)
	}

	_, err = floor.CreateRoom("#analysis", "@user", []string{"@code"}, "")
	if err == nil {
		t.Fatal("expected error for duplicate room, got nil")
	}
}

func TestRoomAgentAlreadyInRoom(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	defer floor.Chat.Close()

	_, err := floor.CreateRoom("#room1", "@user", []string{"@data"}, "")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// @data is already in #room1, can't join #room2
	_, err = floor.CreateRoom("#room2", "@user", []string{"@data"}, "")
	if err == nil {
		t.Fatal("expected error for agent already in room, got nil")
	}
}

func TestRoomIsolation(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)

	// Drain main floor events so Post doesn't block
	go func() { for range floor.Chat.Events() {} }()

	// Post a message on main floor before creating room
	floor.Chat.Post(ChatMessage{From: "@user", Content: "hello everyone"})

	// All agents should see it
	if floor.AgentContexts["@data"].Len() != 1 {
		t.Fatalf("@data: expected 1 entry, got %d", floor.AgentContexts["@data"].Len())
	}
	if floor.AgentContexts["@designer"].Len() != 1 {
		t.Fatalf("@designer: expected 1 entry, got %d", floor.AgentContexts["@designer"].Len())
	}

	// Create room with @data and @code
	room, err := floor.CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// Drain room events so Post doesn't block
	go func() { for range room.Chat.Events() {} }()

	// Post to main floor — @designer sees it, @data and @code do NOT
	floor.Chat.Post(ChatMessage{From: "@user", Content: "main floor message"})

	// @designer should have 2 entries (original + new)
	if floor.AgentContexts["@designer"].Len() != 2 {
		t.Errorf("@designer: expected 2 entries, got %d", floor.AgentContexts["@designer"].Len())
	}

	// @data should have 1 (original) + 1 (system msg about room) = 2
	// but NOT the "main floor message"
	dataEntries := floor.AgentContexts["@data"].Entries()
	for _, e := range dataEntries {
		if e.Content == "main floor message" {
			t.Error("@data should NOT see main floor messages while in room")
		}
	}

	// Post to room — only room agents see it
	room.Chat.Post(ChatMessage{From: "@user", Content: "room message"})

	// @data should now see the room message
	dataEntries = floor.AgentContexts["@data"].Entries()
	foundRoomMsg := false
	for _, e := range dataEntries {
		if e.Content == "room message" {
			foundRoomMsg = true
		}
	}
	if !foundRoomMsg {
		t.Error("@data should see room message")
	}

	// @designer should NOT see room message
	designerEntries := floor.AgentContexts["@designer"].Entries()
	for _, e := range designerEntries {
		if e.Content == "room message" {
			t.Error("@designer should NOT see room messages")
		}
	}
}

func TestRoomAgentContext(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	go func() { for range floor.Chat.Events() {} }()

	_, err := floor.CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// @data should have a system message about the room transition
	dataEntries := floor.AgentContexts["@data"].Entries()
	if len(dataEntries) != 1 {
		t.Fatalf("@data: expected 1 system entry, got %d", len(dataEntries))
	}
	if dataEntries[0].From != "@system" {
		t.Errorf("expected @system, got %q", dataEntries[0].From)
	}
	if dataEntries[0].Content == "" {
		t.Error("system message should not be empty")
	}
}

func TestRoomClose(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	go func() { for range floor.Chat.Events() {} }()

	room, err := floor.CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Chat.Events() {} }()

	// Post a message in the room
	room.Chat.Post(ChatMessage{From: "@data", Content: "analysis complete"})

	// Close the room
	if err := floor.CloseRoom("#analysis"); err != nil {
		t.Fatalf("CloseRoom failed: %v", err)
	}

	// Room should be closed
	if !room.IsClosed() {
		t.Error("room should be closed")
	}

	// Agents should be back on main floor
	if floor.AgentRoom("@data") != "" {
		t.Errorf("@data should be on main floor, got %q", floor.AgentRoom("@data"))
	}
	if floor.AgentRoom("@code") != "" {
		t.Errorf("@code should be on main floor, got %q", floor.AgentRoom("@code"))
	}

	// Agents should have system message about returning
	dataEntries := floor.AgentContexts["@data"].Entries()
	lastEntry := dataEntries[len(dataEntries)-1]
	if lastEntry.From != "@system" {
		t.Errorf("expected last entry from @system, got %q", lastEntry.From)
	}

	// Main floor should have summary message
	history := floor.Chat.History()
	foundSummary := false
	for _, m := range history {
		if m.From == "@system" && m.Content != "" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Error("expected summary message on main floor after room close")
	}
}

func TestRoomCloseAgentsReceiveMainFloorMessages(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	go func() { for range floor.Chat.Events() {} }()

	room, err := floor.CreateRoom("#analysis", "@user", []string{"@data"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Chat.Events() {} }()

	// Close the room
	if err := floor.CloseRoom("#analysis"); err != nil {
		t.Fatalf("CloseRoom failed: %v", err)
	}

	// Post to main floor after room is closed
	floor.Chat.Post(ChatMessage{From: "@user", Content: "welcome back"})

	// @data should now receive main floor messages again
	dataEntries := floor.AgentContexts["@data"].Entries()
	foundWelcome := false
	for _, e := range dataEntries {
		if e.Content == "welcome back" {
			foundWelcome = true
		}
	}
	if !foundWelcome {
		t.Error("@data should receive main floor messages after returning from room")
	}
}

func TestViewForRoom(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	go func() { for range floor.Chat.Events() {} }()

	room, err := floor.CreateRoom("#analysis", "@user", []string{"@data"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Chat.Events() {} }()

	// ViewForRoom should return a floor with room's Chat
	view := floor.ViewForRoom(room)
	if view.Chat != room.Chat {
		t.Error("ViewForRoom Chat should be room's Chat")
	}
	if view.Sandbox != floor.Sandbox {
		t.Error("ViewForRoom should share Sandbox")
	}
	if view.Blueprint != floor.Blueprint {
		t.Error("ViewForRoom should share Blueprint")
	}

	// Post via view should go to room Chat, not main floor Chat
	view.Chat.Post(ChatMessage{From: "@data", Content: "room response"})

	roomHistory := room.Chat.History()
	if len(roomHistory) != 1 || roomHistory[0].Content != "room response" {
		t.Errorf("expected room history to contain 'room response', got %v", roomHistory)
	}

	mainHistory := floor.Chat.History()
	for _, m := range mainHistory {
		if m.Content == "room response" {
			t.Error("room response should NOT appear in main floor history")
		}
	}
}

func TestRoomControllerFiltersAgents(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	defer floor.Chat.Close()

	room, err := floor.CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// Room controller should only know about @data and @code
	ctrl := room.Controller
	if len(ctrl.Blueprint.Agents) != 2 {
		t.Fatalf("expected 2 agents in room controller, got %d", len(ctrl.Blueprint.Agents))
	}

	agentIDs := make(map[string]bool)
	for _, a := range ctrl.Blueprint.Agents {
		agentIDs[a.ID] = true
	}
	if !agentIDs["@data"] || !agentIDs["@code"] {
		t.Errorf("expected @data and @code in room controller, got %v", agentIDs)
	}
	if agentIDs["@designer"] {
		t.Error("@designer should not be in room controller")
	}
}

func TestRoomMissedMainFloorMessages(t *testing.T) {
	// When agents are in a room, they miss main floor messages.
	// This is by design — "just like people."
	bp := testBlueprint()
	floor := NewFloor(bp)
	go func() { for range floor.Chat.Events() {} }()

	room, err := floor.CreateRoom("#analysis", "@user", []string{"@data"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Chat.Events() {} }()

	// Post several messages to main floor while @data is in room
	floor.Chat.Post(ChatMessage{From: "@user", Content: "msg1"})
	floor.Chat.Post(ChatMessage{From: "@designer", Content: "msg2"})
	floor.Chat.Post(ChatMessage{From: "@user", Content: "msg3"})

	// Close room — @data returns
	floor.CloseRoom("#analysis")

	// @data should NOT have msg1, msg2, msg3 — no backfill
	dataEntries := floor.AgentContexts["@data"].Entries()
	for _, e := range dataEntries {
		if e.Content == "msg1" || e.Content == "msg2" || e.Content == "msg3" {
			t.Errorf("@data should have missed main floor message %q", e.Content)
		}
	}
}
