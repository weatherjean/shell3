package patchwidgets

import (
	"errors"
	"fmt"
)

// AskSpec configures a free-text prompt rendered by [Ask].
//
// Input is the question shown above the input line. Default, if non-empty,
// is returned when the user submits an empty line. Placeholder is shown
// dimmed inside the empty input box and never returned. TimeoutSeconds, if
// > 0, cancels the prompt with reason "timeout".
type AskSpec struct {
	Input          string `json:"input"`
	Default        string `json:"default,omitempty"`
	Placeholder    string `json:"placeholder,omitempty"`
	TimeoutSeconds int    `json:"timeout,omitempty"`
}

// Validate checks required fields.
func (s AskSpec) Validate() error {
	if s.Input == "" {
		return errors.New("ask: input is required")
	}
	if s.TimeoutSeconds < 0 {
		return errors.New("ask: timeout must be non-negative")
	}
	return nil
}

// PickChoice is one selectable item in a [PickSpec]. Value is what the
// widget returns; Label is what is shown to the user. If Label is empty,
// Value is shown.
type PickChoice struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Hint  string `json:"hint,omitempty"`
}

// Display returns the user-visible label.
func (c PickChoice) Display() string {
	if c.Label != "" {
		return c.Label
	}
	return c.Value
}

// PickSpec configures a list selector rendered by [Pick].
//
// Input is shown as the title above the list. Choices is required and
// must be non-empty. Default, if set, is matched against choice values
// to pre-select an entry. TimeoutSeconds, if > 0, cancels with reason
// "timeout". Filter enables incremental substring filtering.
type PickSpec struct {
	Input          string       `json:"input"`
	Choices        []PickChoice `json:"choices"`
	Default        string       `json:"default,omitempty"`
	Filter         bool         `json:"filter,omitempty"`
	TimeoutSeconds int          `json:"timeout,omitempty"`
}

// Validate checks required fields.
func (s PickSpec) Validate() error {
	if s.Input == "" {
		return errors.New("pick: input is required")
	}
	if len(s.Choices) == 0 {
		return errors.New("pick: at least one choice is required")
	}
	for i, c := range s.Choices {
		if c.Value == "" {
			return fmt.Errorf("pick: choice %d: value is required", i)
		}
	}
	if s.TimeoutSeconds < 0 {
		return errors.New("pick: timeout must be non-negative")
	}
	return nil
}

// ConfirmSpec configures a yes/no prompt rendered by [Confirm].
//
// Input is the question. Default selects which option is highlighted and
// returned on bare Enter; valid values are "yes", "no", or "" (treated
// as "no"). TimeoutSeconds, if > 0, cancels with reason "timeout".
type ConfirmSpec struct {
	Input          string `json:"input"`
	Default        string `json:"default,omitempty"`
	TimeoutSeconds int    `json:"timeout,omitempty"`
}

// Validate checks required fields.
func (s ConfirmSpec) Validate() error {
	if s.Input == "" {
		return errors.New("confirm: input is required")
	}
	switch s.Default {
	case "", "yes", "no":
	default:
		return errors.New(`confirm: default must be "yes", "no", or ""`)
	}
	if s.TimeoutSeconds < 0 {
		return errors.New("confirm: timeout must be non-negative")
	}
	return nil
}
