package floor

import (
	"strings"
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
	defer floor.DefaultSession().MainRoom.Close()

	room, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze the data")
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
	if floor.DefaultSession().AgentRoom("@data") != "#analysis" {
		t.Errorf("expected @data in #analysis, got %q", floor.DefaultSession().AgentRoom("@data"))
	}
	if floor.DefaultSession().AgentRoom("@code") != "#analysis" {
		t.Errorf("expected @code in #analysis, got %q", floor.DefaultSession().AgentRoom("@code"))
	}
	if floor.DefaultSession().AgentRoom("@designer") != "" {
		t.Errorf("expected @designer on main floor, got %q", floor.DefaultSession().AgentRoom("@designer"))
	}
}

func TestRoomDuplicateCreation(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	defer floor.DefaultSession().MainRoom.Close()

	_, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data"}, "")
	if err != nil {
		t.Fatalf("first CreateRoom failed: %v", err)
	}

	_, err = floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@code"}, "")
	if err == nil {
		t.Fatal("expected error for duplicate room, got nil")
	}
}

func TestRoomAgentAlreadyInRoom(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	defer floor.DefaultSession().MainRoom.Close()

	_, err := floor.DefaultSession().CreateRoom("#room1", "@user", []string{"@data"}, "")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// @data is already in #room1, can't join #room2
	_, err = floor.DefaultSession().CreateRoom("#room2", "@user", []string{"@data"}, "")
	if err == nil {
		t.Fatal("expected error for agent already in room, got nil")
	}
}

func TestRoomIsolation(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)

	// Drain main floor events so Post doesn't block
	go func() { for range floor.DefaultSession().MainRoom.Events() {} }()

	// Post a message on main floor before creating room
	floor.DefaultSession().MainRoom.Post(ChatMessage{From: "@user", Content: "hello everyone"})

	// All agents should see it
	if floor.DefaultSession().AgentContexts["@data"].Len() != 1 {
		t.Fatalf("@data: expected 1 entry, got %d", floor.DefaultSession().AgentContexts["@data"].Len())
	}
	if floor.DefaultSession().AgentContexts["@designer"].Len() != 1 {
		t.Fatalf("@designer: expected 1 entry, got %d", floor.DefaultSession().AgentContexts["@designer"].Len())
	}

	// Create room with @data and @code
	room, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// Drain room events so Post doesn't block
	go func() { for range room.Events() {} }()

	// Post to main floor — @designer sees it, @data and @code do NOT
	floor.DefaultSession().MainRoom.Post(ChatMessage{From: "@user", Content: "main floor message"})

	// @designer should have 2 entries (original + new)
	if floor.DefaultSession().AgentContexts["@designer"].Len() != 2 {
		t.Errorf("@designer: expected 2 entries, got %d", floor.DefaultSession().AgentContexts["@designer"].Len())
	}

	// @data should have 1 (original) + 1 (system msg about room) = 2
	// but NOT the "main floor message"
	dataEntries := floor.DefaultSession().AgentContexts["@data"].Entries()
	for _, e := range dataEntries {
		if e.Content == "main floor message" {
			t.Error("@data should NOT see main floor messages while in room")
		}
	}

	// Post to room — only room agents see it
	room.Post(ChatMessage{From: "@user", Content: "room message"})

	// @data should now see the room message
	dataEntries = floor.DefaultSession().AgentContexts["@data"].Entries()
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
	designerEntries := floor.DefaultSession().AgentContexts["@designer"].Entries()
	for _, e := range designerEntries {
		if e.Content == "room message" {
			t.Error("@designer should NOT see room messages")
		}
	}
}

func TestRoomAgentContext(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	go func() { for range floor.DefaultSession().MainRoom.Events() {} }()

	_, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// @data should have a system message about the room transition
	dataEntries := floor.DefaultSession().AgentContexts["@data"].Entries()
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
	go func() { for range floor.DefaultSession().MainRoom.Events() {} }()

	room, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Events() {} }()

	// Post a message in the room
	room.Post(ChatMessage{From: "@data", Content: "analysis complete"})

	// Close the room
	if err := floor.DefaultSession().CloseRoom("#analysis"); err != nil {
		t.Fatalf("CloseRoom failed: %v", err)
	}

	// Room should be closed
	if !room.IsClosed() {
		t.Error("room should be closed")
	}

	// Agents should be back on main floor
	if floor.DefaultSession().AgentRoom("@data") != "" {
		t.Errorf("@data should be on main floor, got %q", floor.DefaultSession().AgentRoom("@data"))
	}
	if floor.DefaultSession().AgentRoom("@code") != "" {
		t.Errorf("@code should be on main floor, got %q", floor.DefaultSession().AgentRoom("@code"))
	}

	// Agents should have system message about returning
	dataEntries := floor.DefaultSession().AgentContexts["@data"].Entries()
	lastEntry := dataEntries[len(dataEntries)-1]
	if lastEntry.From != "@system" {
		t.Errorf("expected last entry from @system, got %q", lastEntry.From)
	}

	// Main floor should have summary message
	history := floor.DefaultSession().MainRoom.History()
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
	go func() { for range floor.DefaultSession().MainRoom.Events() {} }()

	room, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Events() {} }()

	// Close the room
	if err := floor.DefaultSession().CloseRoom("#analysis"); err != nil {
		t.Fatalf("CloseRoom failed: %v", err)
	}

	// Post to main floor after room is closed
	floor.DefaultSession().MainRoom.Post(ChatMessage{From: "@user", Content: "welcome back"})

	// @data should now receive main floor messages again
	dataEntries := floor.DefaultSession().AgentContexts["@data"].Entries()
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

func TestSessionForRoom(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	sess := floor.DefaultSession()
	go func() { for range sess.MainRoom.Events() {} }()

	room, err := sess.CreateRoom("#analysis", "@user", []string{"@data"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Events() {} }()

	// ForRoom should return a session view with room's Chat,
	// sharing Floor (and thus Sandbox/Blueprint via Floor) with the parent.
	view := sess.ForRoom(room)
	if view.MainRoom != room {
		t.Error("ForRoom Chat should be room's Chat")
	}
	if view.Floor != floor {
		t.Error("ForRoom should share Floor")
	}
	if view.Floor.Sandbox != floor.Sandbox {
		t.Error("ForRoom should expose same Sandbox via Floor")
	}
	if view.Floor.Blueprint != floor.Blueprint {
		t.Error("ForRoom should expose same Blueprint via Floor")
	}

	// Post via view should go to room Chat, not main session Chat
	view.MainRoom.Post(ChatMessage{From: "@data", Content: "room response"})

	roomHistory := room.History()
	if len(roomHistory) != 1 || roomHistory[0].Content != "room response" {
		t.Errorf("expected room history to contain 'room response', got %v", roomHistory)
	}

	mainHistory := sess.MainRoom.History()
	for _, m := range mainHistory {
		if m.Content == "room response" {
			t.Error("room response should NOT appear in main session history")
		}
	}
}

func TestRoomControllerFiltersAgents(t *testing.T) {
	bp := testBlueprint()
	floor := NewFloor(bp)
	defer floor.DefaultSession().MainRoom.Close()

	room, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data", "@code"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}

	// Room controller should only consider @data and @code from the
	// floor's agent set.
	ctrl := room.Controller
	roomAgents := ctrl.agents()
	if len(roomAgents) != 2 {
		t.Fatalf("expected 2 agents in room controller, got %d", len(roomAgents))
	}

	agentIDs := make(map[string]bool)
	for _, a := range roomAgents {
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
	go func() { for range floor.DefaultSession().MainRoom.Events() {} }()

	room, err := floor.DefaultSession().CreateRoom("#analysis", "@user", []string{"@data"}, "analyze")
	if err != nil {
		t.Fatalf("CreateRoom failed: %v", err)
	}
	go func() { for range room.Events() {} }()

	// Post several messages to main floor while @data is in room
	floor.DefaultSession().MainRoom.Post(ChatMessage{From: "@user", Content: "msg1"})
	floor.DefaultSession().MainRoom.Post(ChatMessage{From: "@designer", Content: "msg2"})
	floor.DefaultSession().MainRoom.Post(ChatMessage{From: "@user", Content: "msg3"})

	// Close room — @data returns
	floor.DefaultSession().CloseRoom("#analysis")

	// @data should NOT have msg1, msg2, msg3 — no backfill
	dataEntries := floor.DefaultSession().AgentContexts["@data"].Entries()
	for _, e := range dataEntries {
		if e.Content == "msg1" || e.Content == "msg2" || e.Content == "msg3" {
			t.Errorf("@data should have missed main floor message %q", e.Content)
		}
	}
}

func TestTryAutoCloseRoom(t *testing.T) {
	bp := &blueprint.Blueprint{
		Name: "test",
		Agents: []blueprint.Agent{
			{ID: "@data", Activation: "always", ToolContext: "full"},
			{ID: "@code", Activation: "mention", ToolContext: "full"},
		},
	}
	floor := NewFloor(bp)
	ctrl := NewController(floor)

	// Create a room
	room, err := floor.DefaultSession().CreateRoom("#work", "@user", []string{"@data", "@code"}, "do stuff")
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	ctrl.RoomBound["@data"] = true
	ctrl.RoomBound["@code"] = true

	// Post a message so room has some history
	room.Post(ChatMessage{From: "@data", Content: "done with the analysis"})

	// Non-room event — should not close anything
	info := TryAutoCloseRoom("", Decision{Action: "wait"}, floor.DefaultSession(), ctrl)
	if info != "" {
		t.Errorf("expected no auto-close for main floor, got: %s", info)
	}

	// Room event but action is "trigger" — should not close
	info = TryAutoCloseRoom("#work", Decision{Action: "trigger", AgentID: "@code"}, floor.DefaultSession(), ctrl)
	if info != "" {
		t.Errorf("expected no auto-close for trigger, got: %s", info)
	}

	// Room event with "wait" — should auto-close
	info = TryAutoCloseRoom("#work", Decision{Action: "wait"}, floor.DefaultSession(), ctrl)
	if info == "" {
		t.Fatal("expected auto-close info, got empty")
	}

	// Room should be closed
	if !room.IsClosed() {
		t.Error("room should be closed after auto-close")
	}

	// Agents should be unbound
	if ctrl.RoomBound["@data"] || ctrl.RoomBound["@code"] {
		t.Error("agents should be removed from RoomBound after auto-close")
	}

	// Summary should be posted to main floor
	mainHistory := floor.DefaultSession().MainRoom.History()
	found := false
	for _, m := range mainHistory {
		if m.From == "@system" && strings.Contains(m.Content, "#work") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected summary posted to main floor after auto-close")
	}

	// Calling again on closed room — should be a no-op
	info = TryAutoCloseRoom("#work", Decision{Action: "wait"}, floor.DefaultSession(), ctrl)
	if info != "" {
		t.Errorf("expected no-op for already-closed room, got: %s", info)
	}
}
