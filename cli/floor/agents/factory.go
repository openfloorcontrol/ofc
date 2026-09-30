package agents

import (
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
)

// New builds the Agent for a spec by its type. Assign it to
// Floor.AgentFactory.
func New(spec *blueprint.Agent) floor.Agent {
	if spec.Type == "acp" {
		return NewACP(spec)
	}
	return NewLLM(spec)
}
