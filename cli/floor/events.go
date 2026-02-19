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

// AgentError is sent when a runner encounters an error.
type AgentError struct {
	AgentID string
	Err     error
	Partial string // any content produced before the error
}

// UserCommand is sent for slash commands (/quit, /clear).
type UserCommand struct {
	Command string
}

// --- Outbound events (from controller) ---

// PromptAgent tells the coordinator to dispatch a runner for this agent.
type PromptAgent struct {
	AgentID string
}

// WaitingForUser indicates the turn has returned to the user.
type WaitingForUser struct{}

// ConversationCleared indicates /clear was processed.
type ConversationCleared struct{}

// FloorStopped indicates /quit was processed.
type FloorStopped struct{}

// SystemInfo is an informational message (sandbox ready, agent started, etc.).
type SystemInfo struct {
	Text string
}

// --- Stream events (runner → frontend, bypass controller) ---

// TokenStreamed is a single token received from an agent.
type TokenStreamed struct {
	AgentID string
	Token   string
}

// ToolCallStarted indicates an agent started a tool call.
type ToolCallStarted struct {
	AgentID string
	Title   string
}

// ToolCallResult is the output of a completed tool call.
type ToolCallResult struct {
	AgentID string
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

// Seal the interface — only floor package types can implement Event.
func (UserMessage) eventMarker()          {}
func (AgentDone) eventMarker()            {}
func (AgentPassed) eventMarker()          {}
func (AgentError) eventMarker()           {}
func (UserCommand) eventMarker()          {}
func (PromptAgent) eventMarker()          {}
func (WaitingForUser) eventMarker()       {}
func (ConversationCleared) eventMarker()  {}
func (FloorStopped) eventMarker()         {}
func (SystemInfo) eventMarker()           {}
func (TokenStreamed) eventMarker()        {}
func (ToolCallStarted) eventMarker()      {}
func (ToolCallResult) eventMarker()       {}
func (AgentThinking) eventMarker()        {}
func (AgentLabel) eventMarker()           {}
