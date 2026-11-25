package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/openfloorcontrol/ofc/floor"
)

// Building manages a server's floors: it knows the stopped ones too,
// starts a floor when a request names it, and creates, stops and deletes
// floors. A server without a Building serves just the floors added with
// AddFloor.
type Building interface {
	// Floors lists every floor, running or not.
	Floors() ([]FloorInfo, error)
	// Templates lists the blueprints floors can be made from.
	Templates() ([]TemplateInfo, error)
	// Open returns the floor with the given ID, starting it if it is
	// stopped; ErrFloorNotFound if there is none. Every request on a
	// floor opens it, so Open also marks the floor as in use.
	Open(id string) (*floor.Floor, error)
	// Create makes a new floor from a template and returns it.
	Create(template string) (FloorInfo, error)
	// Rename gives a floor a new name; its ID stays.
	Rename(id, name string) error
	// Blueprint returns the text of a floor's blueprint.
	Blueprint(id string) (string, error)
	// SaveBlueprint replaces a floor's blueprint with text, if text is a
	// valid blueprint (else a *BlueprintError), and stops the floor so
	// its next request starts it with the new one.
	SaveBlueprint(id, text string) error
	// Stop stops a running floor; a stopped one stays as it is.
	Stop(id string) error
	// Delete stops a floor and removes it with its files.
	Delete(id string) error
}

// FloorInfo describes a floor in GET /api/v1/floors.
type FloorInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Template    string `json:"template,omitempty"`
	Running     bool   `json:"running"`
}

// TemplateInfo describes a blueprint floors can be made from.
type TemplateInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ErrFloorNotFound is returned by Building.Open for an unknown floor.
var ErrFloorNotFound = errors.New("floor not found")

// ErrTemplateNotFound is returned by Building.Create for an unknown
// template.
var ErrTemplateNotFound = errors.New("template not found")

// BlueprintError says why a blueprint was rejected.
type BlueprintError struct{ Err error }

func (e *BlueprintError) Error() string { return e.Err.Error() }
func (e *BlueprintError) Unwrap() error { return e.Err }

// SetBuilding makes b manage the server's floors. Call before Start.
func (s *Server) SetBuilding(b Building) {
	s.building = b
}

// openFloor returns the floor with the given ID: through the Building if
// the server has one, else among the floors added with AddFloor.
func (s *Server) openFloor(id string) (*floor.Floor, error) {
	if s.building != nil {
		return s.building.Open(id)
	}
	if f, ok := s.floor(id); ok {
		return f, nil
	}
	return nil, ErrFloorNotFound
}

// registerBuildingRoutes adds the routes that manage floors. Without a
// Building they answer 404.
func (s *Server) registerBuildingRoutes() {
	s.echo.GET("/api/v1/templates", s.withBuilding(handleListTemplates))
	s.echo.POST("/api/v1/floors", s.withBuilding(handleCreateFloor))
	s.echo.POST("/api/v1/floors/:floor/stop", s.withBuilding(handleStopFloor))
	s.echo.PATCH("/api/v1/floors/:floor", s.withBuilding(handleRenameFloor))
	s.echo.GET("/api/v1/floors/:floor/blueprint", s.withBuilding(handleGetBlueprint))
	s.echo.PUT("/api/v1/floors/:floor/blueprint", s.withBuilding(handleSaveBlueprint))
	s.echo.DELETE("/api/v1/floors/:floor", s.withBuilding(handleDeleteFloor))
}

func (s *Server) withBuilding(h func(echo.Context, Building) error) echo.HandlerFunc {
	return func(c echo.Context) error {
		if s.building == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "this server does not manage floors"})
		}
		return h(c, s.building)
	}
}

// GET /api/v1/floors — the floors, by ID. "manageable" tells whether
// floors can be created, stopped and deleted here.
func (s *Server) handleListFloors(c echo.Context) error {
	var floors []FloorInfo
	if s.building != nil {
		var err error
		if floors, err = s.building.Floors(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	} else {
		floors = s.servedFloors()
	}
	if floors == nil {
		floors = []FloorInfo{}
	}
	return c.JSON(http.StatusOK, map[string]any{"manageable": s.building != nil, "floors": floors})
}

// GET /api/v1/templates — the blueprints floors can be made from.
func handleListTemplates(c echo.Context, b Building) error {
	templates, err := b.Templates()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if templates == nil {
		templates = []TemplateInfo{}
	}
	return c.JSON(http.StatusOK, map[string]any{"templates": templates})
}

// POST /api/v1/floors {"template": name} — make a floor from a template.
func handleCreateFloor(c echo.Context, b Building) error {
	var req struct {
		Template string `json:"template"`
	}
	if err := c.Bind(&req); err != nil || req.Template == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "template is required"})
	}
	info, err := b.Create(req.Template)
	if errors.Is(err, ErrTemplateNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, info)
}

// PATCH /api/v1/floors/:floor {"name": name} — rename a floor.
func handleRenameFloor(c echo.Context, b Building) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}
	return floorResult(c, b.Rename(c.Param("floor"), strings.TrimSpace(req.Name)))
}

// GET /api/v1/floors/:floor/blueprint — {"blueprint": text}, the floor's
// blueprint.yaml.
func handleGetBlueprint(c echo.Context, b Building) error {
	text, err := b.Blueprint(c.Param("floor"))
	if err != nil {
		return floorResult(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"blueprint": text})
}

// PUT /api/v1/floors/:floor/blueprint {"blueprint": text} — replace the
// floor's blueprint. An invalid one is rejected with 400 and the reason.
func handleSaveBlueprint(c echo.Context, b Building) error {
	var req struct {
		Blueprint string `json:"blueprint"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
	}
	err := b.SaveBlueprint(c.Param("floor"), req.Blueprint)
	var bpErr *BlueprintError
	if errors.As(err, &bpErr) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": bpErr.Error()})
	}
	return floorResult(c, err)
}

// POST /api/v1/floors/:floor/stop — stop a running floor.
func handleStopFloor(c echo.Context, b Building) error {
	return floorResult(c, b.Stop(c.Param("floor")))
}

// DELETE /api/v1/floors/:floor — stop a floor and delete its files.
func handleDeleteFloor(c echo.Context, b Building) error {
	return floorResult(c, b.Delete(c.Param("floor")))
}

func floorResult(c echo.Context, err error) error {
	switch {
	case errors.Is(err, ErrFloorNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
