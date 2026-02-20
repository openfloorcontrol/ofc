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

// RegisterFurniture adds an MCP endpoint for a piece of furniture.
// The endpoint is served at /api/v1/floors/{floor}/mcp/{name}/
func (s *APIServer) RegisterFurniture(floor, name string, mcpSrv *mcp.Server) {
	path := fmt.Sprintf("/api/v1/floors/%s/mcp/%s", floor, name)
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpSrv
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	// Mount the stdlib http.Handler under the MCP path.
	// Echo needs a wildcard suffix to match sub-paths (e.g. /mcp).
	s.echo.Any(path, echo.WrapHandler(handler))
	s.echo.Any(path+"/", echo.WrapHandler(handler))
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
