package tui

import "charm.land/lipgloss/v2"

// Theme is one of the palettes selectable with /tema.
type Theme struct {
	Name   string
	Dark   bool
	Bg     string
	Text   string
	Muted  string
	Accent string
	Green  string
	Yellow string
	Red    string
	Pink   string
}

func themes() []Theme {
	return []Theme{
		{Name: "noite", Dark: true, Bg: "#1a1b26", Text: "#c0caf5", Muted: "#565f89", Accent: "#7aa2f7", Green: "#9ece6a", Yellow: "#e0af68", Red: "#f7768e", Pink: "#E8C881"},
		{Name: "oceano", Dark: true, Bg: "#0b1d2a", Text: "#d7e7f5", Muted: "#5b7c99", Accent: "#5eead4", Green: "#86efac", Yellow: "#fde68a", Red: "#fb7185", Pink: "#E8C881"},
		{Name: "âmbar", Dark: true, Bg: "#1c1410", Text: "#f3e6d0", Muted: "#8a7058", Accent: "#f59e0b", Green: "#a3e635", Yellow: "#fbbf24", Red: "#f87171", Pink: "#f0abfc"},
	}
}

func (t Theme) style(fg string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
}

func (t Theme) box() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Muted)).
		Foreground(lipgloss.Color(t.Text)).
		Background(lipgloss.Color(t.Bg)).
		Padding(0, 1)
}
