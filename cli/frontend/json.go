package frontend

import (
	"encoding/json"
	"io"
	"os"

	"github.com/openfloorcontrol/ofc/floor"
)

// JSONFrontend outputs all floor events as JSONL (one JSON object per line) to a writer.
// Designed for machine-readable output: testing, evaluation, and piping to other tools.
type JSONFrontend struct {
	encoder *json.Encoder
	logFile string
	debug   bool
	out     *Output // for log file only (stdout is reserved for JSON)
}

// NewJSON creates a JSON frontend that writes JSONL to stdout.
func NewJSON(logFile string, debug bool) *JSONFrontend {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return &JSONFrontend{
		encoder: enc,
		logFile: logFile,
		debug:   debug,
	}
}

func (j *JSONFrontend) emit(payload map[string]interface{}) {
	j.encoder.Encode(payload)
}

// LogWriter returns the log file writer (nil if no log file).
func (j *JSONFrontend) LogWriter() io.Writer {
	if j.out != nil {
		return j.out.LogWriter()
	}
	return nil
}

// Debug writes a debug message to the log file.
func (j *JSONFrontend) Debug(msg string) {
	if j.out != nil {
		j.out.Debug("%s", msg)
	}
}

// EmitInfo writes a system_info line, e.g. floor startup progress.
func (j *JSONFrontend) EmitInfo(text string) {
	j.emit(map[string]interface{}{"type": "system_info", "text": text})
}

// RunLoop writes the session's events as JSONL until the session stops
// (or, with an initial prompt, until it waits for the user). The session
// may be local or remote.
func (j *JSONFrontend) RunLoop(client SessionClient, initialPrompt string) error {
	// Set up log file (stdout is reserved for JSON)
	if j.logFile != "" || j.debug {
		j.out = NewOutput(j.logFile, j.debug)
		defer j.out.Close()
	}
	defer client.Close()

	info := client.Info()
	j.emit(map[string]interface{}{
		"type":      "floor_started",
		"name":      info.Name,
		"agents":    info.Agents,
		"furniture": info.Furniture,
		"session":   client.ID(),
	})

	if initialPrompt != "" {
		if err := client.PostUserInput(initialPrompt); err != nil {
			return err
		}
	}
	events := client.Events()

	oneShot := initialPrompt != "" && !floor.IsCommand(initialPrompt)

	for tagged := range events {
		if payload := floor.EventJSON(tagged.Event); payload != nil {
			if tagged.RoomID != "" {
				payload["room_id"] = tagged.RoomID
			}
			j.emit(payload)
		}

		switch tagged.Event.(type) {
		case floor.SessionStopped:
			j.emit(map[string]interface{}{"type": "floor_stopped"})
			return nil
		case floor.AwaitingInput:
			if oneShot {
				j.emit(map[string]interface{}{"type": "floor_stopped"})
				return nil
			}
		}
	}

	j.emit(map[string]interface{}{"type": "floor_stopped"})
	return nil
}
