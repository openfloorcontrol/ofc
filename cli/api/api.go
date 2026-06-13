// Package api implements the HTTP API server that exposes a floor's
// chat, furniture, and MCP endpoints. The concrete Server type
// satisfies the floor.APIServer interface; callers (cmd/, tests)
// construct api.New() and assign it to Floor.APIServer before
// Floor.Start.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/furniture"
)

// Server serves MCP endpoints for furniture over HTTP. Implements
// floor.APIServer.
type Server struct {
	echo      *echo.Echo
	listener  net.Listener
	authToken string
}

// New creates a new API server.
func New() *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// CORS middleware — needed for Vite dev server (port 5173 → 8080)
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	return &Server{echo: e}
}

// SetAuthToken sets the token and installs auth middleware.
// Must be called before Start(). If token is empty, no auth is enforced.
func (s *Server) SetAuthToken(token string) {
	s.authToken = token
	if token != "" {
		s.echo.Use(authMiddleware(token))
	}
}

// AuthToken returns the current auth token.
func (s *Server) AuthToken() string {
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
// Skips non-API routes (static files serve without auth — not sensitive).
func authMiddleware(token string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path

			// Skip non-API routes (static files)
			if !strings.HasPrefix(path, "/api/") {
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
func (s *Server) RegisterFurniture(floorID, name string, mcpSrv *mcp.Server) {
	getServer := func(r *http.Request) *mcp.Server { return mcpSrv }

	// Streamable HTTP endpoint
	httpPath := fmt.Sprintf("/api/v1/floors/%s/mcp/%s", floorID, name)
	httpHandler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	s.echo.Any(httpPath, echo.WrapHandler(httpHandler))
	s.echo.Any(httpPath+"/", echo.WrapHandler(httpHandler))

	// SSE endpoint (for ACP agents like claude-code-acp that only support SSE)
	ssePath := fmt.Sprintf("/api/v1/floors/%s/sse/%s", floorID, name)
	sseHandler := mcp.NewSSEHandler(getServer, nil)
	s.echo.Any(ssePath, echo.WrapHandler(sseHandler))
	s.echo.Any(ssePath+"/", echo.WrapHandler(sseHandler))
}

// ServeStaticWeb serves the web/dist/ directory as static files with SPA fallback.
func (s *Server) ServeStaticWeb(webFS fs.FS) {
	indexHTML, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		indexHTML = []byte("<html><body>index.html not found</body></html>")
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
func (s *Server) Start(addr string) error {
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
func (s *Server) Stop() error {
	if s.echo != nil {
		return s.echo.Shutdown(context.Background())
	}
	return nil
}

// BaseURL returns the base URL of the running server (e.g. "http://localhost:12345").
func (s *Server) BaseURL() string {
	if s.listener == nil {
		return ""
	}
	return fmt.Sprintf("http://%s", s.listener.Addr().String())
}

// RegisterFloorAPI adds message and furniture endpoints for the floor.
// workspacePath is a func so it can be resolved lazily (sandbox may start after registration).
func (s *Server) RegisterFloorAPI(chat *floor.Room, bp *blueprint.Blueprint, furnitureMap map[string]furniture.Furniture, workspacePath func() string) {
	s.echo.POST("/api/v1/messages", handlePostMessage(chat))
	s.echo.GET("/api/v1/messages", handleGetMessages(chat))
	s.echo.GET("/api/v1/events", handleSSEEvents(chat))
	s.echo.GET("/api/v1/agents", handleGetAgents(bp))
	s.echo.GET("/api/v1/furniture", handleGetFurniture(furnitureMap))
	s.echo.POST("/api/v1/furniture/:name/call", handleFurnitureCall(furnitureMap))
	s.echo.GET("/api/v1/file/*", handleServeFile(furnitureMap, workspacePath))
}

// POST /api/v1/messages — inject a message into the floor chat.
// If from is empty or "@user", routes through PostUserInput (handles slash commands).
// Otherwise posts as the specified sender (for external agents/webhooks).
func handlePostMessage(chat *floor.Room) echo.HandlerFunc {
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
			chat.Post(floor.ChatMessage{From: from, Content: req.Content})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": map[string]string{"from": from, "content": req.Content},
		})
	}
}

// GET /api/v1/messages — return chat history as JSON.
func handleGetMessages(chat *floor.Room) echo.HandlerFunc {
	type jsonMessage struct {
		From             string                  `json:"from"`
		Content          string                  `json:"content"`
		ToolInteractions []floor.ToolInteraction `json:"tool_interactions,omitempty"`
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
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		Type       string   `json:"type"`
		Activation string   `json:"activation"`
		Model      string   `json:"model,omitempty"`
		Command    string   `json:"command,omitempty"`
		Furniture  []string `json:"furniture,omitempty"`
		Prompt     string   `json:"prompt_summary,omitempty"` // first line only
		Sandbox    bool     `json:"sandbox,omitempty"`
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
			// Extract first non-empty line of prompt as summary
			promptSummary := ""
			if a.Prompt != "" {
				for _, line := range strings.Split(a.Prompt, "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						if len(line) > 120 {
							line = line[:120] + "..."
						}
						promptSummary = line
						break
					}
				}
			}
			agents[i] = jsonAgent{
				ID:         a.ID,
				Name:       name,
				Type:       typ,
				Activation: activation,
				Model:      a.Model,
				Command:    a.Command,
				Furniture:  a.Furniture,
				Prompt:     promptSummary,
				Sandbox:    a.CanUseSandbox,
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

// GET /api/v1/file/* — unified file serving endpoint.
// Path scheme:
//   - ":furniture/path" → serve via furniture's FileReader (MCP)
//   - "path"            → serve from workspace (sandbox dir or ./workspace)
func handleServeFile(furnitureMap map[string]furniture.Furniture, workspacePath func() string) echo.HandlerFunc {
	return func(c echo.Context) error {
		rawPath := c.Param("*")
		if rawPath == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "file path is required"})
		}

		// Furniture-qualified: ":name/path"
		if strings.HasPrefix(rawPath, ":") {
			slashIdx := strings.Index(rawPath, "/")
			if slashIdx < 0 {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing path after furniture name"})
			}
			furName := rawPath[1:slashIdx]
			filePath := rawPath[slashIdx+1:]

			fur, ok := furnitureMap[furName]
			if !ok {
				return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("furniture %q not found", furName)})
			}
			fr, ok := fur.(furniture.FileReader)
			if !ok {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("furniture %q does not support file reading", furName)})
			}

			data, mimeType, err := fr.ReadFileRaw(filePath)
			if err != nil {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			if mimeType == "" || mimeType == "application/octet-stream" || mimeType == "text/plain" {
				if detected := mime.TypeByExtension(filepath.Ext(filePath)); detected != "" {
					mimeType = detected
				}
			}
			c.Response().Header().Set("Cache-Control", "no-cache")
			return c.Blob(http.StatusOK, mimeType, data)
		}

		// Bare path: serve from workspace
		wsDirs := []string{}
		if ws := workspacePath(); ws != "" {
			wsDirs = append(wsDirs, ws)
		}
		wsDirs = append(wsDirs, "workspace") // convention fallback

		for _, wsDir := range wsDirs {
			absWS, err := filepath.Abs(wsDir)
			if err != nil {
				continue
			}
			absFile, err := filepath.Abs(filepath.Join(wsDir, rawPath))
			if err != nil {
				continue
			}
			// Path traversal guard
			if !strings.HasPrefix(absFile, absWS+string(os.PathSeparator)) && absFile != absWS {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "path outside workspace"})
			}
			data, err := os.ReadFile(absFile)
			if err != nil {
				continue
			}
			mimeType := mime.TypeByExtension(filepath.Ext(rawPath))
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			c.Response().Header().Set("Cache-Control", "no-cache")
			return c.Blob(http.StatusOK, mimeType, data)
		}

		return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
	}
}

// GET /api/v1/events — SSE stream of chat events.
func handleSSEEvents(chat *floor.Room) echo.HandlerFunc {
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
				payload := floor.EventJSON(ev)
				if payload == nil {
					continue
				}
				data, _ := json.Marshal(payload)
				fmt.Fprintf(c.Response(), "data: %s\n\n", data)
				c.Response().Flush()
			}
		}
	}
}
