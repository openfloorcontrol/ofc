package blueprint

import (
	"fmt"

	"github.com/mikiobraun/mf/registry"
)

// resolveMF fills endpoint, model and api_key from mf addresses: an
// agent's mf sets the agent's own, defaults.mf sets the defaults. An
// address is an mf alias, endpoint/model, or @tag, resolved in mf's
// registry ($MF_CONFIG_DIR, else ~/.config/mf). The registry is read only
// when the blueprint uses mf.
func resolveMF(bp *Blueprint) error {
	var store *registry.Store
	resolve := func(addr string) (endpoint, model, key string, err error) {
		if store == nil {
			dir, err := registry.DefaultDir()
			if err != nil {
				return "", "", "", err
			}
			if store, err = registry.Open(dir); err != nil {
				return "", "", "", fmt.Errorf("open mf registry: %w", err)
			}
		}
		ref, err := registry.Resolve(store, addr)
		if err != nil {
			return "", "", "", err
		}
		key, _ = ref.Key(store)
		return ref.Endpoint.URL, ref.ModelID, key, nil
	}

	if d := &bp.Defaults; d.MF != "" {
		if d.Endpoint != "" || d.Model != "" || d.APIKey != "" {
			return fmt.Errorf("defaults: mf sets endpoint, model and api_key; set either mf or those")
		}
		var err error
		if d.Endpoint, d.Model, d.APIKey, err = resolve(d.MF); err != nil {
			return fmt.Errorf("defaults: mf %q: %w", d.MF, err)
		}
	}
	for i := range bp.Agents {
		a := &bp.Agents[i]
		if a.MF == "" {
			continue
		}
		if a.Type == "acp" {
			return fmt.Errorf("agent %s: mf sets an LLM agent's model; an ACP agent chooses its own", a.ID)
		}
		if a.Endpoint != "" || a.Model != "" || a.APIKey != "" {
			return fmt.Errorf("agent %s: mf sets endpoint, model and api_key; set either mf or those", a.ID)
		}
		var err error
		if a.Endpoint, a.Model, a.APIKey, err = resolve(a.MF); err != nil {
			return fmt.Errorf("agent %s: mf %q: %w", a.ID, a.MF, err)
		}
	}
	return nil
}
