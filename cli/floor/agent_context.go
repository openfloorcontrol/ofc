package floor

import "sync"

// AgentContext accumulates messages for a specific agent.
// It acts as the agent's personal "memory" — what it has heard across rooms.
// Messages are pushed via the MessageListener interface when the agent
// is subscribed to a Chat.
type AgentContext struct {
	agentID string

	mu      sync.RWMutex
	entries []*ChatMessage // pointers to messages in some Chat (+ system messages)
	sentIdx int            // index up to which entries have been sent (for Delta)
}

// NewAgentContext creates a context for the given agent.
func NewAgentContext(agentID string) *AgentContext {
	return &AgentContext{
		agentID: agentID,
	}
}

// OnMessage implements MessageListener. Called by Chat.Post() when a message
// is posted to the Chat this context is listening to.
func (ac *AgentContext) OnMessage(msg *ChatMessage) {
	ac.mu.Lock()
	ac.entries = append(ac.entries, msg)
	ac.mu.Unlock()
}

// Entries returns all accumulated messages (snapshot, safe for concurrent use).
// Used by LLMAgent to build full context.
func (ac *AgentContext) Entries() []*ChatMessage {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	result := make([]*ChatMessage, len(ac.entries))
	copy(result, ac.entries)
	return result
}

// Delta returns messages received since the last MarkSent() call.
// Used by ACPAgent to send only new messages.
func (ac *AgentContext) Delta() []*ChatMessage {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	if ac.sentIdx >= len(ac.entries) {
		return nil
	}
	delta := make([]*ChatMessage, len(ac.entries)-ac.sentIdx)
	copy(delta, ac.entries[ac.sentIdx:])
	return delta
}

// MarkSent records that all current entries have been sent to the agent.
// Subsequent Delta() calls will only return messages added after this point.
func (ac *AgentContext) MarkSent() {
	ac.mu.Lock()
	ac.sentIdx = len(ac.entries)
	ac.mu.Unlock()
}

// AppendSystem inserts a system-level message that only this agent sees.
// Used for room transitions ("You moved to #analysis with @code").
func (ac *AgentContext) AppendSystem(text string) {
	ac.mu.Lock()
	ac.entries = append(ac.entries, &ChatMessage{
		From:    "@system",
		Content: text,
	})
	ac.mu.Unlock()
}

// Clear resets the context. Called when the chat is cleared (/clear).
func (ac *AgentContext) Clear() {
	ac.mu.Lock()
	ac.entries = nil
	ac.sentIdx = 0
	ac.mu.Unlock()
}

// Len returns the number of accumulated entries.
func (ac *AgentContext) Len() int {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return len(ac.entries)
}
