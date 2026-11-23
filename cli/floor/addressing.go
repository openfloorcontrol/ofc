package floor

import (
	"context"

	"github.com/openfloorcontrol/ofc/blueprint"
)

// AddressKind is what the last message of a room does to the floor.
type AddressKind string

const (
	// AddressCall: the message asks the agent (or the user) something.
	// The agent gets the floor, and when it is done the floor returns to
	// the one who asked.
	AddressCall AddressKind = "call"
	// AddressChime: nobody asked the agent, but it would react. It gets
	// the floor only when nobody is waiting for an answer.
	AddressChime AddressKind = "chime"
	// AddressNobody: the message addresses nobody new.
	AddressNobody AddressKind = "nobody"
)

// Address is whom the last message of a room addresses.
type Address struct {
	Kind    AddressKind
	AgentID string // the called or chiming agent; "@user" for a call to the user
	Reason  string // why, for debug output
}

// AddressRequest is what an Addresser decides on.
type AddressRequest struct {
	// Seq identifies the decision; Controller.Next takes only the
	// address for the latest one.
	Seq uint64
	// History is the room's messages, oldest first.
	History []ChatMessage
	// Agents are the agents that may get the floor, in blueprint order.
	Agents []blueprint.Agent
	// CallStack is who asked whom, innermost last.
	CallStack []Frame
}

// Addresser decides whom the last message of a room addresses.
type Addresser interface {
	Address(ctx context.Context, req AddressRequest) Address
}

// MentionAddresser addresses by explicit mention and activation: "@x?"
// calls agent x, "@user?" calls the user, and otherwise the first agent
// with activation "always" chimes in.
type MentionAddresser struct{}

func (MentionAddresser) Address(_ context.Context, req AddressRequest) Address {
	if len(req.History) == 0 {
		return Address{Kind: AddressNobody}
	}
	last := req.History[len(req.History)-1]
	mentions := extractMentions(last.Content)

	if last.From != "@user" {
		for _, m := range mentions {
			if m == "@user" {
				return Address{Kind: AddressCall, AgentID: "@user", Reason: "mentions @user?"}
			}
		}
	}
	for _, a := range req.Agents {
		for _, m := range mentions {
			if m == a.ID && m != last.From {
				return Address{Kind: AddressCall, AgentID: a.ID, Reason: "mentions " + a.ID + "?"}
			}
		}
	}
	for _, a := range req.Agents {
		if a.ID != last.From && a.Activation == "always" {
			return Address{Kind: AddressChime, AgentID: a.ID, Reason: "activation: always"}
		}
	}
	return Address{Kind: AddressNobody}
}
