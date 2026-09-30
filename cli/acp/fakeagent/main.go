// Command fakeagent is a deterministic ACP agent for tests. It answers
// every prompt with one line:
//
//	pid=<process id> prompts=<prompts seen by this process> | <prompt text>
//
// where <prompt text> is the prompt's text blocks joined with " / ". Tests
// use pid to tell subprocesses apart and the text to see what context an
// agent received. It speaks ACP over stdin/stdout.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
)

type fakeAgent struct {
	conn *acp.AgentSideConnection

	mu      sync.Mutex
	prompts int
	nextID  int
}

func (a *fakeAgent) Initialize(ctx context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			McpCapabilities: acp.McpCapabilities{Http: true, Sse: true},
		},
	}, nil
}

func (a *fakeAgent) NewSession(ctx context.Context, _ acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	return acp.NewSessionResponse{SessionId: acp.SessionId(fmt.Sprintf("fake-%d", a.nextID))}, nil
}

func (a *fakeAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	var texts []string
	for _, b := range params.Prompt {
		if b.Text != nil {
			texts = append(texts, b.Text.Text)
		}
	}
	a.mu.Lock()
	a.prompts++
	n := a.prompts
	a.mu.Unlock()

	reply := fmt.Sprintf("pid=%d prompts=%d | %s", os.Getpid(), n, strings.Join(texts, " / "))
	err := a.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: params.SessionId,
		Update:    acp.UpdateAgentMessageText(reply),
	})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *fakeAgent) Authenticate(ctx context.Context, _ acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *fakeAgent) Cancel(ctx context.Context, _ acp.CancelNotification) error { return nil }

func (a *fakeAgent) SetSessionMode(ctx context.Context, _ acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (a *fakeAgent) SetSessionConfigOption(ctx context.Context, _ acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (a *fakeAgent) Logout(ctx context.Context, _ acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (a *fakeAgent) CloseSession(ctx context.Context, _ acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (a *fakeAgent) ListSessions(ctx context.Context, _ acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (a *fakeAgent) ResumeSession(ctx context.Context, _ acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, fmt.Errorf("resume not supported")
}

func main() {
	ag := &fakeAgent{}
	ag.conn = acp.NewAgentSideConnection(ag, os.Stdout, os.Stdin)
	<-ag.conn.Done()
}
