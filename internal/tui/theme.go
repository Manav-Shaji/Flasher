package tui

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Background lipgloss.Color
	Foreground lipgloss.Color
	Dim        lipgloss.Color
	Highlight  lipgloss.Color
	Accent     lipgloss.Color
	Warning    lipgloss.Color
	Error      lipgloss.Color
	Success    lipgloss.Color
	StatusBg   lipgloss.Color
	StatusFg   lipgloss.Color
	Title      lipgloss.Color
	Border     lipgloss.Color
}

var CurrentTheme = Theme{
	Background: lipgloss.Color("#0a0a0a"), // ColorDarkBg
	Foreground: lipgloss.Color("#ffffff"), // ColorText
	Dim:        lipgloss.Color("#8a8a9a"),
	Highlight:  lipgloss.Color("#00f3ff"), // ColorNeonCyan
	Accent:     lipgloss.Color("#bd00ff"), // ColorPurple
	Warning:    lipgloss.Color("#ffaa00"),
	Error:      lipgloss.Color("#ff003c"), // ColorError
	Success:    lipgloss.Color("#00ff66"), // ColorSuccess
	StatusBg:   lipgloss.Color("#0a0a0a"),
	StatusFg:   lipgloss.Color("#ffffff"),
	Title:      lipgloss.Color("#bd00ff"), // ColorPurple
	Border:     lipgloss.Color("#3a3a4a"),
}

var (
	TitleStyle     = lipgloss.NewStyle().Bold(true)
	DimStyle       = lipgloss.NewStyle().Foreground(CurrentTheme.Dim)
	BaseStyle      = lipgloss.NewStyle().Foreground(CurrentTheme.Foreground)
	BorderStyle    = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(CurrentTheme.Border)
	HUDLabelStyle  = lipgloss.NewStyle().Foreground(CurrentTheme.Dim).Bold(true)
	HUDValueStyle  = lipgloss.NewStyle().Foreground(CurrentTheme.Highlight).Bold(true)
	SeparatorStyle = lipgloss.NewStyle().Foreground(CurrentTheme.Dim)
	SelectedStyle  = lipgloss.NewStyle().Bold(true)
	BadgeStyle     = lipgloss.NewStyle().Padding(0, 1).Bold(true)
)

var (
	ColorNeonCyan = lipgloss.Color("#00f3ff")
	ColorPurple   = lipgloss.Color("#bd00ff")
	ColorDarkBg   = lipgloss.Color("#0a0a0a")
	ColorText     = lipgloss.Color("#ffffff")
	ColorError    = lipgloss.Color("#ff003c")
	ColorSuccess  = lipgloss.Color("#00ff66")

	StyleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorNeonCyan).
			Padding(0, 1).
			Border(lipgloss.DoubleBorder(), true).
			BorderForeground(ColorPurple)

	StylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorNeonCyan).
			Padding(0, 1)

	StyleStatus = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorPurple).
			Padding(0, 1)

	StyleError = lipgloss.NewStyle().
			Foreground(ColorError).
			Bold(true)

	StyleSuccess = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true)
	PanelTitle = lipgloss.NewStyle().
			Foreground(CurrentTheme.Foreground).
			Bold(true).
			Align(lipgloss.Left)
)
