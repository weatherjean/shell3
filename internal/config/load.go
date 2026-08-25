package config

import (
	"fmt"
	"path/filepath"
)

// Load reads the config directory dir: shell3.sh (required — the kit carries
// the wiring, agents, tools, skills and cron jobs) + .env. Absent optional
// pieces disable their features. Every error names the file that caused it.
func Load(dir string) (*LoadedConfig, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	c, err := load(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	return c, nil
}

func load(dir string) (*LoadedConfig, error) {
	// The kit carries its own wiring in a `shell3:` block; here we only lift
	// that. Its agents, tools and skills are loaded by agentsetup.
	k, data, err := readWiring(dir)
	if err != nil {
		return nil, err
	}
	secrets, err := loadDotEnv(filepath.Join(dir, ".env"))
	if err != nil {
		return nil, err
	}
	c := &LoadedConfig{Secrets: secrets, dir: dir, kit: k}
	if err := c.parseYAML(data, secrets); err != nil {
		return nil, err
	}
	return c, nil
}
