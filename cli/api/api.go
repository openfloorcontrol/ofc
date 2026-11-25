// Package api implements the HTTP API server that exposes floors'
// sessions, furniture, and MCP endpoints. The concrete Server type
// satisfies the floor.APIServer interface; callers (cmd/, tests)
// construct api.New() and assign it to Floor.APIServer before
// Floor.Start, which adds the floor to it.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openfloorcontrol/ofc/floor"
	"github.com/openfloorcontrol/ofc/furniture"
)

// Server serves the API of its floors over HTTP: per floor, sessions,
// agents, furniture and the furniture's MCP endpoints, all under
// /api/v1/floors/{floor}/. Implements floor.APIServer.
type Server struct {
	echo      *echo.Echo
	listener  net.Listener
	authToken string

	// mu guards floors and mcp: floors and furniture come and go while
	// the server runs.
	mu     sync.RWMutex
	floors map[string]*floor.Floor
	// mcp holds the MCP endpoints of each floor's furniture, by floor ID
	// and furniture name.
	mcp map[string]map[string]mcpHandlers

	// building, if set, manages the floors (see SetBuilding).
	building Building
}

// mcpHandlers are the two MCP transports of one furniture.
type mcpHandlers struct {
	http, sse http.Handler
}

// Route patterns of the furniture MCP endpoints.
const (
	mcpRoute = "/api/v1/floors/:floor/mcp/:name"
	sseRoute = "/api/v1/floors/:floor/sse/:name"
)

// New creates a new API server.
func New() *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// CORS middleware — needed for Vite dev server (port 5173 → 8080)
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	s := &Server{echo: e, floors: map[string]*floor.Floor{}, mcp: map[string]map[string]mcpHandlers{}}
	e.Use(s.authMiddleware)
	s.registerFloorRoutes()
	s.registerBuildingRoutes()
	return s
}

// SetAuthToken sets the access token. Must be called before Start().
// An empty token leaves the API open, except furniture MCP endpoints.
func (s *Server) SetAuthToken(token string) {
	s.authToken = token
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

// authMiddleware checks the token of /api/* requests, given as
// Authorization: Bearer <token> or ?token=<token> (for EventSource).
//   - A furniture MCP endpoint needs the access token, or the token of
//     an agent that lists the furniture.
//   - Any other /api route needs the access token when one is set.
//
// Non-API routes (the web UI's static files) and /api/health need no token.
func (s *Server) authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		path := c.Request().URL.Path
		if !strings.HasPrefix(path, "/api/") || path == HealthPath {
			return next(c)
		}
		token := requestToken(c)
		isAccessToken := s.authToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.authToken)) == 1

		route := strings.TrimSuffix(c.Path(), "/")
		var allowed bool
		if route == mcpRoute || route == sseRoute {
			f, ok := s.floor(c.Param("floor"))
			allowed = isAccessToken || (token != "" && ok && f.AgentMayUse(token, c.Param("name")))
		} else {
			allowed = s.authToken == "" || isAccessToken
		}
		if !allowed {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		}
		return next(c)
	}
}

// requestToken returns the request's bearer token or ?token= parameter.
func requestToken(c echo.Context) string {
	if t, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer "); ok {
		return t
	}
	return c.QueryParam("token")
}

// RegisterFurniture serves a piece of furniture over MCP, in both
// transports:
//   - /api/v1/floors/{floor}/mcp/{name}/ — Streamable HTTP
//   - /api/v1/floors/{floor}/sse/{name}/ — SSE (legacy, used by claude-code-acp)
func (s *Server) RegisterFurniture(floorID, name string, mcpSrv *mcp.Server) {
	getServer := func(r *http.Request) *mcp.Server { return mcpSrv }
	h := mcpHandlers{
		http: mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true}),
		sse:  mcp.NewSSEHandler(getServer, nil),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcp[floorID] == nil {
		s.mcp[floorID] = map[string]mcpHandlers{}
	}
	s.mcp[floorID][name] = h
}

// serveMCP serves the MCP endpoint of the furniture named by the path,
// in the transport chosen by sse.
func (s *Server) serveMCP(sse bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		s.mu.RLock()
		h, ok := s.mcp[c.Param("floor")][c.Param("name")]
		s.mu.RUnlock()
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "furniture not found"})
		}
		if sse {
			h.sse.ServeHTTP(c.Response(), c.Request())
		} else {
			h.http.ServeHTTP(c.Response(), c.Request())
		}
		return nil
	}
}

// ServeStaticWeb serves the web UI as static files with SPA fallback.
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
	addr, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Sprintf("http://%s", s.listener.Addr().String())
	}
	// A server bound to all interfaces is reached locally via loopback;
	// the MCP handlers reject loopback requests with a non-localhost Host
	// (DNS rebinding protection), and "[::]" is not localhost.
	host := addr.IP.String()
	if addr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(addr.Port)))
}

// HealthPath reports liveness and versions. It needs no token, so a deploy
// script on the host can check which build is running.
const HealthPath = "/api/health"

// Health is the body of GET /api/health.
type Health struct {
	// Version is the deployment's version (--deploy-version), e.g. the
	// commit of the deployed blueprint; empty when not set.
	Version string `json:"version"`
	// OFC is the ofc binary's version.
	OFC string `json:"ofc"`
}

// RegisterHealth adds GET /api/health, answering with h.
func (s *Server) RegisterHealth(h Health) {
	s.echo.GET(HealthPath, func(c echo.Context) error {
		return c.JSON(http.StatusOK, h)
	})
}

// AddFloor serves f's API under /api/v1/floors/{f.ID()}/. Fails if the
// server already has a floor with that ID.
func (s *Server) AddFloor(f *floor.Floor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.floors[f.ID()]; ok {
		return fmt.Errorf("the API already serves a floor %q", f.ID())
	}
	s.floors[f.ID()] = f
	return nil
}

// RemoveFloor stops serving the floor with the given ID: its API and its
// furniture's MCP endpoints.
func (s *Server) RemoveFloor(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.floors, id)
	delete(s.mcp, id)
}

func (s *Server) floor(id string) (*floor.Floor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.floors[id]
	return f, ok
}

// registerFloorRoutes adds the floor list and each floor's session,
// agent, furniture, file and MCP endpoints. A request names its floor in
// the path.
func (s *Server) registerFloorRoutes() {
	const p = "/api/v1/floors/:floor"
	for _, route := range []string{mcpRoute, mcpRoute + "/"} {
		s.echo.Any(route, s.serveMCP(false))
	}
	for _, route := range []string{sseRoute, sseRoute + "/"} {
		s.echo.Any(route, s.serveMCP(true))
	}
	s.echo.GET("/api/v1/floors", s.handleListFloors)
	s.echo.GET(p+"/sessions", s.onFloor(handleListSessions))
	s.echo.POST(p+"/sessions", s.onFloor(handleCreateSession))
	s.echo.POST(p+"/sessions/:id/messages", s.onSession(handlePostMessage))
	s.echo.GET(p+"/sessions/:id/messages", s.onSession(handleGetMessages))
	s.echo.POST(p+"/sessions/:id/attachments", s.onSession(handlePostAttachment))
	s.echo.GET(p+"/sessions/:id/attachments/:aid", s.onSession(handleGetAttachment))
	s.echo.GET(p+"/sessions/:id/events", s.onSession(handleSSEEvents))
	s.echo.GET(p+"/agents", s.onFloor(handleGetAgents))
	s.echo.GET(p+"/furniture", s.onFloor(handleGetFurniture))
	s.echo.POST(p+"/furniture/:name/call", s.onFloor(handleFurnitureCall))
	s.echo.GET(p+"/file/*", s.onFloor(handleServeFile))
}

// onFloor resolves the :floor path parameter (see openFloor) and hands
// the floor to h. An unknown floor is not found.
func (s *Server) onFloor(h func(*floor.Floor) echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, err := s.openFloor(c.Param("floor"))
		if errors.Is(err, ErrFloorNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "floor not found"})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return h(f)(c)
	}
}

// onSession resolves :floor and the session :id on it (see withSession).
func (s *Server) onSession(h func(echo.Context, *floor.Session) error) echo.HandlerFunc {
	return s.onFloor(func(f *floor.Floor) echo.HandlerFunc { return withSession(f, h) })
}

// servedFloors describes the floors added with AddFloor, by ID.
func (s *Server) servedFloors() []FloorInfo {
	s.mu.RLock()
	out := make([]FloorInfo, 0, len(s.floors))
	for id, f := range s.floors {
		out = append(out, FloorInfo{ID: id, Name: f.Blueprint.Name, Description: f.Blueprint.Description, Running: true})
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UserHeader names the user the auth gateway (volume-auth via Caddy)
// authenticated. It is trusted because every /api route needs the access
// token, which only the gateway sends. Without it, a caller is nobody in
// particular and sees every session.
const UserHeader = "X-Volume-User"

func requestUser(c echo.Context) string {
	return c.Request().Header.Get(UserHeader)
}

// withSession resolves the :id path parameter to a running session
// (resuming it from the store if needed) and passes it to the handler.
// Another user's session is reported as not found.
func withSession(f *floor.Floor, h func(echo.Context, *floor.Session) error) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		ok, err := f.MayOpen(id, requestUser(c))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		}
		sess, err := f.Session(id)
		if errors.Is(err, floor.ErrSessionNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		}
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return h(c, sess)
	}
}

// GET /api/v1/floors/:floor/sessions — the floor's stored sessions the caller may open,
// most recent first. "user" is the caller's user, omitted when unknown.
func handleListSessions(f *floor.Floor) echo.HandlerFunc {
	type jsonSession struct {
		ID           string     `json:"id"`
		User         string     `json:"user,omitempty"`
		CreatedAt    time.Time  `json:"created_at"`
		LastActivity *time.Time `json:"last_activity,omitempty"`
		EventCount   int        `json:"event_count"`
	}
	type jsonList struct {
		User     string        `json:"user,omitempty"`
		Sessions []jsonSession `json:"sessions"`
	}
	return func(c echo.Context) error {
		user := requestUser(c)
		infos, err := f.ListSessions(user)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		out := make([]jsonSession, len(infos))
		for i, info := range infos {
			out[i] = jsonSession{ID: info.ID, User: info.Meta.User, CreatedAt: info.Meta.CreatedAt, EventCount: info.EventCount}
			if !info.LastActivity.IsZero() {
				last := info.LastActivity
				out[i].LastActivity = &last
			}
		}
		return c.JSON(http.StatusOK, jsonList{User: user, Sessions: out})
	}
}

// POST /api/v1/floors/:floor/sessions — start a new session, owned by the caller's user.
func handleCreateSession(f *floor.Floor) echo.HandlerFunc {
	return func(c echo.Context) error {
		sess, err := f.CreateSession(requestUser(c))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"id": sess.ID()})
	}
}

// POST /api/v1/floors/:floor/sessions/:id/messages — inject a message into the session.
// If from is empty or "@user", routes through PostUserInput (handles slash commands).
// Otherwise posts as the specified sender (for external agents/webhooks).
//
// attachments refers to images uploaded with POST .../attachments, by id;
// name is the display name. Only @user messages take attachments.
func handlePostMessage(c echo.Context, sess *floor.Session) error {
	var req struct {
		From        string `json:"from"`
		Content     string `json:"content"`
		Attachments []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"attachments"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
	}
	if req.Content == "" && len(req.Attachments) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "content is required"})
	}

	from := req.From
	if from == "" || from == "@user" {
		from = "@user"
		in := floor.UserInput{Text: req.Content}
		for _, ref := range req.Attachments {
			att, err := sess.LookupAttachment(ref.ID, ref.Name)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			in.Attachments = append(in.Attachments, att)
		}
		if err := sess.MainRoom.PostUserInput(in); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
	} else {
		if len(req.Attachments) > 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "only @user messages take attachments"})
		}
		sess.MainRoom.Post(floor.ChatMessage{From: from, Content: req.Content})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": map[string]string{"from": from, "content": req.Content},
	})
}

// POST /api/v1/floors/:floor/sessions/:id/attachments?name=<file name> — upload an
// image; the body is the image's bytes. Answers with the Attachment,
// whose id a message then refers to.
func handlePostAttachment(c echo.Context, sess *floor.Session) error {
	data, err := io.ReadAll(io.LimitReader(c.Request().Body, floor.MaxAttachmentSize+1))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	name := c.QueryParam("name")
	if name == "" {
		name = "image"
	}
	att, err := sess.AttachImage(name, data)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, att)
}

// GET /api/v1/floors/:floor/sessions/:id/attachments/:aid — an uploaded image's bytes.
// The id is the content's hash, so the response never changes.
func handleGetAttachment(c echo.Context, sess *floor.Session) error {
	data, err := sess.AttachmentData(c.Param("aid"))
	if errors.Is(err, floor.ErrBlobNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "attachment not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	c.Response().Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	return c.Blob(http.StatusOK, http.DetectContentType(data), data)
}

// GET /api/v1/floors/:floor/sessions/:id/messages — the session's #main history as JSON.
// Each message carries its seq; open the event stream with
// ?last_event_id=<last seq> to continue exactly after it.
func handleGetMessages(c echo.Context, sess *floor.Session) error {
	type jsonMessage struct {
		Seq              uint64                  `json:"seq"`
		From             string                  `json:"from"`
		Content          string                  `json:"content"`
		ToolInteractions []floor.ToolInteraction `json:"tool_interactions,omitempty"`
		Attachments      []floor.Attachment      `json:"attachments,omitempty"`
	}
	events, err := sess.Store().Read(sess.ID(), floor.EventFilter{RoomID: floor.MainRoomID})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	msgs := []jsonMessage{}
	for _, ev := range events {
		mp, ok := ev.Event.(floor.MessagePostedEvent)
		if !ok {
			continue
		}
		msgs = append(msgs, jsonMessage{
			Seq:              ev.Seq,
			From:             mp.Message.From,
			Content:          mp.Message.Content,
			ToolInteractions: mp.Message.ToolInteractions,
			Attachments:      mp.Message.Attachments,
		})
	}
	return c.JSON(http.StatusOK, map[string]interface{}{"messages": msgs})
}

// GET /api/v1/floors/:floor/agents — return floor metadata and agent list.
func handleGetAgents(f *floor.Floor) echo.HandlerFunc {
	bp := f.Blueprint
	type jsonAgent struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		Type       string   `json:"type"`
		Activation string   `json:"activation"`
		Model      string   `json:"model,omitempty"`
		Command    string   `json:"command,omitempty"`
		Furniture  []string `json:"furniture,omitempty"`
		Prompt     string   `json:"prompt_summary,omitempty"` // first line only
		Sandbox    string   `json:"sandbox,omitempty"`
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
				Sandbox:    a.Sandbox,
			}
		}
		return c.JSON(http.StatusOK, map[string]interface{}{
			"floor_name":  bp.Name,
			"description": bp.Description,
			"agents":      agents,
		})
	}
}

// GET /api/v1/floors/:floor/furniture — list available furniture with their tools.
func handleGetFurniture(f *floor.Floor) echo.HandlerFunc {
	furnitureMap := f.Furniture
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

// POST /api/v1/floors/:floor/furniture/:name/call — proxy a tool call to a furniture instance.
func handleFurnitureCall(f *floor.Floor) echo.HandlerFunc {
	furnitureMap := f.Furniture
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

// GET /api/v1/floors/:floor/file/* — unified file serving endpoint.
// Path scheme:
//   - ":furniture/path" → serve via furniture's FileReader (MCP)
//   - "path"            → serve from the first workspace that has it: the
//     sandboxes' workspaces, then the workspace beside the blueprint
func handleServeFile(f *floor.Floor) echo.HandlerFunc {
	furnitureMap, workspacePaths := f.Furniture, f.WorkspacePaths
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
		wsDirs := append(workspacePaths(), filepath.Join(f.Blueprint.Dir, "workspace"))

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

// GET /api/v1/floors/:floor/sessions/:id/events — SSE stream of the session's events
// (all rooms, plus the loop's lifecycle events). Sub-room events carry
// a room_id.
//
// Stored messages carry their seq as the SSE id. A client that sends
// Last-Event-ID (browsers do on reconnect) or ?last_event_id= first gets
// the stored messages after that seq, then the live stream. Streaming
// events (tokens, tool calls) are not stored and are not replayed.
func handleSSEEvents(c echo.Context, sess *floor.Session) error {
	cursorText := c.Request().Header.Get("Last-Event-ID")
	if cursorText == "" {
		cursorText = c.QueryParam("last_event_id")
	}
	var cursor uint64
	if cursorText != "" {
		n, err := strconv.ParseUint(cursorText, 10, 64)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "last event id must be a seq number"})
		}
		cursor = n
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	c.Response().Flush()

	// Subscribe before reading the store, so nothing posted in between
	// is missed; live messages already replayed are skipped by seq.
	sub := sess.Subscribe()
	defer sess.Unsubscribe(sub)

	if cursorText != "" {
		events, err := sess.Store().Read(sess.ID(), floor.EventFilter{FromSeq: cursor})
		if err != nil {
			return nil
		}
		for _, ev := range events {
			mp, ok := ev.Event.(floor.MessagePostedEvent)
			if !ok {
				continue
			}
			roomID := ev.RoomID
			if roomID == floor.MainRoomID {
				roomID = ""
			}
			writeSSE(c, roomID, floor.MessagePosted{Message: mp.Message, Seq: ev.Seq})
			cursor = ev.Seq
		}
	}

	ctx := c.Request().Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case tagged, ok := <-sub:
			if !ok {
				return nil
			}
			if mp, isMsg := tagged.Event.(floor.MessagePosted); isMsg && mp.Seq <= cursor {
				continue
			}
			writeSSE(c, tagged.RoomID, tagged.Event)
		}
	}
}

// writeSSE writes one event to the stream. Stored messages get their seq
// as the SSE id.
func writeSSE(c echo.Context, roomID string, ev floor.ChatEvent) {
	payload := floor.EventJSON(ev)
	if payload == nil {
		return
	}
	if roomID != "" {
		payload["room_id"] = roomID
	}
	data, _ := json.Marshal(payload)
	if mp, ok := ev.(floor.MessagePosted); ok && mp.Seq > 0 {
		fmt.Fprintf(c.Response(), "id: %d\n", mp.Seq)
	}
	fmt.Fprintf(c.Response(), "data: %s\n\n", data)
	c.Response().Flush()
}
