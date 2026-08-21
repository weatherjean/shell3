package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/kit"
)

// KitFileName is the kit a config directory is read from. Its presence is
// what makes a directory a shell3 config — there is no second format.
// Defined in internal/kit, which owns the format; re-exported here because
// this package's callers phrase the "no config here" error.
const KitFileName = kit.FileName

// readWiring returns the YAML wiring for a config directory. A shell3.sh
// carries its wiring in a `shell3:` declaration block, which is re-marshalled
// here so the existing strict YAML parser stays the one place models,
// telegram, and mcp are understood.
func readWiring(dir string) ([]byte, error) {
	kitPath := filepath.Join(dir, KitFileName)
	src, err := os.ReadFile(kitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s — run 'shell3 boot' to create one", KitFileName)
		}
		return nil, err
	}
	k, err := kit.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", KitFileName, err)
	}
	if k.Wiring == nil {
		return nil, fmt.Errorf("%s: no `shell3:` wiring block — models and the front-end are declared there", KitFileName)
	}
	data, err := yaml.Marshal(k.Wiring)
	if err != nil {
		return nil, fmt.Errorf("%s: wiring: %w", KitFileName, err)
	}
	return data, nil
}
