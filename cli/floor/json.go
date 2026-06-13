package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// JSONFrontend outputs all floor events as JSONL (one JSON object per line) to a writer.
// Designed for machine-readable output: testing, evaluation, and piping to other tools.
type JSONFrontend struct {
	encoder *json.Encoder
	logFile string
	debug   bool
	out     *Output // for log file only (stdout is reserved for JSON)
}

// NewJSONFrontend creates a JSON frontend that writes JSONL to stdout.
func NewJSONFrontend(logFile string, debug bool) *JSONFrontend {
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
func (j *JSONFrontend) RunLoop(floor *Floor, ctrl *Controller, agents map[string]Agent, initialPrompt string) error {
	// Set up log file (stdout is reserved for JSON)
	if j.logFile != "" || j.debug {
		j.out = NewOutput(j.logFile, j.debug)
		defer j.out.Close()
	}

	// Start floor infrastructure
	if err := floor.Start(func(msg string) {
		j.emit(map[string]interface{}{
			"type": "system_info",
			"text": msg,
		})
	}); err != nil {
		return err
	}
	defer floor.Stop()

	// Emit floor_started
	agentIDs := make([]string, 0, len(floor.Blueprint.Agents))
	for _, a := range floor.Blueprint.Agents {
		agentIDs = append(agentIDs, a.ID)
	}
	furnitureNames := make([]string, 0, len(floor.Furniture))
	for name := range floor.Furniture {
		furnitureNames = append(furnitureNames, name)
	}
	j.emit(map[string]interface{}{
		"type":      "floor_started",
		"name":      floor.Blueprint.Name,
		"agents":    agentIDs,
		"furniture": furnitureNames,
	})

	sess := floor.DefaultSession()

	// Post initial prompt
	if initialPrompt != "" {
		sess.MainRoom.PostUserInput(initialPrompt)
	}

	oneShot := initialPrompt != "" && !IsCommand(initialPrompt)

	var cancelAgent context.CancelFunc

	unified := sess.StartUnified()

	onCloseInfo := func(info string) {
		j.emit(map[string]interface{}{"type": "system_info", "text": info})
	}

	for tagged := range unified {
		ec, ok := ResolveEventContext(sess, ctrl, tagged)
		if !ok {
			continue
		}
		ev := tagged.Event

		// Serialize and emit the event
		payload := EventJSON(ev)
		if payload != nil {
			if ec.RoomID != "" {
				payload["room_id"] = ec.RoomID
			}
			j.emit(payload)
		}

		// Handle controller decisions (same logic as CLI)
		switch ev.(type) {
		case MessagePosted, AgentPassedEvent, AgentErrorEvent:
			decision := DecideAndAutoClose(ec, ev, sess, ctrl, onCloseInfo)

			switch decision.Action {
			case "trigger":
				agent, ok := agents[decision.AgentID]
				if !ok {
					j.emit(map[string]interface{}{
						"type": "system_info",
						"text": fmt.Sprintf("unknown agent %s", decision.AgentID),
					})
					continue
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancelAgent = cancel
				turn := NewAgentTurn(ec.Sess, ec.Sess.MainRoom, ec.Sess.Floor, decision.AgentID)
				go func() {
					defer cancel()
					agent.Run(ctx, turn)
				}()

			case "wait":
				if oneShot && ec.RoomID == "" {
					j.emit(map[string]interface{}{"type": "floor_stopped"})
					return nil
				}

			case "stop":
				j.emit(map[string]interface{}{"type": "floor_stopped"})
				return nil
			}

		case UserCommandEvent:
			cmd := ev.(UserCommandEvent)
			decision := HandleCommand(cmd.Command, sess, ctrl)
			switch decision.Action {
			case "stop":
				j.emit(map[string]interface{}{"type": "floor_stopped"})
				return nil
			case "room_created", "room_closed":
				j.emit(map[string]interface{}{
					"type": "system_info",
					"text": decision.Info,
				})
			case "error":
				j.emit(map[string]interface{}{
					"type":  "system_info",
					"text":  decision.Info,
				})
			}
		}
	}

	_ = cancelAgent // suppress unused warning
	j.emit(map[string]interface{}{"type": "floor_stopped"})
	return nil
}
