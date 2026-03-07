package floor

// Event is the base interface for all floor events.
// Sealed — only types in this package implement it.
type Event interface {
	eventMarker()
}

// --- Inbound events (to controller) ---

// UserMessage is sent when the user provides input.
type UserMessage struct {
	Content string
}

// AgentDone is sent when an agent finishes its full response.
type AgentDone struct {
	AgentID          string
	Content          string
	ToolInteractions []ToolInteraction
}

// AgentPassed is sent when an agent responds with [PASS].
type AgentPassed struct {
	AgentID string
}

// AgentError is sent when an agent encounters an error.
type AgentError struct {
	AgentID string
	Err     error
	Partial string // any content produced before the error
}

// UserCommand is sent for slash commands (/quit, /clear).
type UserCommand struct {
	Command string
}

// --- Informational ---

// SystemInfo is an informational message (sandbox ready, agent started, etc.).
type SystemInfo struct {
	Text string
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
// RoomID is "" for main floor events.
type TaggedEvent struct {
	RoomID string
	Event  ChatEvent
}

// Seal the interface — only floor package types can implement Event.
func (UserMessage) eventMarker()    {}
func (AgentDone) eventMarker()      {}
func (AgentPassed) eventMarker()    {}
func (AgentError) eventMarker()     {}
func (UserCommand) eventMarker()    {}
func (SystemInfo) eventMarker()     {}
func (TokenStreamed) eventMarker()  {}
func (ToolCallStarted) eventMarker() {}
func (ToolCallOutput) eventMarker()  {}
func (ToolCallResult) eventMarker()  {}
func (AgentThinking) eventMarker()  {}
func (AgentLabel) eventMarker()          {}
func (FurnitureUpdated) eventMarker()    {}
