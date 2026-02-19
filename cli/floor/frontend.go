package floor

import "io"

// Frontend renders floor events and produces user input.
// The CLI terminal is one implementation; a web UI (SSE/WebSocket) is another.
type Frontend interface {
	// Render displays an event to the user.
	Render(event Event)

	// ReadInput blocks until the user provides input.
	// Returns a UserMessage or UserCommand, or an error on EOF/interrupt.
	ReadInput() (Event, error)

	// LogWriter returns an io.Writer for the log file, or nil if no log is active.
	// Used to pass log access to subsystems (e.g. ACP client debug output).
	LogWriter() io.Writer

	// Close cleans up resources (close log file, etc.).
	Close()
}

// StreamSink receives high-frequency streaming events from agent runners.
// Separated from Frontend for clarity, but a CLI frontend implements both.
type StreamSink interface {
	OnStream(event Event)
}
