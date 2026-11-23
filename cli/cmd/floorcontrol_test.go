package cmd

import (
	"slices"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
)

var shopBlueprint = &blueprint.Blueprint{
	Agents: []blueprint.Agent{
		{ID: "@hiro", Activation: "always"},
		{ID: "@maya", Activation: "mention"},
	},
	FloorControl: blueprint.FloorControl{DefaultAgent: "@hiro"},
}

// Answers are compared by who gets the floor, under the floor's rules.
func TestCasesCompareWhoGetsTheFloor(t *testing.T) {
	for _, tc := range []struct {
		c    fcCase
		want []string
	}{
		{ // the caller gets the floor back either way
			fcCase{History: []string{"@user: What do you shoot?", "@hiro: What do you mostly shoot?"},
				Stack: []string{"@user -> @hiro"}, Want: answers{"call @user", "nobody"}},
			[]string{"@user"},
		},
		{ // a user message to nobody goes to the default agent
			fcCase{History: []string{"@user: *pats the cat*"}, Want: answers{"nobody", "chime @hiro"}},
			[]string{"@hiro"},
		},
		{ // unless it passed
			fcCase{History: []string{"@user: *pats the cat*"}, Passed: []string{"@hiro"}, Want: answers{"nobody"}},
			[]string{"@user"},
		},
		{ // Maya's answer goes back to Hiro, who asked her
			fcCase{History: []string{"@hiro: @maya? sensor?", "@maya: Still good."},
				Stack: []string{"@user -> @hiro", "@hiro -> @maya"}, Want: answers{"nobody", "chime @hiro"}},
			[]string{"@hiro"},
		},
	} {
		req, err := tc.c.request(shopBlueprint)
		if err != nil {
			t.Fatal(err)
		}
		got, err := tc.c.wantedFloor(shopBlueprint, req)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%v: floor = %v, %v; want %v", tc.c.History, got, err, tc.want)
		}
	}
}

func TestCasesRejectUnknownAnswers(t *testing.T) {
	for _, w := range []string{"call @mya", "chime", "nobody please", "ask @maya"} {
		c := fcCase{History: []string{"@user: hi"}, Want: answers{w}}
		req, _ := c.request(shopBlueprint)
		if _, err := c.wantedFloor(shopBlueprint, req); err == nil {
			t.Errorf("want %q accepted", w)
		}
	}
	c := fcCase{History: []string{"@user: hi"}, Want: answers{"nobody"}}
	req, err := c.request(shopBlueprint)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.floorAfter(shopBlueprint, req, floor.Address{Kind: floor.AddressCall, AgentID: "@maya"}); got != "@maya" {
		t.Errorf("call @maya gives the floor to %s", got)
	}
}
