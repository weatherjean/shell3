package dialog

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const ModelPickerID = "model-picker"

// ModelPicker is a dialog for selecting a model from a list.
type ModelPicker struct {
	models   []string
	cursor   int
	onSelect func(string) tea.Cmd
}

func NewModelPicker(models []string, onSelect func(string) tea.Cmd) *ModelPicker {
	return &ModelPicker{models: models, onSelect: onSelect}
}

func (*ModelPicker) ID() string { return ModelPickerID }

var (
	pickerUpKey    = key.NewBinding(key.WithKeys("up", "k"))
	pickerDownKey  = key.NewBinding(key.WithKeys("down", "j"))
	pickerSelectKey = key.NewBinding(key.WithKeys("enter"))
	pickerCloseKey = key.NewBinding(key.WithKeys("esc", "q"))
)

func (p *ModelPicker) HandleMsg(msg tea.Msg) Action {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case key.Matches(kp, pickerCloseKey):
		return ActionClose{}
	case key.Matches(kp, pickerUpKey):
		if p.cursor > 0 {
			p.cursor--
		}
	case key.Matches(kp, pickerDownKey):
		if p.cursor < len(p.models)-1 {
			p.cursor++
		}
	case key.Matches(kp, pickerSelectKey):
		if len(p.models) == 0 {
			return ActionClose{}
		}
		name := p.models[p.cursor]
		var cmd tea.Cmd
		if p.onSelect != nil {
			cmd = p.onSelect(name)
		}
		return ActionCmd{Cmd: cmd}
	}
	return nil
}

var (
	pickerSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#EAB308")).
				Bold(true)
	pickerNormalStyle = lipgloss.NewStyle().
				Foreground(colorFg)
)

func (p *ModelPicker) View(width, height int) string {
	dialogW := width * 2 / 3
	if dialogW < 40 {
		dialogW = 40
	}
	if dialogW > width {
		dialogW = width
	}

	innerW := dialogW - 4
	if innerW < 1 {
		innerW = 1
	}

	title := dialogTitleStyle.Width(innerW).Render("/model")

	var rows []string
	for i, m := range p.models {
		line := "  " + m
		if i == p.cursor {
			line = pickerSelectedStyle.Render(" > " + m)
		} else {
			line = pickerNormalStyle.Render(line)
		}
		rows = append(rows, line)
	}
	body := dialogBodyStyle.Width(innerW).Render(strings.Join(rows, "\n"))
	hint := dialogHintStyle.Width(innerW).Render("↑/↓  k/j  navigate     enter  select     esc  close")

	inner := lipgloss.JoinVertical(lipgloss.Left, title, body, hint)
	box := dialogFrameStyle.Width(dialogW).Render(inner)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
