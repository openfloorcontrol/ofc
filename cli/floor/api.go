package floor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/openfloorcontrol/ofc/furniture"
)

// APIServer serves MCP endpoints for furniture over HTTP.
type APIServer struct {
	echo      *echo.Echo
	listener  net.Listener
	authToken string
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
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	return &APIServer{echo: e}
}

// SetAuthToken sets the token and installs auth middleware.
// Must be called before Start(). If token is empty, no auth is enforced.
func (s *APIServer) SetAuthToken(token string) {
	s.authToken = token
	if token != "" {
		s.echo.Use(authMiddleware(token))
	}
}

// AuthToken returns the current auth token.
func (s *APIServer) AuthToken() string {
	return s.authToken
}

// GenerateToken creates a cryptographically random 32-byte hex token.
func GenerateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// authMiddleware checks for a valid auth token on /api/* routes.
// Accepts Authorization: Bearer <token> header or ?token=<token> query param.
// Skips non-API routes (static files) and the token endpoint itself.
func authMiddleware(token string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path

			// Skip non-API routes (static files)
			if !strings.HasPrefix(path, "/api/") {
				return next(c)
			}

			// Skip the token endpoint (has its own loopback check)
			if path == "/api/v1/auth/token" {
				return next(c)
			}

			// Check Authorization header
			auth := c.Request().Header.Get("Authorization")
			if auth == "Bearer "+token {
				return next(c)
			}

			// Check query parameter (needed for EventSource/SSE)
			if c.QueryParam("token") == token {
				return next(c)
			}

			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		}
	}
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
// If auth is enabled, injects the token into index.html.
func (s *APIServer) ServeStaticWeb(webFS fs.FS) {
	// Read index.html and optionally inject auth token
	indexHTML, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		indexHTML = []byte("<html><body>index.html not found</body></html>")
	}
	if s.authToken != "" {
		tokenScript := fmt.Sprintf(`<script>window.__OFC_TOKEN="%s"</script></head>`, s.authToken)
		indexHTML = []byte(strings.Replace(string(indexHTML), "</head>", tokenScript, 1))
	}

	fileServer := http.FileServer(http.FS(webFS))

	// Catch-all: serve static files, fall back to index.html for SPA routes
	s.echo.GET("/*", echo.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't serve static files for API routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Root path → serve (possibly token-injected) index.html
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexHTML)
			return
		}

		// Try serving the file directly
		rr := &statusRecorder{ResponseWriter: w}
		fileServer.ServeHTTP(rr, r)

		// If file not found, serve index.html (SPA fallback)
		if rr.status == http.StatusNotFound {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexHTML)
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

// RegisterFloorAPI adds message and furniture endpoints for the floor.
func (s *APIServer) RegisterFloorAPI(chat *Chat, bp *blueprint.Blueprint, furnitureMap map[string]furniture.Furniture) {
	s.echo.POST("/api/v1/messages", handlePostMessage(chat))
	s.echo.GET("/api/v1/messages", handleGetMessages(chat))
	s.echo.GET("/api/v1/events", handleSSEEvents(chat))
	s.echo.GET("/api/v1/agents", handleGetAgents(bp))
	s.echo.GET("/api/v1/furniture", handleGetFurniture(furnitureMap))
	s.echo.POST("/api/v1/furniture/:name/call", handleFurnitureCall(furnitureMap))
	s.echo.GET("/api/v1/auth/token", handleAuthToken(s))
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

// GET /api/v1/furniture — list available furniture with their tools.
func handleGetFurniture(furnitureMap map[string]furniture.Furniture) echo.HandlerFunc {
	type jsonTool struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Parameters  map[string]interface{} `json:"parameters,omitempty"`
	}
	type jsonFurniture struct {
		Name  string     `json:"name"`
		Tools []jsonTool `json:"tools"`
	}
	return func(c echo.Context) error {
		var items []jsonFurniture
		for _, fur := range furnitureMap {
			tools := fur.Tools()
			jtools := make([]jsonTool, len(tools))
			for i, t := range tools {
				jtools[i] = jsonTool{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				}
			}
			items = append(items, jsonFurniture{
				Name:  fur.Name(),
				Tools: jtools,
			})
		}
		if items == nil {
			items = []jsonFurniture{}
		}
		return c.JSON(http.StatusOK, map[string]interface{}{"furniture": items})
	}
}

// POST /api/v1/furniture/:name/call — proxy a tool call to a furniture instance.
func handleFurnitureCall(furnitureMap map[string]furniture.Furniture) echo.HandlerFunc {
	type request struct {
		Tool string                 `json:"tool"`
		Args map[string]interface{} `json:"args"`
	}
	return func(c echo.Context) error {
		name := c.Param("name")
		fur, ok := furnitureMap[name]
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("furniture %q not found", name)})
		}

		var req request
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		}
		if req.Tool == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "tool is required"})
		}
		if req.Args == nil {
			req.Args = make(map[string]interface{})
		}

		result, err := fur.Call(req.Tool, req.Args)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{"result": result})
	}
}

// GET /api/v1/auth/token — returns the auth token (loopback only, for dev mode).
func handleAuthToken(s *APIServer) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Only allow from loopback addresses
		ip := c.RealIP()
		if ip != "127.0.0.1" && ip != "::1" && ip != "localhost" {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "only available from localhost"})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{"token": s.authToken})
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
				"id":       se.ID,
				"title":    se.Title,
			}
		case ToolCallOutput:
			payload = map[string]interface{}{
				"type":     "tool_call_output",
				"agent_id": se.AgentID,
				"id":       se.ID,
				"output":   se.Output,
			}
		case ToolCallResult:
			payload = map[string]interface{}{
				"type":     "tool_call_result",
				"agent_id": se.AgentID,
				"id":       se.ID,
				"title":    se.Title,
				"output":   se.Output,
			}
		case AgentLabel:
			payload = map[string]interface{}{
				"type":     "agent_label",
				"agent_id": se.AgentID,
			}
		case FurnitureUpdated:
			payload = map[string]interface{}{
				"type": "furniture_updated",
				"name": se.Name,
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
