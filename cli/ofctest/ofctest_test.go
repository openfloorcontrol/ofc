package ofctest_test

import (
	"os"
	"testing"

	"github.com/openfloorcontrol/ofc/ofctest"
)

// TestTaskboardAddTask runs the taskboard example and checks that
// the planner agent creates a task on the board.
func TestTaskboardAddTask(t *testing.T) {
	if os.Getenv("OFC_INTEGRATION") == "" {
		t.Skip("set OFC_INTEGRATION=1 to run (requires LLM endpoint)")
	}

	result := ofctest.RunFloor(t, "../../examples/taskboard/blueprint.yaml",
		"Add a task: write documentation for the eval command")

	result.AssertNoErrors(t)
	result.AssertAgentSpoke(t, "@planner")
	result.AssertToolCalled(t, "tasks")

	// LLM-as-judge: did the planner actually create a task (not just list)?
	ev := result.Eval(t, "Did the agent create a new task on the task board (not just list existing tasks)?")
	ev.AssertMinScore(t, 3)
}
