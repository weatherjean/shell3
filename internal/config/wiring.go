package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/kit"
)

// KitFileName is the kit a config directory is read from when present. Its
// presence is what enables the kit model — there is no toggle.
const KitFileName = "shell3.sh"

// readWiring returns the YAML wiring for a config directory and whether it came
// from a kit. A shell3.sh carries its wiring in a `shell3:` declaration block,
// which is re-marshalled here so the existing strict YAML parser stays the one
// place models, telegram, and mcp are understood.
//
// Falls back to shell3.yaml when no kit is present.
func readWiring(dir string) ([]byte, bool, error) {
	kitPath := filepath.Join(dir, KitFileName)
	src, err := os.ReadFile(kitPath)
	switch {
	case err == nil:
		k, perr := kit.Parse(src)
		if perr != nil {
			return nil, false, fmt.Errorf("%s: %w", KitFileName, perr)
		}
		if k.Wiring == nil {
			return nil, false, fmt.Errorf("%s: no `shell3:` wiring block — models and the front-end are declared there", KitFileName)
		}
		data, merr := yaml.Marshal(k.Wiring)
		if merr != nil {
			return nil, false, fmt.Errorf("%s: wiring: %w", KitFileName, merr)
		}
		return data, true, nil
	case !os.IsNotExist(err):
		return nil, false, err
	}

	data, err := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, fmt.Errorf("no %s or shell3.yaml — run 'shell3 boot' to create one", KitFileName)
		}
		return nil, false, err
	}
	return data, false, nil
}
