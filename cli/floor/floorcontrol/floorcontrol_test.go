package floorcontrol

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/llm/llmtest"
)

// shop is a request after the user asked something, with @hiro and @maya
// on the floor.
func shop(history ...string) floor.AddressRequest {
	req := floor.AddressRequest{Agents: []blueprint.Agent{
		{ID: "@hiro", Name: "Uncle Hiro"},
		{ID: "@maya", Name: "The Pixel Peeper", TurnPrompt: "Stays out while Hiro talks to the customer."},
	}}
	for _, h := range history {
		from, content, _ := strings.Cut(h, ": ")
		req.History = append(req.History, floor.ChatMessage{From: from, Content: content})
	}
	return req
}

func addresser(t *testing.T, fc blueprint.FloorControl, script ...llmtest.Response) (*Addresser, *llmtest.Mock) {
	t.Helper()
	mock := llmtest.New().Script(script...)
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	fc.Endpoint, fc.Model = srv.URL+"/v1", "router"
	return New(fc), mock
}

func TestDecideReadsTheAnswer(t *testing.T) {
	a, _ := addresser(t, blueprint.FloorControl{},
		llmtest.Response{Content: "**call @maya**\nThe customer asks Maya about the sensor."})
	res := a.Decide(shop("@user: Maya, what about the sensor?"))
	if !res.Valid || res.Address.Kind != floor.AddressCall || res.Address.AgentID != "@maya" {
		t.Fatalf("got %+v", res)
	}
	if res.Address.Reason != "The customer asks Maya about the sensor." {
		t.Errorf("reason = %q", res.Address.Reason)
	}
}

func TestDecideAsksAgainOnAnInvalidAnswer(t *testing.T) {
	a, mock := addresser(t, blueprint.FloorControl{Retries: 1},
		llmtest.Response{Content: "I think Maya should answer that."},
		llmtest.Response{Content: "chime @maya\nShe knows sensors."})
	res := a.Decide(shop("@user: What about the sensor?"))
	if !res.Valid || res.Address.Kind != floor.AddressChime || len(res.Answers) != 2 {
		t.Fatalf("got %+v", res)
	}
	followUp := mock.Requests()[1].Messages
	if last := followUp[len(followUp)-1].Content; !strings.Contains(last, "exactly one of") || !strings.Contains(last, "chime @maya") {
		t.Errorf("follow-up = %q", last)
	}
}

// The model learns who answers a user message that addresses nobody, as
// long as that agent may speak.
func TestPromptNamesTheDefaultAgent(t *testing.T) {
	a, mock := addresser(t, blueprint.FloorControl{},
		llmtest.Response{Content: "nobody"}, llmtest.Response{Content: "nobody"})
	req := shop("@user: *pats the cat*")
	req.DefaultAgent = "@hiro"
	a.Decide(req)
	req.Agents = req.Agents[1:] // @hiro passed
	a.Decide(req)

	if s := mock.Requests()[0].Messages[0].Content; !strings.Contains(s, "addresses nobody, @hiro answers it") {
		t.Errorf("system prompt lacks the default agent:\n%s", s)
	}
	if s := mock.Requests()[1].Messages[0].Content; strings.Contains(s, "addresses nobody,") {
		t.Errorf("system prompt names a default agent that passed:\n%s", s)
	}
}

func TestDecideGivesUpOnInvalidAnswers(t *testing.T) {
	a, _ := addresser(t, blueprint.FloorControl{},
		llmtest.Response{Content: "Maya, probably."})
	res := a.Decide(shop("@user: What about the sensor?"))
	if res.Valid || res.Address.Kind != floor.AddressNobody || !strings.Contains(res.Address.Reason, "invalid answer") {
		t.Fatalf("got %+v", res)
	}
}

func TestDecideWithoutAModel(t *testing.T) {
	a := New(blueprint.FloorControl{Endpoint: "http://127.0.0.1:1/v1", Model: "router"})
	res := a.Decide(shop("@user: hi"))
	if res.Err == nil || res.Address.Kind != floor.AddressNobody {
		t.Fatalf("got %+v", res)
	}
}

// The model sees the general prompt, each agent's turn prompt, only the
// latest messages, who is waiting, and exactly the valid options.
func TestPromptContents(t *testing.T) {
	a, mock := addresser(t, blueprint.FloorControl{History: 2},
		llmtest.Response{Content: "nobody\nHiro answered."})
	req := shop(
		"@user: first message",
		"@user: Which Nikon do you have?",
		"@hiro: Just the D750.")
	req.CallStack = []floor.Frame{{Caller: "@user", Callee: "@hiro"}}
	a.Decide(req)

	msgs := mock.Requests()[0].Messages
	system, user := msgs[0].Content, msgs[1].Content
	for _, want := range []string{"You run the floor", "@maya (The Pixel Peeper)",
		"When @maya speaks: Stays out while Hiro talks", "call @user\n", "call @maya\n", "chime @maya\n", "nobody"} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt lacks %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "call @hiro") {
		t.Errorf("the last speaker is offered:\n%s", system)
	}
	if strings.Contains(system, "addresses nobody,") {
		t.Errorf("mentions a default agent without one:\n%s", system)
	}
	if strings.Contains(user, "first message") || !strings.Contains(user, "@hiro: Just the D750.") ||
		!strings.Contains(user, "Waiting for an answer: @user asked @hiro.") {
		t.Errorf("conversation:\n%s", user)
	}
}
