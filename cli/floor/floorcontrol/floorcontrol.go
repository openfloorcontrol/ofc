// Package floorcontrol decides whom a room's last message addresses by
// asking a model. It implements floor.Addresser.
package floorcontrol

import (
	"context"
	"fmt"
	"strings"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	llmsdk "github.com/openfloorcontrol/ofc/llm"
)

// DefaultPrompt is the general part of the instructions; the
// participants, their turn prompts and the answer format follow it.
const DefaultPrompt = `You run the floor of a conversation between several participants: you decide who speaks next after the last message.

- call @x: the last message asks @x something or asks for their view, by name or with "@x?". @user is the human; an agent asking the human something calls @user.
- chime @x: nobody in particular was asked, but @x would want to react to the last message.
- nobody: the last message addresses no one new. Whoever is waiting for an answer gets the floor back, otherwise the human does.

Prefer nobody over chime unless a participant clearly has something to add.`

// Addresser asks a model whom a message addresses.
type Addresser struct {
	client  *llmsdk.Client
	model   string
	history int
	retries int
	prompt  string
}

// New builds an Addresser from a blueprint's resolved floor_control.
func New(fc blueprint.FloorControl) *Addresser {
	prompt := fc.Prompt
	if prompt == "" {
		prompt = DefaultPrompt
	}
	history := fc.History
	if history <= 0 {
		history = blueprint.DefaultFloorControlHistory
	}
	return &Addresser{
		client:  llmsdk.NewClient(fc.Endpoint, fc.APIKey),
		model:   fc.Model,
		history: history,
		retries: fc.Retries,
		prompt:  prompt,
	}
}

// Result is one decision and how it was reached.
type Result struct {
	Address floor.Address
	// Answers are the model's answers, one per attempt.
	Answers []string
	// Valid reports whether an answer was one of the options.
	Valid bool
	// Err is set when the model couldn't be asked.
	Err error
}

// Address implements floor.Addresser.
func (a *Addresser) Address(_ context.Context, req floor.AddressRequest) floor.Address {
	return a.Decide(req).Address
}

// Decide asks the model whom the last message of req.History addresses.
// An answer that isn't one of the offered options is asked again up to
// the configured number of retries; after that, and when the model
// can't be asked, the message addresses nobody.
func (a *Addresser) Decide(req floor.AddressRequest) Result {
	if len(req.History) == 0 {
		return Result{Address: floor.Address{Kind: floor.AddressNobody}, Valid: true}
	}
	opts := options(req)
	messages := []llmsdk.Message{
		{Role: "system", Content: a.systemPrompt(req, opts)},
		{Role: "user", Content: a.conversation(req)},
	}

	var res Result
	for attempt := 0; attempt <= a.retries; attempt++ {
		if attempt > 0 {
			messages = append(messages,
				llmsdk.Message{Role: "assistant", Content: res.Answers[len(res.Answers)-1]},
				llmsdk.Message{Role: "user", Content: "Reply with exactly one of these on the first line: " +
					strings.Join(opts, ", ") + ". Then give a short reason on the second line."})
		}
		out, err := a.client.ChatStream(a.model, messages, 0, nil, llmsdk.StreamHandler{})
		if err != nil {
			res.Err = err
			res.Address = floor.Address{Kind: floor.AddressNobody, Reason: fmt.Sprintf("floor control model failed: %v", err)}
			return res
		}
		res.Answers = append(res.Answers, out.Content)
		if addr, ok := parse(out.Content, opts); ok {
			res.Address, res.Valid = addr, true
			return res
		}
	}
	res.Address = floor.Address{Kind: floor.AddressNobody,
		Reason: fmt.Sprintf("invalid answer %q", firstLine(res.Answers[len(res.Answers)-1]))}
	return res
}

// options are the answers the model may give for req: a call to the user
// (unless the user wrote the last message), a call or chime for every
// agent that may speak except the last speaker, and nobody.
func options(req floor.AddressRequest) []string {
	last := req.History[len(req.History)-1].From
	var opts []string
	if last != "@user" {
		opts = append(opts, "call @user")
	}
	for _, ag := range req.Agents {
		if ag.ID != last {
			opts = append(opts, "call "+ag.ID, "chime "+ag.ID)
		}
	}
	return append(opts, "nobody")
}

func (a *Addresser) systemPrompt(req floor.AddressRequest, opts []string) string {
	var b strings.Builder
	b.WriteString(a.prompt)
	b.WriteString("\n\nParticipants:\n- @user: the human\n")
	for _, ag := range req.Agents {
		b.WriteString("- " + ag.ID)
		if ag.Name != "" {
			b.WriteString(" (" + ag.Name + ")")
		}
		b.WriteString("\n")
		if ag.TurnPrompt != "" {
			b.WriteString("  When " + ag.ID + " speaks: " + strings.TrimSpace(ag.TurnPrompt) + "\n")
		}
	}
	for _, ag := range req.Agents {
		if ag.ID == req.DefaultAgent {
			b.WriteString("\nWhen a message of @user addresses nobody, " + ag.ID + " answers it; nobody is then the right answer.\n")
		}
	}
	b.WriteString("\nAnswer with exactly one of these on the first line:\n")
	for _, o := range opts {
		b.WriteString(o + "\n")
	}
	b.WriteString("On the second line, give a short reason.")
	return b.String()
}

// conversation is the user message: the latest messages with their
// speakers, without tool output, and who is waiting for an answer.
func (a *Addresser) conversation(req floor.AddressRequest) string {
	var msgs []floor.ChatMessage
	for _, m := range req.History {
		if m.From != "@system" {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) > a.history {
		msgs = msgs[len(msgs)-a.history:]
	}
	var b strings.Builder
	b.WriteString("The latest messages, oldest first:\n\n")
	for _, m := range msgs {
		b.WriteString(m.From + ": " + strings.TrimSpace(m.Content) + "\n\n")
	}
	if n := len(req.CallStack); n > 0 {
		f := req.CallStack[n-1]
		b.WriteString(fmt.Sprintf("Waiting for an answer: %s asked %s.\n\n", f.Caller, f.Callee))
	}
	b.WriteString("Who does the last message address?")
	return b.String()
}

// parse reads an answer whose first line is one of opts, ignoring case,
// surrounding markdown emphasis or quotes, and a trailing period; the
// rest is the reason.
func parse(answer string, opts []string) (floor.Address, bool) {
	first := strings.Trim(firstLine(answer), " *`\"'.")
	for _, o := range opts {
		if !strings.EqualFold(first, o) {
			continue
		}
		reason := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(answer), firstLine(answer)))
		kind, agent, _ := strings.Cut(o, " ")
		return floor.Address{Kind: floor.AddressKind(kind), AgentID: agent, Reason: strings.Join(strings.Fields(reason), " ")}, true
	}
	return floor.Address{}, false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
