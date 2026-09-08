package wrk

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/lispconfig"
)

// Compile emits a launcher pinned to the checked inputs. Execution and snapshot
// admission belong to the durable runtime, including waits and cancellation.
func Compile(def *Definition, configPath string) (string, error) {
	if def == nil {
		return "", fmt.Errorf("wrk: definition is required")
	}
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return "", err
	}
	definitionPath, err := filepath.Abs(def.Path)
	if err != nil {
		return "", err
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	source, err := os.ReadFile(definitionPath)
	if err != nil {
		return "", err
	}
	cfg, err := lispconfig.Parse(configPath, config)
	if err != nil {
		return "", err
	}
	if _, err := Parse(definitionPath, source, cfg); err != nil {
		return "", err
	}
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
# Regenerate after changing either input. shell3 owns execution and durable state.
exec "${SHELL3_BIN:-shell3}" wrk run \
  --config %s --config-sha256 %s --workflow-sha256 %s \
  --state "${SHELL3_WRK_STATE:-}" --run-id "${SHELL3_WRK_RUN_ID:-}" \
  --notify-to "${SHELL3_WRK_NOTIFY_TO:-}" --notify-state "${SHELL3_WRK_NOTIFY_STATE:-}" \
  -- %s "${1:-}"
`, shellQuote(configPath), shellQuote(fmt.Sprintf("%x", sha256.Sum256(config))),
		shellQuote(fmt.Sprintf("%x", sha256.Sum256(source))), shellQuote(definitionPath)), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
