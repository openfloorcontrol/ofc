package frontend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/openfloorcontrol/ofc/floor"
)

// RemoteSession is a SessionClient for a session on an ofc web server
// (`ofc run --web`), reached through its HTTP API.
type RemoteSession struct {
	base   string // e.g. https://host:port
	token  string
	id     string
	info   FloorInfo
	hist   []floor.ChatMessage
	events chan floor.TaggedEvent
	cancel context.CancelFunc
}

// NewRemoteSession connects to the server at base. With sessionID empty,
// it starts a new session there. token is sent as a Bearer token.
func NewRemoteSession(base, token, sessionID string) (*RemoteSession, error) {
	r := &RemoteSession{base: strings.TrimSuffix(base, "/"), token: token, id: sessionID}

	if r.id == "" {
		var created struct{ ID string }
		if err := r.do("POST", "/api/v1/sessions", nil, &created); err != nil {
			return nil, fmt.Errorf("create session: %w", err)
		}
		r.id = created.ID
	}
	if err := r.loadInfo(); err != nil {
		return nil, err
	}

	var history struct {
		Messages []struct {
			Seq     uint64 `json:"seq"`
			From    string `json:"from"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := r.do("GET", r.sessionPath("/messages"), nil, &history); err != nil {
		return nil, fmt.Errorf("load session %s: %w", r.id, err)
	}
	var lastSeq uint64
	for _, m := range history.Messages {
		r.hist = append(r.hist, floor.ChatMessage{From: m.From, Content: m.Content})
		lastSeq = m.Seq
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	resp, err := r.request(ctx, "GET", r.sessionPath(fmt.Sprintf("/events?last_event_id=%d", lastSeq)), nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open event stream: %w", err)
	}
	r.events = make(chan floor.TaggedEvent, 64)
	go r.readEvents(ctx, resp.Body)
	return r, nil
}

func (r *RemoteSession) ID() string                       { return r.id }
func (r *RemoteSession) Info() FloorInfo                  { return r.info }
func (r *RemoteSession) History() []floor.ChatMessage     { return r.hist }
func (r *RemoteSession) Events() <-chan floor.TaggedEvent { return r.events }
func (r *RemoteSession) Close()                           { r.cancel() }

func (r *RemoteSession) PostUserInput(text string) error {
	body, _ := json.Marshal(map[string]string{"content": text})
	return r.do("POST", r.sessionPath("/messages"), body, nil)
}

func (r *RemoteSession) loadInfo() error {
	var agents struct {
		FloorName   string `json:"floor_name"`
		Description string `json:"description"`
		Agents      []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := r.do("GET", "/api/v1/agents", nil, &agents); err != nil {
		return fmt.Errorf("load floor info: %w", err)
	}
	var furniture struct {
		Furniture []struct {
			Name string `json:"name"`
		} `json:"furniture"`
	}
	if err := r.do("GET", "/api/v1/furniture", nil, &furniture); err != nil {
		return fmt.Errorf("load furniture: %w", err)
	}
	r.info = FloorInfo{Name: agents.FloorName, Description: agents.Description}
	for _, a := range agents.Agents {
		r.info.Agents = append(r.info.Agents, a.ID)
	}
	for _, f := range furniture.Furniture {
		r.info.Furniture = append(r.info.Furniture, f.Name)
	}
	return nil
}

// readEvents decodes the SSE stream into Events until it ends or Close.
func (r *RemoteSession) readEvents(ctx context.Context, body io.ReadCloser) {
	defer body.Close()
	defer close(r.events)
	lines := bufio.NewScanner(body)
	lines.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lines.Scan() {
		data, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue
		}
		ev, err := floor.ParseEventJSON([]byte(data))
		if err != nil {
			continue // an event type this client doesn't know
		}
		select {
		case r.events <- ev:
		case <-ctx.Done():
			return
		}
	}
}

func (r *RemoteSession) sessionPath(suffix string) string {
	return "/api/v1/sessions/" + url.PathEscape(r.id) + suffix
}

// do sends a request and decodes a JSON response into out (if non-nil).
func (r *RemoteSession) do(method, path string, body []byte, out any) error {
	resp, err := r.request(context.Background(), method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// request sends a request with the Bearer token and fails on non-2xx.
func (r *RemoteSession) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}
