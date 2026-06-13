package floor

import (
	"io/fs"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openfloorcontrol/ofc/blueprint"
	"github.com/openfloorcontrol/ofc/furniture"
)

// APIServer is the small surface Floor needs from the HTTP API layer.
// The concrete implementation lives in the top-level api/ package. Floor.APIServer is
// typed as this interface so the engine doesn't import the api package
// (callers — typically cmd/ — construct api.New() and assign it).
//
// All methods called by Floor.Start, Floor.AddFurniture, Floor.Stop,
// and floor_acp.go are listed here.
type APIServer interface {
	// SetAuthToken installs token-based auth on /api/* routes.
	// Must be called before Start. Empty token disables auth.
	SetAuthToken(token string)

	// AuthToken returns the currently-configured auth token (empty if
	// auth is disabled).
	AuthToken() string

	// RegisterFloorAPI mounts /api/v1/messages, /api/v1/events,
	// /api/v1/agents, /api/v1/furniture, and /api/v1/file/* against the
	// given Room. workspacePath is a closure so the sandbox can be
	// resolved lazily.
	RegisterFloorAPI(chat *Room, bp *blueprint.Blueprint, furnitureMap map[string]furniture.Furniture, workspacePath func() string)

	// RegisterFurniture mounts the MCP transports (Streamable HTTP +
	// SSE) for one furniture instance.
	RegisterFurniture(floorID, name string, mcpSrv *mcp.Server)

	// ServeStaticWeb serves web/dist as static files with SPA fallback.
	ServeStaticWeb(webFS fs.FS)

	// Start binds the listener and begins serving in a background
	// goroutine. Pass ":0" for auto-assigned port.
	Start(addr string) error

	// Stop shuts down the server.
	Stop() error

	// BaseURL returns the running server's base URL
	// (e.g. "http://127.0.0.1:42301"). Empty before Start.
	BaseURL() string
}
