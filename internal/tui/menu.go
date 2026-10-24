package tui

import (
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

func GetDefaultMenu() []MenuItem {
	return []MenuItem{
		{Label: "Dashboard", Icon: iconFor("dashboard"), Desc: "Open device dashboard", Action: "dashboard", Section: "DEVICE"},
		{Label: "Refresh Status", Icon: iconFor("refresh"), Desc: "Re-scan device connection", Action: "refresh", Section: "DEVICE"},
		{Label: "Reboot System", Icon: iconFor("rb_system"), Desc: "Reboot to system", Action: "rb_system", Section: "DEVICE"},
		{Label: "Reboot Recovery", Icon: iconFor("rb_recovery"), Desc: "Reboot to recovery", Action: "rb_recovery", Section: "DEVICE"},
		{Label: "Flash Recovery", Icon: iconFor("flash_rec"), Desc: "Select & flash recovery.img (FASTBOOT)", Action: "flash_rec", Section: "FLASH", Danger: true},
		{Label: "Flash Boot", Icon: iconFor("flash_boot"), Desc: "Select & flash boot.img (FASTBOOT)", Action: "flash_boot", Section: "FLASH", Danger: true},
		{Label: "ADB Sideload", Icon: iconFor("sideload"), Desc: "Sideload ROM ZIP (ADB SIDELOAD)", Action: "sideload", Section: "FLASH", Danger: true},
		{Label: "SHA-256 Hash", Icon: iconFor("calculate_sha256"), Desc: "Calculate a file's SHA-256 checksum", Action: "calculate_sha256", Section: "TOOLS"},
		{Label: "Custom Command", Icon: iconFor("custom"), Desc: "Run recognized read-only commands for the selected device", Action: "custom", Section: "TOOLS"},
		{Label: "Wipe Super", Icon: iconFor("wipe_super"), Desc: "Wipe super partition (FASTBOOT)", Action: "wipe_super", Section: "DANGER", Danger: true},
		{Label: "App Settings", Icon: iconFor("settings"), Desc: "Modify preferences and paths", Action: "settings", Section: "SYSTEM"},
		{Label: "Help", Icon: iconFor("help"), Desc: "Show help and shortcuts", Action: "help", Section: "SYSTEM"},
		{Label: "Exit", Icon: iconFor("exit"), Desc: "Exit application", Action: "exit", Section: "SYSTEM"},
	}
}

func (m *AppModel) SetupUI() {
	m.UI.Progress = progress.New(progress.WithDefaultGradient())
	m.UI.TextInput = textinput.New()
	m.UI.TextInput.Placeholder = "Search files..."
	m.UI.TextInput.CharLimit = 128
	m.UI.TextInput.Width = 40
}

// Prefer single-codepoint emoji with default emoji presentation; icons that
// need U+FE0F can misalign between terminals.
func iconFor(action string) string {
	switch action {
	case "dashboard":
		return "\U0001F4CA"
	case "refresh":
		return "\U0001F4E1"
	case "rb_system":
		return "\U0001F504"
	case "rb_recovery":
		return "\U0001F501"
	case "flash_rec":
		return "\U0001F4BE"
	case "flash_boot":
		return "\U0001F462"
	case "sideload":
		return "\U0001F4F2"
	case "custom":
		return "\U0001F4BB"
	case "wipe_super":
		return "\U0001F9F9"
	case "settings":
		return "\u2699\uFE0F"
	case "help":
		return "\u2753"
	case "exit":
		return "\u274C"
	default:
		return "\U0001F510"
	}
}

func iconCells(icon string) int {
	switch icon {
	case "\U0001F4CA", "\U0001F4E1", "\U0001F504", "\U0001F501",
		"\U0001F4BE", "\U0001F462", "\U0001F4F2", "\U0001F4BB",
		"\U0001F9F9", "\u2699\uFE0F", "\u2753", "\u274C", "\U0001F510":
		return 2
	default:
		return lipgloss.Width(icon)
	}
}

func menuIcon(action string) string {
	if isASCIIMode() {
		switch action {
		case "dashboard":
			return "#"
		case "refresh":
			return "r"
		case "rb_system":
			return "o"
		case "rb_recovery":
			return "+"
		case "flash_rec":
			return "v"
		case "flash_boot":
			return "^"
		case "sideload":
			return "~"
		case "custom":
			return ">"
		case "wipe_super":
			return "x"
		case "settings":
			return "="
		case "help":
			return "?"
		case "exit":
			return "q"
		default:
			return "*"
		}
	}
	return iconFor(action)
}
