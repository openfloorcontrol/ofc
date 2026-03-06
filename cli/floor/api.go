package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openfloorcontrol/ofc/blueprint"
)

// APIServer serves MCP endpoints for furniture over HTTP.
type APIServer struct {
	echo     *echo.Echo
	listener net.Listener
}

// NewAPIServer creates a new API server.
func NewAPIServer() *APIServer {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// CORS middleware — needed for Vite dev server (port 5173 → 8080)
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAccept},
	}))

	return &APIServer{echo: e}
}

// RegisterFurniture adds MCP endpoints for a piece of furniture.
// Registers both Streamable HTTP and SSE transports:
//   - /api/v1/floors/{floor}/mcp/{name}/ — Streamable HTTP
//   - /api/v1/floors/{floor}/sse/{name}/ — SSE (legacy, used by claude-code-acp)
func (s *APIServer) RegisterFurniture(floor, name string, mcpSrv *mcp.Server) {
	getServer := func(r *http.Request) *mcp.Server { return mcpSrv }

	// Streamable HTTP endpoint
	httpPath := fmt.Sprintf("/api/v1/floors/%s/mcp/%s", floor, name)
	httpHandler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	s.echo.Any(httpPath, echo.WrapHandler(httpHandler))
	s.echo.Any(httpPath+"/", echo.WrapHandler(httpHandler))

	// SSE endpoint (for ACP agents like claude-code-acp that only support SSE)
	ssePath := fmt.Sprintf("/api/v1/floors/%s/sse/%s", floor, name)
	sseHandler := mcp.NewSSEHandler(getServer, nil)
	s.echo.Any(ssePath, echo.WrapHandler(sseHandler))
	s.echo.Any(ssePath+"/", echo.WrapHandler(sseHandler))
}

// ServeStaticWeb serves the web/dist/ directory as static files with SPA fallback.
func (s *APIServer) ServeStaticWeb(webFS fs.FS) {
	fileServer := http.FileServer(http.FS(webFS))

	// Catch-all: serve static files, fall back to index.html for SPA routes
	s.echo.GET("/*", echo.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't serve static files for API routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try serving the file directly
		// Use a response recorder to check if file exists
		rr := &statusRecorder{ResponseWriter: w}
		fileServer.ServeHTTP(rr, r)

		// If file not found, serve index.html (SPA fallback)
		if rr.status == http.StatusNotFound {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
		}
	})))
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	if code != http.StatusNotFound {
		r.ResponseWriter.WriteHeader(code)
	}
	r.wrote = true
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	if r.status == http.StatusNotFound {
		return len(b), nil // discard 404 body
	}
	return r.ResponseWriter.Write(b)
}

// Start begins listening in a background goroutine on the given address.
// Pass ":0" for auto-assigned port.
func (s *APIServer) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = ln
	s.echo.Listener = ln
	go s.echo.Start("")
	return nil
}

// Stop shuts down the server.
func (s *APIServer) Stop() error {
	if s.echo != nil {
		return s.echo.Shutdown(context.Background())
	}
	return nil
}

// BaseURL returns the base URL of the running server (e.g. "http://localhost:12345").
func (s *APIServer) BaseURL() string {
	if s.listener == nil {
		return ""
	}
	return fmt.Sprintf("http://%s", s.listener.Addr().String())
}

// RegisterFloorAPI adds message endpoints for posting to and reading from the floor.
func (s *APIServer) RegisterFloorAPI(chat *Chat, bp *blueprint.Blueprint) {
	s.echo.POST("/api/v1/messages", handlePostMessage(chat))
	s.echo.GET("/api/v1/messages", handleGetMessages(chat))
	s.echo.GET("/api/v1/events", handleSSEEvents(chat))
	s.echo.GET("/api/v1/agents", handleGetAgents(bp))
}

// POST /api/v1/messages — inject a message into the floor chat.
// If from is empty or "@user", routes through PostUserInput (handles slash commands).
// Otherwise posts as the specified sender (for external agents/webhooks).
func handlePostMessage(chat *Chat) echo.HandlerFunc {
	type request struct {
		From    string `json:"from"`
		Content string `json:"content"`
	}
	return func(c echo.Context) error {
		var req request
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		}
		if req.Content == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "content is required"})
		}

		from := req.From
		if from == "" || from == "@user" {
			from = "@user"
			chat.PostUserInput(req.Content)
		} else {
			chat.Post(ChatMessage{From: from, Content: req.Content})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": map[string]string{"from": from, "content": req.Content},
		})
	}
}

// GET /api/v1/messages — return chat history as JSON.
func handleGetMessages(chat *Chat) echo.HandlerFunc {
	type jsonMessage struct {
		From             string            `json:"from"`
		Content          string            `json:"content"`
		ToolInteractions []ToolInteraction `json:"tool_interactions,omitempty"`
	}
	return func(c echo.Context) error {
		history := chat.History()
		msgs := make([]jsonMessage, len(history))
		for i, m := range history {
			msgs[i] = jsonMessage{
				From:             m.From,
				Content:          m.Content,
				ToolInteractions: m.ToolInteractions,
			}
		}
		return c.JSON(http.StatusOK, map[string]interface{}{"messages": msgs})
	}
}

// GET /api/v1/agents — return floor metadata and agent list.
func handleGetAgents(bp *blueprint.Blueprint) echo.HandlerFunc {
	type jsonAgent struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Activation string `json:"activation"`
	}
	return func(c echo.Context) error {
		agents := make([]jsonAgent, len(bp.Agents))
		for i, a := range bp.Agents {
			typ := a.Type
			if typ == "" {
				typ = "llm"
			}
			name := a.Name
			if name == "" {
				name = a.ID
			}
			activation := a.Activation
			if activation == "" {
				activation = "always"
			}
			agents[i] = jsonAgent{
				ID:         a.ID,
				Name:       name,
				Type:       typ,
				Activation: activation,
			}
		}
		return c.JSON(http.StatusOK, map[string]interface{}{
			"floor_name":  bp.Name,
			"description": bp.Description,
			"agents":      agents,
		})
	}
}

// GET /api/v1/events — SSE stream of chat events.
func handleSSEEvents(chat *Chat) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)
		c.Response().Flush()

		sub := chat.Subscribe()
		defer chat.Unsubscribe(sub)

		ctx := c.Request().Context()
		for {
			select {
			case <-ctx.Done():
				return nil
			case ev, ok := <-sub:
				if !ok {
					return nil
				}
				data := sseEventJSON(ev)
				if data == nil {
					continue
				}
				fmt.Fprintf(c.Response(), "data: %s\n\n", data)
				c.Response().Flush()
			}
		}
	}
}

// sseEventJSON converts a ChatEvent to a JSON byte slice for SSE.
func sseEventJSON(ev ChatEvent) []byte {
	var payload interface{}

	switch e := ev.(type) {
	case MessagePosted:
		payload = map[string]interface{}{
			"type": "message_posted",
			"message": map[string]interface{}{
				"from":              e.Message.From,
				"content":           e.Message.Content,
				"tool_interactions": e.Message.ToolInteractions,
			},
		}
	case StreamEvent:
		switch se := e.Event.(type) {
		case TokenStreamed:
			payload = map[string]interface{}{
				"type":     "token",
				"agent_id": se.AgentID,
				"token":    se.Token,
			}
		case ToolCallStarted:
			payload = map[string]interface{}{
				"type":     "tool_call_started",
				"agent_id": se.AgentID,
				"title":    se.Title,
			}
		case ToolCallResult:
			payload = map[string]interface{}{
				"type":     "tool_call_result",
				"agent_id": se.AgentID,
				"title":    se.Title,
				"output":   se.Output,
			}
		case AgentLabel:
			payload = map[string]interface{}{
				"type":     "agent_label",
				"agent_id": se.AgentID,
			}
		default:
			return nil
		}
	case AgentFinished:
		payload = map[string]interface{}{
			"type":     "agent_finished",
			"agent_id": e.AgentID,
		}
	case AgentPassedEvent:
		payload = map[string]interface{}{
			"type":     "agent_passed",
			"agent_id": e.AgentID,
		}
	case AgentErrorEvent:
		payload = map[string]interface{}{
			"type":     "agent_error",
			"agent_id": e.AgentID,
			"error":    e.Err.Error(),
		}
	default:
		return nil
	}

	data, _ := json.Marshal(payload)
	return data
}
