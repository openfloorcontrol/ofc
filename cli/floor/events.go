package floor

// Event is the base interface for streaming/display events that flow
// through Room.PostStream and the TUI display channel. These are
// ephemeral — they are NOT stored in the session log (that's
// ChatEvent in chat.go).
//
// Sealed — only types in this package implement it.
type Event interface {
	eventMarker()
}

// --- Informational ---

// SystemInfo is an informational message (sandbox ready, agent started,
// debug output, etc.). Used by frontends to display non-chat status.
type SystemInfo struct {
	Text string
}

// AgentDone is a display marker the TUI uses to add a newline after an
// agent finishes streaming. Sent by the TUI's background goroutine to
// its Bubble Tea model in response to AgentFinished (chat.go).
type AgentDone struct {
	AgentID string
}

// --- Stream events (agent → frontend, bypass controller) ---

// TokenStreamed is a single token received from an agent.
type TokenStreamed struct {
	AgentID string
	Token   string
}

// ToolCallStarted indicates an agent started a tool call.
type ToolCallStarted struct {
	AgentID string
	ID      string // unique tool call ID
	Title   string
}

// ToolCallOutput streams output for an in-flight tool call (e.g. terminal output).
type ToolCallOutput struct {
	AgentID string
	ID      string // matches ToolCallStarted.ID
	Output  string
}

// ToolCallResult is the output of a completed tool call.
type ToolCallResult struct {
	AgentID string
	ID      string // matches ToolCallStarted.ID
	Title   string
	Output  string
}

// AgentThinking indicates an agent is processing (for spinners).
type AgentThinking struct {
	AgentID string
}

// AgentLabel is emitted before streaming begins so the frontend can render the agent's label.
type AgentLabel struct {
	AgentID string
}

// FurnitureUpdated is emitted when a furniture's state may have changed (after a Call).
type FurnitureUpdated struct {
	Name string // furniture name (e.g. "tasks")
}

// TaggedEvent wraps a ChatEvent with a room identifier.
// RoomID is "" for the main room.
type TaggedEvent struct {
	RoomID string
	Event  ChatEvent
}

// Seal the interface — only floor package types can implement Event.
func (SystemInfo) eventMarker()       {}
func (AgentDone) eventMarker()        {}
func (TokenStreamed) eventMarker()    {}
func (ToolCallStarted) eventMarker()  {}
func (ToolCallOutput) eventMarker()   {}
func (ToolCallResult) eventMarker()   {}
func (AgentThinking) eventMarker()    {}
func (AgentLabel) eventMarker()       {}
func (FurnitureUpdated) eventMarker() {}
