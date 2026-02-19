package floor

// ANSI color codes
const (
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Reset  = "\033[0m"
	Cyan   = "\033[36m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Purple = "\033[35m"
	Red    = "\033[31m"
	Gray   = "\033[90m"
)

// agentColors is the palette cycled through for agent labels.
// @user always gets Cyan; agents get the rest in order.
var agentColors = []string{Green, Purple, Yellow, Blue, Red}

// ToolInteraction stores one tool call and its result.
type ToolInteraction struct {
	Command string
	Output  string
}

// FloorMessage is a floor-level message (distinct from llm.Message which is for the API).
type FloorMessage struct {
	FromID           string            // "@user", "@data", "@code"
	Content          string            // The text content
	ToolInteractions []ToolInteraction // Tool calls made during this turn
}

// Frame represents one level in the delegation chain.
// Caller asked Callee a question via @mention?
type Frame struct {
	Caller string // e.g. "@data"
	Callee string // e.g. "@code"
}
