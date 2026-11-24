package blueprint

import (
	"fmt"

	"github.com/mikiobraun/mf/registry"
)

// ResolvedModel is where a model reference leads.
type ResolvedModel struct {
	Endpoint string // OpenAI-compatible API base URL
	Model    string // the provider's model ID
	APIKey   string
	Source   string // where it came from, for display
	Vision   bool   // takes images: the blueprint model's or mf entry's vision
}

// ResolveModel resolves a model reference: a name from the blueprint's
// models section, else an mf address — alias, endpoint/model or @tag —
// in mf's registry ($MF_CONFIG_DIR, else ~/.config/mf).
func ResolveModel(bp *Blueprint, ref string) (ResolvedModel, error) {
	return (&modelResolver{bp: bp}).resolve(ref)
}

// modelResolver resolves references against one blueprint, reading mf's
// registry once, and only when a reference isn't a blueprint model.
type modelResolver struct {
	bp    *Blueprint
	store *registry.Store
}

func (r *modelResolver) resolve(ref string) (ResolvedModel, error) {
	if def, ok := r.bp.Models[ref]; ok {
		return ResolvedModel{
			Endpoint: def.Endpoint, Model: def.Model, APIKey: def.APIKey, Vision: def.Vision,
			Source: fmt.Sprintf("%s (blueprint) → %s", ref, def.Model),
		}, nil
	}
	if r.store == nil {
		dir, err := registry.DefaultDir()
		if err != nil {
			return ResolvedModel{}, err
		}
		if r.store, err = registry.Open(dir); err != nil {
			return ResolvedModel{}, fmt.Errorf("open mf registry: %w", err)
		}
	}
	m, err := registry.Resolve(r.store, ref)
	if err != nil {
		return ResolvedModel{}, fmt.Errorf("model %q is not in the blueprint's models, and mf: %w", ref, err)
	}
	key, _ := m.Key(r.store)
	return ResolvedModel{
		Endpoint: m.Endpoint.URL, Model: m.ModelID, APIKey: key, Vision: m.Vision,
		Source: fmt.Sprintf("%s (mf) → %s/%s", ref, m.Endpoint.Name, m.ModelID),
	}, nil
}

// resolveModels sets each LLM agent's Endpoint, Model, APIKey and
// ModelSource from its model reference (model:, else defaults.model),
// and the same for floor_control's model. An agent without a reference
// keeps an empty model.
func resolveModels(bp *Blueprint) error {
	for name, def := range bp.Models {
		if def.Endpoint == "" || def.Model == "" {
			return fmt.Errorf("models: %s needs endpoint and model", name)
		}
	}

	r := &modelResolver{bp: bp}
	for i := range bp.Agents {
		a := &bp.Agents[i]
		if a.Type == "acp" {
			if a.ModelRef != "" {
				return fmt.Errorf("agent %s: model is for LLM agents; an ACP agent chooses its own", a.ID)
			}
			continue
		}
		ref := a.ModelRef
		if ref == "" {
			ref = bp.Defaults.Model
		}
		if ref == "" {
			continue
		}
		m, err := r.resolve(ref)
		if err != nil {
			return fmt.Errorf("agent %s: %w", a.ID, err)
		}
		a.Endpoint, a.Model, a.APIKey, a.ModelSource, a.Vision = m.Endpoint, m.Model, m.APIKey, m.Source, m.Vision
	}

	if fc := &bp.FloorControl; fc.ModelRef != "" {
		m, err := r.resolve(fc.ModelRef)
		if err != nil {
			return fmt.Errorf("floor_control: %w", err)
		}
		fc.Endpoint, fc.Model, fc.APIKey, fc.ModelSource = m.Endpoint, m.Model, m.APIKey, m.Source
	}
	return nil
}
