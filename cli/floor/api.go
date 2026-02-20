package floor

import (
	"context"
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
