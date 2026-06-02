package floor

import "github.com/openfloorcontrol/ofc/blueprint"

// ANSI color codes used by frontends (CLI, TUI) for rendering.
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

// BuildColorMap assigns colors to agents, cycling through the palette.
func BuildColorMap(bp *blueprint.Blueprint) map[string]string {
	cm := map[string]string{"@user": Cyan}
	for i, a := range bp.Agents {
		cm[a.ID] = agentColors[i%len(agentColors)]
	}
	return cm
}
