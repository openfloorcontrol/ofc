package floor

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// APIServer is the small surface Floor needs from the HTTP API layer.
// The concrete implementation lives in the top-level api/ package. Floor.APIServer is
// typed as this interface so the engine doesn't import the api package
// (callers — typically cmd/ — construct api.New(), start it, and assign
// it). The caller owns the server: it starts it before Floor.Start and
// stops it after Floor.Stop; one server can serve several floors.
type APIServer interface {
	// AddFloor serves the floor's sessions (list, create, and per
	// session messages and events), agents, furniture and files under
	// /api/v1/floors/{f.ID()}/. Fails if a floor with that ID is served.
	AddFloor(f *Floor) error

	// RemoveFloor stops serving the floor with the given ID, its
	// furniture's MCP endpoints included.
	RemoveFloor(id string)

	// RegisterFurniture serves one furniture instance of a floor over
	// MCP (Streamable HTTP + SSE). Furniture endpoints also accept the
	// token of an agent that lists the furniture (Floor.AgentMayUse).
	RegisterFurniture(floorID, name string, mcpSrv *mcp.Server)

	// BaseURL returns the running server's base URL
	// (e.g. "http://127.0.0.1:42301"). Empty before the server starts.
	BaseURL() string
}
