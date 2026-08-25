package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/kit"
)

// readWiring parses a config directory's kit and returns it together with its
// `shell3:` block re-marshalled as YAML, so the strict YAML parser stays the
// one place models, telegram, and mcp are understood. The kit itself is kept
// (LoadedConfig.Kit) rather than parsed a second time by every later caller.
func readWiring(dir string) (*kit.Kit, []byte, error) {
	kitPath := filepath.Join(dir, kit.FileName)
	src, err := os.ReadFile(kitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("no %s — run 'shell3 boot' to create one", kit.FileName)
		}
		return nil, nil, err
	}
	k, err := kit.Parse(src)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", kit.FileName, err)
	}
	if k.Wiring == nil {
		return nil, nil, fmt.Errorf("%s: no `shell3:` wiring block — models and the front-end are declared there", kit.FileName)
	}
	data, err := yaml.Marshal(k.Wiring)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: wiring: %w", kit.FileName, err)
	}
	return k, data, nil
}
