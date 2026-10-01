// Command fakeagent is a deterministic ACP agent for tests. It answers
// every prompt with one line:
//
//	pid=<process id> prompts=<prompts in this ACP session> | <prompt text>
//
// where <prompt text> is the prompt's text blocks joined with " / ". Tests
// use pid to tell subprocesses apart, prompts to see whether a session
// continued, and the text to see what context the agent received.
//
// A prompt containing "sleep=<ms>" delays the reply by that long, for
// tests of long-running turns.
//
// It supports session/resume: each ACP session's prompt count is kept in
// $FAKE_ACP_STATE_DIR/<session id>, so a new process can resume it.
// Without FAKE_ACP_STATE_DIR, sessions live only in memory and resume
// fails. It speaks ACP over stdin/stdout.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

var sleepRe = regexp.MustCompile(`sleep=(\d+)`)

type fakeAgent struct {
	conn     *acp.AgentSideConnection
	stateDir string

	mu      sync.Mutex
	prompts map[acp.SessionId]int
	created int
}

func (a *fakeAgent) Initialize(ctx context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			McpCapabilities:     acp.McpCapabilities{Http: true, Sse: true},
			SessionCapabilities: acp.SessionCapabilities{Resume: &acp.SessionResumeCapabilities{}},
		},
	}, nil
}

func (a *fakeAgent) NewSession(ctx context.Context, _ acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.created++
	id := acp.SessionId(fmt.Sprintf("fake-%d-%d", os.Getpid(), a.created))
	a.prompts[id] = 0
	if err := a.save(id); err != nil {
		return acp.NewSessionResponse{}, err
	}
	return acp.NewSessionResponse{SessionId: id}, nil
}

func (a *fakeAgent) ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stateDir == "" {
		return acp.ResumeSessionResponse{}, fmt.Errorf("unknown session %s (no FAKE_ACP_STATE_DIR)", params.SessionId)
	}
	data, err := os.ReadFile(filepath.Join(a.stateDir, string(params.SessionId)))
	if err != nil {
		return acp.ResumeSessionResponse{}, fmt.Errorf("unknown session %s: %w", params.SessionId, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return acp.ResumeSessionResponse{}, fmt.Errorf("corrupt state for session %s: %w", params.SessionId, err)
	}
	a.prompts[params.SessionId] = n
	return acp.ResumeSessionResponse{}, nil
}

func (a *fakeAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	var texts []string
	for _, b := range params.Prompt {
		if b.Text != nil {
			texts = append(texts, b.Text.Text)
		}
	}

	a.mu.Lock()
	n, ok := a.prompts[params.SessionId]
	if !ok {
		a.mu.Unlock()
		return acp.PromptResponse{}, fmt.Errorf("unknown session %s", params.SessionId)
	}
	n++
	a.prompts[params.SessionId] = n
	err := a.save(params.SessionId)
	a.mu.Unlock()
	if err != nil {
		return acp.PromptResponse{}, err
	}

	text := strings.Join(texts, " / ")
	if m := sleepRe.FindStringSubmatch(text); m != nil {
		ms, _ := strconv.Atoi(m[1])
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}

	reply := fmt.Sprintf("pid=%d prompts=%d | %s", os.Getpid(), n, text)
	err = a.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: params.SessionId,
		Update:    acp.UpdateAgentMessageText(reply),
	})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// save writes the session's prompt count to the state dir, if any.
// Must be called with a.mu held.
func (a *fakeAgent) save(id acp.SessionId) error {
	if a.stateDir == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(a.stateDir, string(id)), []byte(strconv.Itoa(a.prompts[id])), 0o644)
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

func main() {
	ag := &fakeAgent{
		stateDir: os.Getenv("FAKE_ACP_STATE_DIR"),
		prompts:  make(map[acp.SessionId]int),
	}
	ag.conn = acp.NewAgentSideConnection(ag, os.Stdout, os.Stdin)
	<-ag.conn.Done()
}
