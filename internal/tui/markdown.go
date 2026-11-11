package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

func (m *Model) markdown(width int, src string) (string, error) {
	if width < 1 {
		width = 80
	}
	if m.md == nil || m.mdWidth != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return "", err
		}
		m.md = r
		m.mdWidth = width
	}
	out, err := m.md.Render(src)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}
