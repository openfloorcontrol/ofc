package blueprint

import (
	"fmt"

	"github.com/mikiobraun/mf/registry"
)

// resolveModels sets each LLM agent's Endpoint, Model, APIKey and
// ModelSource from its model reference (model:, else defaults.model). A
// reference is a name from the blueprint's models section, else an mf
// address — alias, endpoint/model or @tag — resolved in mf's registry
// ($MF_CONFIG_DIR, else ~/.config/mf). The registry is read only when a
// reference isn't a blueprint model. An agent without a reference keeps
// an empty model.
func resolveModels(bp *Blueprint) error {
	for name, def := range bp.Models {
		if def.Endpoint == "" || def.Model == "" {
			return fmt.Errorf("models: %s needs endpoint and model", name)
		}
	}

	var store *registry.Store
	fromMF := func(addr string) (*registry.Ref, string, error) {
		if store == nil {
			dir, err := registry.DefaultDir()
			if err != nil {
				return nil, "", err
			}
			if store, err = registry.Open(dir); err != nil {
				return nil, "", fmt.Errorf("open mf registry: %w", err)
			}
		}
		ref, err := registry.Resolve(store, addr)
		if err != nil {
			return nil, "", err
		}
		key, _ := ref.Key(store)
		return ref, key, nil
	}

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
		if def, ok := bp.Models[ref]; ok {
			a.Endpoint, a.Model, a.APIKey = def.Endpoint, def.Model, def.APIKey
			a.ModelSource = fmt.Sprintf("%s (blueprint) → %s", ref, def.Model)
			continue
		}
		r, key, err := fromMF(ref)
		if err != nil {
			return fmt.Errorf("agent %s: model %q is not in the blueprint's models, and mf: %w", a.ID, ref, err)
		}
		a.Endpoint, a.Model, a.APIKey = r.Endpoint.URL, r.ModelID, key
		a.ModelSource = fmt.Sprintf("%s (mf) → %s/%s", ref, r.Endpoint.Name, r.ModelID)
	}
	return nil
}
