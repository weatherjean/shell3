//go:build unix

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/orchestrator"
	scheduler "github.com/weatherjean/shell3/internal/schedule"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
)

// telegramHostController is the one control plane shared by the human
// /reload command and the model-facing shell3 tool. Its mutex serializes
// config generations across concurrent Telegram rooms.
type telegramHostController struct {
	mu sync.Mutex

	configPath     string
	workDir        string
	rt             *shell3.Runtime
	bot            *telegram.Bot
	current        *lispconfig.Config
	currentLoaded  time.Time
	restartEnabled bool
	applyAllowFrom bool
}

type restartRequiredError struct{ reasons []string }

func (e *restartRequiredError) Error() string {
	return "restart required: " + strings.Join(e.reasons, "; ")
}

func telegramRestartReasons(current, fresh *lispconfig.Config) []string {
	if current == nil || current.Telegram == nil || fresh == nil || fresh.Telegram == nil {
		return nil
	}
	var reasons []string
	if fresh.Telegram.TokenEnv != current.Telegram.TokenEnv {
		reasons = append(reasons, "telegram token-env changed")
	}
	if fresh.Telegram.HomeChat != current.Telegram.HomeChat {
		reasons = append(reasons, "telegram home-chat changed")
	}
	if !scheduler.SameDeclarations(fresh.Schedules, current.Schedules) {
		reasons = append(reasons, "schedule declarations changed")
	}
	return reasons
}

func telegramChange(current, fresh *lispconfig.Config) (string, []string) {
	if reflect.DeepEqual(current, fresh) {
		return "none", nil
	}
	reasons := telegramRestartReasons(current, fresh)
	if len(reasons) > 0 {
		return "restart_required", reasons
	}
	return "reloadable", nil
}

func telegramChangedSections(current, fresh *lispconfig.Config) []string {
	sections := make([]string, 0, 8)
	for _, section := range []struct {
		name    string
		changed bool
	}{
		{"memory", current.Memory != fresh.Memory},
		{"models", !reflect.DeepEqual(current.Models, fresh.Models)},
		{"orchestrator", !reflect.DeepEqual(current.Main, fresh.Main)},
		{"skills", !reflect.DeepEqual(current.Skills, fresh.Skills)},
		{"runners", !reflect.DeepEqual(current.Runners, fresh.Runners)},
		{"agents", !reflect.DeepEqual(current.Agents, fresh.Agents)},
		{"schedules", !reflect.DeepEqual(current.Schedules, fresh.Schedules)},
		{"telegram", !reflect.DeepEqual(current.Telegram, fresh.Telegram)},
	} {
		if section.changed {
			sections = append(sections, section.name)
		}
	}
	return sections
}

// telegramConfigFingerprint hashes the parsed configuration rather than the
// source text. Formatting and comments therefore do not manufacture a change,
// matching telegramChange's semantic comparison. Config contains secret
// environment-variable names but never resolved credential values.
func telegramConfigFingerprint(cfg *lispconfig.Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("fingerprint config: %w", err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum), nil
}

func (c *telegramHostController) load() (*lispconfig.Config, error) {
	fresh, err := lispconfig.Load(c.configPath)
	if err != nil {
		return nil, err
	}
	if fresh.Telegram == nil {
		return nil, fmt.Errorf("%s: missing telegram form", c.configPath)
	}
	if fresh.Main == nil {
		return nil, fmt.Errorf("%s: missing orchestrator form", c.configPath)
	}
	if _, err := scheduler.Resolve(c.configPath, fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}

func (c *telegramHostController) configState(fresh *lispconfig.Config) (telegram.ControlResult, error) {
	if c.currentLoaded.IsZero() {
		c.currentLoaded = time.Now().UTC()
	}
	activeFingerprint, err := telegramConfigFingerprint(c.current)
	if err != nil {
		return nil, err
	}
	diskFingerprint, err := telegramConfigFingerprint(fresh)
	if err != nil {
		return nil, err
	}
	change, reasons := telegramChange(c.current, fresh)
	result := telegram.ControlResult{
		"config_valid":              true,
		"config_change":             change,
		"changed_sections":          telegramChangedSections(c.current, fresh),
		"restart_required":          len(reasons) > 0,
		"active_config_fingerprint": activeFingerprint,
		"disk_config_fingerprint":   diskFingerprint,
		"active_config_loaded_at":   c.currentLoaded.UTC().Format(time.RFC3339Nano),
	}
	if len(reasons) > 0 {
		result["reasons"] = reasons
	}
	return result, nil
}

func (c *telegramHostController) inspect(action string) (telegram.ControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fresh, err := c.load()
	if err != nil {
		return nil, err
	}
	result, err := c.configState(fresh)
	if err != nil {
		return nil, err
	}
	result["ok"] = true
	if action == "status" {
		result["host"] = "telegram"
		result["restart_pending"] = c.bot.RestartPending()
	}
	return result, nil
}

func (c *telegramHostController) status(context.Context) (telegram.ControlResult, error) {
	return c.inspect("status")
}

func (c *telegramHostController) validate(context.Context) (telegram.ControlResult, error) {
	return c.inspect("validate")
}

func (c *telegramHostController) reload(context.Context) (telegram.ControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var checked *lispconfig.Config
	var appliedChange string
	var appliedSections []string
	fresh, err := orchestrator.ReloadChecked(c.rt, c.configPath, c.workDir, true, func(candidate *lispconfig.Config) error {
		checked = candidate
		appliedChange, _ = telegramChange(c.current, candidate)
		appliedSections = telegramChangedSections(c.current, candidate)
		return validateTelegramReload(c.configPath, c.current, candidate)
	})
	if err != nil {
		var restartErr *restartRequiredError
		if errors.As(err, &restartErr) {
			result, stateErr := c.configState(checked)
			if stateErr != nil {
				return nil, stateErr
			}
			result["ok"] = false
			result["reload"] = "rejected"
			return result, nil
		}
		return nil, err
	}
	if err := c.applyTelegramSettings(fresh); err != nil {
		return nil, err
	}
	c.current = fresh
	c.currentLoaded = time.Now().UTC()
	result, err := c.configState(fresh)
	if err != nil {
		return nil, err
	}
	result["ok"] = true
	result["applied_config_change"] = appliedChange
	result["applied_sections"] = appliedSections
	result["effective"] = "future_turns"
	return result, nil
}

func (c *telegramHostController) prepareRestart(context.Context) (telegram.ControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.restartEnabled {
		return nil, errors.New("restart is unavailable in telegram --console test mode")
	}
	fresh, err := c.load()
	if err != nil {
		return nil, err
	}
	model := fresh.Models[fresh.Main.Model]
	if _, err := lispconfig.ResolveSecret(model.APIKeyEnv); err != nil {
		return nil, fmt.Errorf("%s: %w", c.configPath, err)
	}
	if _, err := lispconfig.ResolveSecret(fresh.Telegram.TokenEnv); err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	result, err := c.configState(fresh)
	if err != nil {
		return nil, err
	}
	result["ok"] = true
	return result, nil
}

func (c *telegramHostController) reloadCommand() error {
	result, err := c.reload(context.Background())
	if err != nil {
		return err
	}
	if required, _ := result["restart_required"].(bool); required {
		reasons, _ := result["reasons"].([]string)
		return &restartRequiredError{reasons: reasons}
	}
	return nil
}

func (c *telegramHostController) applyTelegramSettings(cfg *lispconfig.Config) error {
	c.bot.SetMaxConcurrentTurns(cfg.Telegram.MaxConcurrentTurns)
	c.bot.SetAnswerAllGroupMessages(cfg.Telegram.GroupMessages == "all")
	if !c.applyAllowFrom {
		return nil
	}
	return c.bot.SetAllowFrom(cfg.Telegram.AllowFrom)
}
