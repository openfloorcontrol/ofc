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

// RunLoop is the event-driven main loop for JSON output.
func (j *JSONFrontend) RunLoop(fl *floor.Floor, initialPrompt string) error {
	// Set up log file (stdout is reserved for JSON)
	if j.logFile != "" || j.debug {
		j.out = NewOutput(j.logFile, j.debug)
		defer j.out.Close()
	}

	// Start floor infrastructure
	if err := fl.Start(func(msg string) {
		j.emit(map[string]interface{}{
			"type": "system_info",
			"text": msg,
		})
	}); err != nil {
		return err
	}
	defer fl.Stop()

	// Emit floor_started
	agentIDs := make([]string, 0, len(fl.Blueprint.Agents))
	for _, a := range fl.Blueprint.Agents {
		agentIDs = append(agentIDs, a.ID)
	}
	furnitureNames := make([]string, 0, len(fl.Furniture))
	for name := range fl.Furniture {
		furnitureNames = append(furnitureNames, name)
	}
	j.emit(map[string]interface{}{
		"type":      "floor_started",
		"name":      fl.Blueprint.Name,
		"agents":    agentIDs,
		"furniture": furnitureNames,
	})

	sess := fl.DefaultSession()
	events := sess.Subscribe()
	defer sess.Unsubscribe(events)
	sess.Start()

	// Post initial prompt
	if initialPrompt != "" {
		sess.MainRoom.PostUserInput(initialPrompt)
	}

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
