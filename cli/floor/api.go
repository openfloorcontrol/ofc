package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
func (s *APIServer) RegisterFloorAPI(chat *Chat) {
	s.echo.POST("/api/v1/messages", handlePostMessage(chat))
	s.echo.GET("/api/v1/messages", handleGetMessages(chat))
	s.echo.GET("/api/v1/events", handleSSEEvents(chat))
}

// POST /api/v1/messages — inject a message into the floor chat.
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
		if req.From == "" {
			req.From = "@user"
		}

		msg := ChatMessage{From: req.From, Content: req.Content}
		chat.Post(msg)

		return c.JSON(http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": map[string]string{"from": msg.From, "content": msg.Content},
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
				"from":    e.Message.From,
				"content": e.Message.Content,
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
