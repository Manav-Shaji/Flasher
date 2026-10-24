package tui

import (
	"errors"
	"flasher/internal/core"
	"flasher/internal/platform"
	"flasher/internal/version"
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/*
	───────────────────────────────
	  MAIN VIEW

───────────────────────────────
*/
func (m AppModel) View() string {
	if m.Width == 0 {
		return "Initializing..."
	}
	if m.Screen == ScreenDashboard {
		return fitView(renderDashboardView(m), m.Width, m.Height)
	}
	if m.Width < 20 || m.Height < 10 {
		message := lipgloss.JoinVertical(lipgloss.Center,
			ansi.Truncate("Terminal too small", m.Width, CurrentGlyphs.Ellipsis),
			ansi.Truncate("Need at least 20x10", m.Width, CurrentGlyphs.Ellipsis))
		return fitView(lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center,
			message,
			lipgloss.WithWhitespaceForeground(CurrentTheme.Background),
			lipgloss.WithWhitespaceBackground(CurrentTheme.Background)), m.Width, m.Height)
	}
	m.syncViewportLayout()

	header := renderHeader(m)
	status := renderStatusBar(m, m.Width)
	menuW, detailW, mainH, _, _ := m.GetLayoutDimensions()
	var main string
	switch {
	case menuW == 0:
		main = renderDetails(m, detailW, mainH)
	case detailW == 0:
		main = renderMenu(m, menuW, mainH)
	default:
		menu := renderMenu(m, menuW, mainH)
		details := renderDetails(m, detailW, mainH)
		main = lipgloss.JoinHorizontal(lipgloss.Top, menu, details)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, header, main, status)

	finalView := lipgloss.Place(
		m.Width, m.Height,
		lipgloss.Left, lipgloss.Top,
		content,
		lipgloss.WithWhitespaceForeground(CurrentTheme.Background),
		lipgloss.WithWhitespaceBackground(CurrentTheme.Background),
	)
	if m.ActiveModal != ModalNone && m.ActiveModal != ModalConfirm {
		return fitView(lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center,
			renderModal(m),
			lipgloss.WithWhitespaceForeground(CurrentTheme.Dim),
			lipgloss.WithWhitespaceBackground(CurrentTheme.Background)), m.Width, m.Height)
	}
	return fitView(finalView, m.Width, m.Height)
}

func fitView(view string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	trimmed := strings.TrimRight(view, "\n")
	lines := []string(nil)
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, CurrentGlyphs.Ellipsis)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

/* ───────────────────────────────
   MENU PANEL
 ─────────────────────────────── */

func renderMenu(m AppModel, width, height int) string {
	var b strings.Builder

	innerWidth := max(1, width-2)
	header := lipgloss.NewStyle().
		Background(CurrentTheme.Accent).Foreground(CurrentTheme.Background).
		Width(innerWidth).Align(lipgloss.Center).Bold(true).
		Render(ansi.Truncate(menuTitle(innerWidth), innerWidth, CurrentGlyphs.Ellipsis))

	b.WriteString(header)
	b.WriteByte('\n')

	itemStyle := BaseStyle.Width(innerWidth)
	selStyle := SelectedStyle.Width(innerWidth).Foreground(CurrentTheme.Background).Background(CurrentTheme.Highlight)
	showIcons := innerWidth >= 22
	markerWidth := 2
	iconWidth := 0
	if showIcons {
		iconWidth = 5
	}
	if innerWidth-markerWidth-iconWidth-1 < 4 {
		showIcons = false
		iconWidth = 0
	}
	if innerWidth-markerWidth-iconWidth-1 < 4 {
		markerWidth = 0
	}
	labelWidth := max(0, innerWidth-markerWidth-iconWidth-1)

	visibleCount := max(1, height-4)
	start := 0
	if m.Selection >= visibleCount {
		start = m.Selection - visibleCount + 1
	}
	end := min(len(m.Menu), start+visibleCount)
	for i := start; i < end; i++ {
		it := m.Menu[i]
		marker, style := "  ", itemStyle
		available, _ := m.itemAvailable(it)
		if !available {
			style = itemStyle.Foreground(CurrentTheme.Dim)
		} else if it.Danger {
			style = itemStyle.Foreground(CurrentTheme.Warning)
		} else {
			style = itemStyle.Foreground(sectionColor(it.Section))
		}
		if i == m.Selection {
			marker, style = ">", selStyle
			if markerWidth == 2 {
				marker = "> "
			}
			if !available {
				style = selStyle.Foreground(CurrentTheme.Dim)
			}
		}
		if markerWidth == 0 {
			marker = ""
		}
		row := marker
		if showIcons {
			row += alignMenuIcon(menuIcon(it.Action), iconWidth)
		}
		if markerWidth > 0 || showIcons {
			row += " "
		}
		row += truncateMenuLabel(it.Label, labelWidth)
		b.WriteString(style.Render(row))
		b.WriteByte('\n')
	}

	// Vertical Fill
	fillCount := height - 4 - (end - start)
	if fillCount > 0 {
		b.WriteString(strings.Repeat("\n", fillCount))
	}

	footerText := menuFooter(innerWidth, singlePane(m.Width) && m.Screen != ScreenDashboard)
	footer := DimStyle.Width(max(1, width-2)).Align(lipgloss.Center).Render(footerText)
	b.WriteString(footer)

	return BorderStyle.BorderForeground(CurrentTheme.Accent).Width(max(1, width-2)).Height(max(1, height-2)).Render(b.String())
}

func menuTitle(innerWidth int) string {
	switch {
	case innerWidth >= 15:
		return " ❯❯ COMMANDS "
	case innerWidth >= 6:
		return "MENU"
	default:
		return ""
	}
}

func menuFooter(innerWidth int, narrow bool) string {
	const (
		full    = "↑/↓ • ↵ • D Dashboard • R Refresh"
		compact = "↑/↓ • ↵ • D • R"
		short   = "↑/↓ • ↵"
		toggle  = " • Tab Log"
	)
	if narrow {
		for _, text := range []string{full + toggle, compact + toggle, short + toggle} {
			if lipgloss.Width(text) <= innerWidth {
				return text
			}
		}
	}
	if lipgloss.Width(full) <= innerWidth {
		return full
	}
	if innerWidth >= 16 {
		return compact
	}
	if innerWidth >= 7 {
		return short
	}
	return ""
}

func sectionColor(section string) lipgloss.Color {
	switch section {
	case "DEVICE":
		return CurrentTheme.Highlight
	case "FLASH":
		return CurrentTheme.Accent
	case "TOOLS":
		return CurrentTheme.Success
	case "DANGER":
		return CurrentTheme.Error
	case "SYSTEM":
		return CurrentTheme.Highlight
	default:
		return CurrentTheme.Dim
	}
}

func alignMenuIcon(icon string, width int) string {
	padding := max(0, width-iconCells(icon))
	return icon + strings.Repeat(" ", padding)
}

func truncateMenuLabel(label string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(label) <= width {
		return label
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(label)
	return string(runes[:width-1]) + "…"
}

/* ───────────────────────────────
   DETAILS PANEL
 ─────────────────────────────── */

func renderDetails(m AppModel, width, height int) string {
	var body string
	menuW, detailW, mainH, _, _ := m.GetLayoutDimensions()
	_ = menuW
	_ = detailW
	_ = mainH

	switch {
	case m.ActiveModal == ModalConfirm:
		body = renderConfirmView(m, width)
	case m.State != core.StateIdle:
		body = renderBusyView(m, width)
	case m.Device.Mode == platform.ModeUnauthorized:
		body = renderUnauthorizedView(width)
	case m.Device.Mode != platform.ModeDisconnected && m.Device.Mode != platform.ModeOffline:
		body = renderDeviceHUD(m, width)
	default:
		body = renderInfoView(m, width)
	}

	// Live Logs Section
	logTitle := lipgloss.NewStyle().Background(CurrentTheme.Dim).Foreground(CurrentTheme.Foreground).
		Width(width - 2).Align(lipgloss.Center).Bold(true).Render(" ⚡ LIVE LOGSTREAM ")

	m.UI.Viewport.Width = max(0, detailW-2)
	m.UI.Viewport.Height = max(0, height-lipgloss.Height(body)-4)

	content := lipgloss.JoinVertical(lipgloss.Left, body, "", logTitle, m.UI.Viewport.View())

	return BorderStyle.BorderForeground(CurrentTheme.Highlight).Width(max(1, width-2)).Height(max(1, height-2)).Render(content)
}

/* ───────────────────────────────
   STATUS BAR
 ─────────────────────────────── */

func renderStatusBar(m AppModel, width int) string {
	styles := statusBarStylesFor(CurrentTheme)
	dot, dotStyle := "●", lipgloss.NewStyle()
	switch m.Tick % 6 {
	case 0, 5:
		dot, dotStyle = "○", styles.dotBorder
	case 1, 4:
		dotStyle = styles.dotHighlight
	case 2, 3:
		dotStyle = styles.dotTitle
	}
	heartbeat := styles.heartbeat.Render(dotStyle.Render(dot))
	modeBg, modeIcon := CurrentTheme.Dim, "⚠"
	modeStyle := styles.modeDefault
	switch m.Device.Mode {
	case platform.ModeFastboot:
		modeBg, modeIcon = CurrentTheme.Highlight, "⚡"
		modeStyle = styles.modeFastboot
	case platform.ModeRecovery, platform.ModeDevice, platform.ModeSideload:
		modeBg, modeIcon = CurrentTheme.Success, "📱"
		modeStyle = styles.modeReady
	case platform.ModeUnauthorized:
		modeBg, modeIcon = CurrentTheme.Error, "📵"
		modeStyle = styles.modeUnauthorized
	case platform.ModeOffline:
		modeBg, modeIcon = CurrentTheme.Dim, "💤"
		modeStyle = styles.modeOffline
	}
	tri := "\ue0b0"
	left := lipgloss.JoinHorizontal(lipgloss.Left,
		heartbeat,
		styles.separatorDimAccent.Render(tri),
		styles.appName.Render(" "+strings.Fields(version.AppName)[0]+" "),
		styles.separatorAccentStatus.Render(tri),
		styles.system.Render("SYSTEM"),
		styles.modeSeparators[[2]lipgloss.Color{CurrentTheme.StatusBg, modeBg}].Render(tri),
		modeStyle.Render(fmt.Sprintf("%s %s", modeIcon, m.Device.Mode)),
		styles.separatorModeDim[[2]lipgloss.Color{modeBg, CurrentTheme.Dim}].Render(tri),
		func() string {
			if !m.DeviceReconciliationRequired {
				return ""
			}
			return styles.reconciliation.Render(" STATE UNKNOWN • SCAN REQUIRED ")
		}(),
		styles.serial.Render(m.Device.Serial),
		styles.separatorDimBackground.Render(tri),
	)
	if lipgloss.Width(left) > width {
		left = ansi.Truncate(left, width, CurrentGlyphs.Ellipsis)
	}
	right := ""
	if m.ActiveToast != nil {
		kind := ChipInfo
		if m.ActiveToast.Warning {
			kind = ChipWarning
		} else {
			switch m.ActiveToast.Type {
			case core.LogError:
				kind = ChipError
			case core.LogSuccess:
				kind = ChipSuccess
			}
		}
		availableWidth := max(0, width-lipgloss.Width(left)-1)
		right = renderChip(kind, m.ActiveToast.Message, availableWidth)
	}
	space := max(0, width-lipgloss.Width(left)-lipgloss.Width(right))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", space), right)
}

type statusBarStyleSet struct {
	dotBorder, dotHighlight, dotTitle lipgloss.Style
	heartbeat                         lipgloss.Style
	separatorDimAccent                lipgloss.Style
	appName                           lipgloss.Style
	separatorAccentStatus             lipgloss.Style
	system                            lipgloss.Style
	modeSeparators                    map[[2]lipgloss.Color]lipgloss.Style
	modeDefault, modeFastboot         lipgloss.Style
	modeReady, modeUnauthorized       lipgloss.Style
	modeOffline                       lipgloss.Style
	separatorModeDim                  map[[2]lipgloss.Color]lipgloss.Style
	separatorDimBackground            lipgloss.Style
	reconciliation                    lipgloss.Style
	serial                            lipgloss.Style
}

var statusBarStylesCache struct {
	sync.Mutex
	styles map[Theme]statusBarStyleSet
}

func statusBarStylesFor(theme Theme) statusBarStyleSet {
	statusBarStylesCache.Lock()
	defer statusBarStylesCache.Unlock()
	if styles, ok := statusBarStylesCache.styles[theme]; ok {
		return styles
	}
	modeBackgrounds := []lipgloss.Color{
		theme.Dim, theme.Highlight, theme.Success, theme.Error,
	}
	styles := statusBarStyleSet{
		dotBorder:             lipgloss.NewStyle().Foreground(theme.Border),
		dotHighlight:          lipgloss.NewStyle().Foreground(theme.Highlight).Bold(true),
		dotTitle:              lipgloss.NewStyle().Foreground(theme.Title).Bold(true),
		heartbeat:             lipgloss.NewStyle().Padding(0, 1).Background(theme.Dim),
		separatorDimAccent:    lipgloss.NewStyle().Foreground(theme.Dim).Background(theme.Accent),
		appName:               lipgloss.NewStyle().Background(theme.Accent).Foreground(theme.Background).Bold(true),
		separatorAccentStatus: lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.StatusBg),
		system: lipgloss.NewStyle().Padding(0, 1).Background(theme.StatusBg).
			Foreground(theme.StatusFg).Bold(true),
		modeSeparators:         make(map[[2]lipgloss.Color]lipgloss.Style),
		separatorModeDim:       make(map[[2]lipgloss.Color]lipgloss.Style),
		separatorDimBackground: lipgloss.NewStyle().Foreground(theme.Dim).Background(theme.Background),
		reconciliation: lipgloss.NewStyle().Padding(0, 1).Background(theme.Error).
			Foreground(theme.Background).Bold(true),
		serial: lipgloss.NewStyle().Padding(0, 1).Background(theme.Dim).Foreground(theme.Foreground),
		modeDefault: lipgloss.NewStyle().Padding(0, 1).Background(theme.Dim).
			Foreground(theme.Foreground).Bold(true),
		modeFastboot: lipgloss.NewStyle().Padding(0, 1).Background(theme.Highlight).
			Foreground(theme.Background).Bold(true),
		modeReady: lipgloss.NewStyle().Padding(0, 1).Background(theme.Success).
			Foreground(theme.Background).Bold(true),
		modeUnauthorized: lipgloss.NewStyle().Padding(0, 1).Background(theme.Error).
			Foreground(theme.Background).Bold(true),
		modeOffline: lipgloss.NewStyle().Padding(0, 1).Background(theme.Dim).
			Foreground(theme.Foreground).Bold(true),
	}
	for _, background := range modeBackgrounds {
		modeSeparator := [2]lipgloss.Color{theme.StatusBg, background}
		styles.modeSeparators[modeSeparator] = lipgloss.NewStyle().
			Foreground(theme.StatusBg).Background(background)
		modeDimSeparator := [2]lipgloss.Color{background, theme.Dim}
		styles.separatorModeDim[modeDimSeparator] = lipgloss.NewStyle().
			Foreground(background).Background(theme.Dim)
	}
	if statusBarStylesCache.styles == nil {
		statusBarStylesCache.styles = make(map[Theme]statusBarStyleSet)
	}
	statusBarStylesCache.styles[theme] = styles
	return styles
}

var (
	confirmTitleStyle = TitleStyle.Background(CurrentTheme.Warning).Foreground(CurrentTheme.Background).Align(lipgloss.Center)
	confirmMsgStyle   = TitleStyle.Foreground(CurrentTheme.Highlight).Align(lipgloss.Left).Padding(0, 1)
	confirmWarnStyle  = DimStyle.Align(lipgloss.Center)
	confirmOptsStyle  = BaseStyle.Bold(true)
	confirmBoxStyle   = BorderStyle.BorderForeground(CurrentTheme.Warning).Padding(1, 1)

	unauthHeaderStyle = lipgloss.NewStyle().Background(CurrentTheme.Warning).Foreground(CurrentTheme.Background).Align(lipgloss.Center).Bold(true)

	hudStatusHeader = lipgloss.NewStyle().Background(CurrentTheme.Highlight).Foreground(CurrentTheme.Background).Bold(true).Align(lipgloss.Center)
	hudInfoBox      = SeparatorStyle.BorderStyle(lipgloss.NormalBorder()).Padding(0, 1)
	hudDescHeader   = TitleStyle.Background(CurrentTheme.Dim).Align(lipgloss.Center)
	hudDesc         = BaseStyle

	infoHeader = lipgloss.NewStyle().Background(CurrentTheme.StatusBg).Foreground(CurrentTheme.StatusFg).Align(lipgloss.Center).Bold(true)
	infoDesc   = BaseStyle.Padding(1, 1)
)

func renderConfirmView(m AppModel, width int) string {
	title := confirmTitleStyle.Width(width - 2).Render("⚠️  EXECUTION CONFIRM")
	msg := confirmMsgStyle.Width(width - 6).Render(m.Modal.ConfirmMsg)
	warn := confirmWarnStyle.Width(width - 4).Align(lipgloss.Center).Render("This action cannot be undone.")
	opts := confirmOptsStyle.Width(width - 4).Align(lipgloss.Center).Render("[Y] PROCEED    [N] CANCEL")
	if m.Modal.ConfirmWord != "" {
		warn = confirmWarnStyle.Width(width - 4).Align(lipgloss.Center).
			Render("Type " + m.Modal.ConfirmWord + " to continue: " + m.Modal.ConfirmTyped)
		opts = confirmOptsStyle.Width(width - 4).Align(lipgloss.Center).
			Render("[ENTER] CONTINUE    [ESC] CANCEL")
	}

	box := confirmBoxStyle.Width(width - 4).
		Render(lipgloss.JoinVertical(lipgloss.Left, msg, "", warn, "", opts))

	return lipgloss.JoinVertical(lipgloss.Left, title, box)
}

func renderBusyView(m AppModel, width int) string {
	stateMsg := fmt.Sprintf("⚙️  %s...", strings.ToUpper(m.State.String()))
	box := BorderStyle.BorderForeground(CurrentTheme.Highlight).Width(width - 4).Align(lipgloss.Center).Render(stateMsg)
	if m.ProgressActive {
		progressLine := lipgloss.NewStyle().Width(width - 4).Align(lipgloss.Center).
			Render(m.UI.Progress.View())
		box = lipgloss.JoinVertical(lipgloss.Center, box, progressLine)
	}
	return lipgloss.JoinVertical(lipgloss.Center, "", box)
}

func renderUnauthorizedView(width int) string {
	header := unauthHeaderStyle.Width(width - 2).Render("ACTION REQUIRED")
	warning := renderChip(ChipWarning, "accept the USB debugging prompt", max(0, width-4))
	return lipgloss.JoinVertical(lipgloss.Left, header, "", warning)
}

func renderDeviceHUD(m AppModel, width int) string {
	lblStyle, valStyle := HUDLabelStyle, HUDValueStyle

	statusHeader := hudStatusHeader.Width(width - 2).Render("DEVICE STATUS")

	row := lipgloss.JoinHorizontal(lipgloss.Left,
		lblStyle.Render(" MDL "), valStyle.Render(fmt.Sprintf("%-12s", m.Device.Model)),
		"   ",
		lblStyle.Render(" BAT "), valStyle.Render(fmt.Sprintf("%-9s", m.Device.Battery)),
	)
	if m.Device.Mode == platform.ModeFastboot {
		row += "   " + lblStyle.Render(" SEC ") + valStyle.Render(m.Device.Secure)
	}

	infoBox := hudInfoBox.Width(width - 4).Render(row)

	descHeader := hudDescHeader.Width(width - 2).Render(" COMMAND INFO ")
	desc := hudDesc.Width(width - 4).Render(m.Menu[m.Selection].Desc)

	return lipgloss.JoinVertical(lipgloss.Left, statusHeader, infoBox, "", descHeader, desc)
}

func renderInfoView(m AppModel, width int) string {
	header := infoHeader.Width(width - 2).Render("INFORMATION")
	desc := ""
	parts := []string{header}
	if m.Selection >= 0 && m.Selection < len(m.Menu) {
		desc = infoDesc.Width(width - 4).Render(m.Menu[m.Selection].Desc)
		parts = append(parts, desc)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func renderDashboardView(m AppModel) string {
	if m.Width < 24 || m.Height < 10 {
		return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center,
			"Dashboard needs a larger terminal.",
			lipgloss.WithWhitespaceForeground(CurrentTheme.Background),
			lipgloss.WithWhitespaceBackground(CurrentTheme.Background))
	}
	title := lipgloss.NewStyle().Width(m.Width).Align(lipgloss.Center).
		Foreground(CurrentTheme.Title).Bold(true).Render(version.AppName + "  /  " + dashboardTitle(m.Device.Mode))
	deviceLine := fmt.Sprintf("%s  •  %s", dashboardModeName(m.Device.Mode), m.Device.Serial)
	if m.Device.Model != "" && m.Device.Model != "-" {
		deviceLine += "  •  " + m.Device.Model
	}
	if m.Device.Mode == platform.ModeAmbiguous {
		deviceLine = fmt.Sprintf("AMBIGUOUS  •  %d devices detected", len(m.Device.Candidates))
	}
	if m.Dashboard.UpdatedAt.IsZero() {
		deviceLine += "  •  Not refreshed"
	} else {
		deviceLine += "  •  Updated " + formatUpdateTime(m.Dashboard.UpdatedAt)
	}
	status := lipgloss.NewStyle().Width(m.Width).Align(lipgloss.Center).
		Foreground(CurrentTheme.Dim).Render(deviceLine)
	panels := dashboardPanels(m)
	statusLine := ""
	if m.DeviceReconciliationRequired {
		statusLine = lipgloss.NewStyle().Width(m.Width).Align(lipgloss.Center).
			Render(renderChip(ChipWarning,
				"DEVICE STATE INDETERMINATE — fresh scan required before device-changing actions",
				m.Width))
	} else if m.State != core.StateIdle {
		statusLine = lipgloss.NewStyle().Foreground(CurrentTheme.Dim).Render("Paused: operation running")
	} else if m.Dashboard.Loading {
		statusLine = lipgloss.NewStyle().Foreground(CurrentTheme.Highlight).Render("Collecting device data...")
	} else if m.Dashboard.Err != "" {
		statusLine = lipgloss.NewStyle().Width(m.Width).Align(lipgloss.Center).
			Render(renderChip(ChipError, m.Dashboard.Err, m.Width))
	}
	filterLine := ""
	if m.DashFilterOn {
		filterLine = "Filter: " + m.DashFilter.View()
	} else if m.DashFilter.Value() != "" {
		filterLine = "Filter: " + m.DashFilter.Value()
	}
	noticeRows := 0
	if statusLine != "" {
		noticeRows++
	}
	if filterLine != "" {
		noticeRows++
	}
	availableHeight := max(4, m.Height-4-noticeRows)

	sidebarWidth := min(28, max(18, m.Width/4))
	if m.Width < 52 {
		sidebarWidth = max(1, m.Width/3)
	}
	contentWidth := max(1, m.Width-sidebarWidth-1)
	sidebar := renderDashboardSidebar(m, sidebarWidth, availableHeight)
	panelsView := renderCompactDashboardPanels(m, panels, contentWidth, availableHeight)
	content := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", panelsView)

	footerText := "Esc Home • Tab Panels/Sidebar • ↑/↓ Navigate • Enter Action • R Refresh • Q Quit"
	if m.Device.Mode == platform.ModeAmbiguous {
		footerText = "↑/↓ Select • Enter Choose • 1-9 Quick Select • R Refresh • Esc Home"
	}
	footer := lipgloss.NewStyle().Width(m.Width).Align(lipgloss.Center).Foreground(CurrentTheme.Dim).
		Render(ansi.Truncate(footerText, m.Width, CurrentGlyphs.Ellipsis))
	parts := []string{title, status, content}
	if statusLine != "" {
		parts = append(parts, statusLine)
	}
	if filterLine != "" {
		parts = append(parts, filterLine)
	}
	parts = append(parts, footer)
	result := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return lipgloss.Place(m.Width, m.Height, lipgloss.Left, lipgloss.Top, result,
		lipgloss.WithWhitespaceForeground(CurrentTheme.Background),
		lipgloss.WithWhitespaceBackground(CurrentTheme.Background))
}

func dashboardTitle(mode platform.DeviceMode) string {
	switch mode {
	case platform.ModeFastboot:
		return "FASTBOOT DASHBOARD"
	case platform.ModeDevice, platform.ModeRecovery, platform.ModeSideload:
		return "ADB DASHBOARD"
	default:
		return "DEVICE DASHBOARD"
	}
}

func dashboardModeName(mode platform.DeviceMode) string {
	switch mode {
	case platform.ModeDevice:
		return "ADB"
	case platform.ModeRecovery:
		return "ADB RECOVERY"
	case platform.ModeSideload:
		return "ADB SIDELOAD"
	default:
		return string(mode)
	}
}

func renderDashboardSidebar(m AppModel, width, height int) string {
	if width < 4 || height < 4 {
		return renderPanel("MENU", "", width, height, m.DashSidebar)
	}
	return renderMenu(m, width, height)
}

func renderCompactDashboardPanels(m AppModel, panels []dashboardPanel, width, height int) string {
	if width < 4 || height < 4 {
		return renderPanel("DASHBOARD", "", width, height, false)
	}
	if len(panels) == 0 {
		return renderPanel("DASHBOARD", "No dashboard information available.", width, height, false)
	}
	focus := m.DashFocus % len(panels)
	if width < 48 || height < 12 {
		index := focus
		lines := filterDashboardLines(panels[index].lines, m.DashFilter.Value())
		lines = wrapDashboardLines(lines, width-2)
		lines = dashboardScroll(lines, m.DashScroll, height-2)
		return renderPanel(panels[index].title, strings.Join(lines, "\n"), width, height,
			!m.DashSidebar)
	}
	panelWidth := width / 2
	panelHeight := height / 2
	scrollLines := func(index int) string {
		lines := filterDashboardLines(panels[index].lines, m.DashFilter.Value())
		lines = wrapDashboardLines(lines, panelWidth-2)
		if focus == index {
			lines = dashboardScroll(lines, m.DashScroll, panelHeight-2)
		}
		return strings.Join(lines, "\n")
	}
	leftWidth := panelWidth
	rightWidth := panelWidth
	top := lipgloss.JoinHorizontal(lipgloss.Top,
		renderPanel(panels[0].title, scrollLines(0), leftWidth, panelHeight, !m.DashSidebar && focus == 0),
		renderPanel(panels[1].title, scrollLines(1), rightWidth, panelHeight, !m.DashSidebar && focus == 1))
	if len(panels) == 3 {
		bottomHeight := max(4, height-panelHeight)
		bottomLines := filterDashboardLines(panels[2].lines, m.DashFilter.Value())
		bottomLines = wrapDashboardLines(bottomLines, width-2)
		if focus == 2 {
			bottomLines = dashboardScroll(bottomLines, m.DashScroll, bottomHeight-2)
		}
		bottom := renderPanel(panels[2].title, strings.Join(bottomLines, "\n"), width, bottomHeight,
			!m.DashSidebar && focus == 2)
		return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
	}
	bottom := lipgloss.JoinHorizontal(lipgloss.Top,
		renderPanel(panels[2].title, scrollLines(2), leftWidth, panelHeight, !m.DashSidebar && focus == 2),
		renderPanel(panels[3].title, scrollLines(3), rightWidth, panelHeight, !m.DashSidebar && focus == 3))
	return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
}

type dashboardPanel struct {
	title string
	lines []string
}

func dashboardPanels(m AppModel) []dashboardPanel {
	data := m.Dashboard
	var panels []dashboardPanel
	if m.Device.Mode == platform.ModeFastboot {
		deviceLines := []string{
			"Serial: " + dashValue(firstNonEmpty(m.Device.Serial, propValue(data.Props, "serialno"))),
			"Product: " + dashValue(firstNonEmpty(m.Device.Codename, propValue(data.Props, "product"))),
			"Model: " + dashboardUnknown(m.Device.Model),
			"Connection: FASTBOOT connected",
		}
		bootloaderLines := []string{
			"Bootloader: " + fastbootBootloaderStatus(m.Device),
			"Secure boot: " + dashboardUnknown(firstNonEmpty(m.Device.SecureBoot, propValue(data.Props, "secure"))),
			"Battery: " + fastbootBatteryStatus(m.Device),
			"Bootloader version: " + dashboardUnknown(firstNonEmpty(m.Device.BootloaderVersion, propValue(data.Props, "version-bootloader"))),
			"Hardware revision: " + dashboardUnknown(firstNonEmpty(m.Device.HardwareRevision, propValue(data.Props, "hw-revision"))),
		}
		variableLines := []string{
			"Current slot: " + dashboardUnknown(firstNonEmpty(m.Device.Slot, propValue(data.Props, "current-slot"))),
			"Slot count: " + dashValueInt(m.Device.SlotCount),
		}
		const maxDisplayedKeyVariables = 4
		displayedKeyVariables := 0
		for _, pair := range data.Vars {
			key := strings.ToLower(pair.Key)
			switch key {
			case "anti", "antirollback", "anti-rollback", "partition-version", "partition_version":
				if displayedKeyVariables >= maxDisplayedKeyVariables {
					break
				}
				variableLines = append(variableLines, pair.Key+": "+pair.Value)
				displayedKeyVariables++
			}
		}
		if displayedKeyVariables == 0 {
			variableLines = append(variableLines, "No additional key variables reported.")
		}
		panels = []dashboardPanel{
			{"DEVICE", deviceLines},
			{"BOOTLOADER", bootloaderLines},
			{"KEY VARIABLES", variableLines},
		}
	} else if m.Device.Mode == platform.ModeDevice || m.Device.Mode == platform.ModeRecovery || m.Device.Mode == platform.ModeSideload {
		cpuPct := data.CPUPct
		if !data.Loaded {
			cpuPct = math.NaN()
		}
		deviceLines := []string{
			"Manufacturer: " + dashboardUnknown(firstNonEmpty(m.Device.Manufacturer, propValue(data.Props, "ro.product.manufacturer"))),
			"Model: " + dashboardUnknown(firstNonEmpty(m.Device.Model, propValue(data.Props, "ro.product.model"))),
			"Codename: " + dashboardUnknown(firstNonEmpty(m.Device.Codename, propValue(data.Props, "ro.product.device"))),
			"Serial: " + dashValue(m.Device.Serial),
			"ADB state: " + adbConnectionState(m.Device.Mode),
			"Transport: " + dashboardUnknown(m.Device.Transport),
			"Android boot complete: " + dashboardBootState(firstNonEmpty(m.Device.BootCompleted, propValue(data.Props, "sys.boot_completed"))),
		}
		romLines := []string{
			"Android version: " + dashboardUnknown(propValue(data.Props, "ro.build.version.release")),
			"SDK / API: " + dashboardUnknown(propValue(data.Props, "ro.build.version.sdk")),
			"Build ID: " + dashboardUnknown(propValue(data.Props, "ro.build.id")),
			"Fingerprint: " + dashboardUnknown(propValue(data.Props, "ro.build.fingerprint")),
			"Build type: " + dashboardUnknown(propValue(data.Props, "ro.build.type")),
			"Security patch: " + dashboardUnknown(propValue(data.Props, "ro.build.version.security_patch")),
			"CPU ABI: " + dashboardUnknown(firstNonEmpty(propValue(data.Props, "ro.product.cpu.abi"), propValue(data.Props, "ro.product.cpu.abilist"))),
			"SELinux: " + dashboardUnknown(data.SELinuxState),
			"Verified Boot: " + dashboardUnknown(propValue(data.Props, "ro.boot.verifiedbootstate")),
			"Bootloader lock property: " + dashboardUnknown(propValue(data.Props, "ro.boot.flash.locked")),
			"Root status: UNKNOWN (not reliably probed)",
			"ROM distribution/version: UNKNOWN (not reliably identified)",
			"Current slot: " + dashboardUnknown(propValue(data.Props, "ro.boot.slot_suffix")),
			"A/B support: " + dashboardABSupport(propValue(data.Props, "ro.build.ab_update")),
		}
		batteryPct := data.Device.BatteryPct
		if !data.Loaded {
			batteryPct = -1
		}
		batteryText := dashboardMetric(percentValue(batteryPct))
		if data.Device.BatteryInvalid {
			batteryText = "INVALID (reading rejected)"
		}
		hardwareLines := []string{
			"Battery: " + batteryText,
			"Voltage: " + dashboardMetric(voltageValue(data.Device.BatteryMV)),
			"Temperature: " + dashboardMetric(temperatureValue(data.Device.TempC)),
			"Charging: " + dashboardUnknown(triStateLabel(data.Device.Charging)),
			"CPU usage: " + dashboardMetric(cpuPercentValue(cpuPct)),
			"RAM: " + capacityAvailable(data.RAMTotalKB*1024, remainingBytes(data.RAMTotalKB, data.RAMUsedKB)*1024),
			"Internal storage: " + capacityUsed(data.StorageTotal, data.StorageUsed),
			"Uptime: " + dashboardMetric(formatUptime(data.UptimeSec)),
		}
		workspace := m.BaseDir
		if workspace == "" && m.App != nil && m.App.Config != nil {
			workspace = m.App.Config.BaseDir
		}
		selectedPackage := "none"
		shaStatus := "PENDING: no package selected"
		inputFingerprint := "PENDING: no package selected"
		compatibilityStatus := "UNKNOWN: no package selected"
		if m.SelectedFlashPath != "" {
			selectedPackage = filepath.Base(m.SelectedFlashPath)
			shaStatus = dashboardUnknown(m.SelectedFlashSHA256)
			inputFingerprint = dashboardInputFingerprint(m.SelectedFlashFingerprint)
			compatibilityStatus = dashboardUnknown(m.SelectedFlashCompatibility)
		}
		compatibilityReadiness := compatibilityReadiness(compatibilityStatus)
		readinessLines := []string{
			readinessLine(readinessIdentity(m.Device, data.Props), "Device identity detected"),
			readinessLine("NOT APPLICABLE", "Fastboot bootloader state while in ADB mode"),
			readinessLine("PENDING", "Selected ROM package: "+selectedPackage),
			"Target compatibility: " + compatibilityStatus,
			"Content identity (calculated SHA-256): " + inputFingerprint,
			"SHA-256: " + shaStatus,
			readinessLine(compatibilityReadiness, "Required firmware verification"),
			readinessLine(compatibilityReadiness, "Required partition verification"),
			readinessLine("PENDING", "User backup confirmation"),
			"ROM folder: " + dashValue(workspace),
		}
		panels = []dashboardPanel{
			{"DEVICE", deviceLines},
			{"CURRENT ROM", romLines},
			{"HARDWARE", hardwareLines},
			{"ROM READINESS", readinessLines},
		}
	} else {
		connectionLines := []string{connectionGuidance(m.Device)}
		candidateLines := []string{}
		for i, candidate := range m.Device.Candidates {
			marker := " "
			if i == m.DashCandidateSelection {
				marker = ">"
			}
			candidateLines = append(candidateLines, fmt.Sprintf("%s %d. %s  %s", marker, i+1, candidate.Mode, candidate.Serial))
		}
		if len(candidateLines) == 0 {
			candidateLines = []string{"No device candidates reported."}
		}
		nextSteps := []string{
			"Connect one Android device and enable USB debugging.",
			"Accept the RSA authorization prompt on the device.",
			"For bootloader access, enter Fastboot manually.",
			"Press R to refresh device discovery.",
		}
		if m.Device.Mode == platform.ModeAmbiguous {
			nextSteps = []string{"Select a device with arrows + Enter or number keys.", "Only the selected device will be targeted."}
		}
		panels = []dashboardPanel{
			{"CONNECTION", connectionLines},
			{"DEVICE CANDIDATES", candidateLines},
			{"NEXT STEPS", nextSteps},
			{"FLASH SAFETY", []string{
				"Diagnostics do not flash, erase, or reboot.",
				"Unknown device identity is not treated as compatible.",
				"Select a device before running device actions.",
			}},
		}
	}
	if !data.Loaded {
		for i := range panels {
			if len(panels[i].lines) == 0 {
				panels[i].lines = []string{"Waiting for device data..."}
			}
		}
	}
	return panels
}

func dashboardUnknown(value string) string {
	if value == "" || value == "-" || value == "unknown" {
		return "UNKNOWN"
	}
	return value
}

func dashboardInputFingerprint(fingerprint string) string {
	if len(fingerprint) != 64 {
		return "UNAVAILABLE"
	}
	return fingerprint[:16] + "…"
}

func dashboardMetric(value string) string {
	if value == "" || value == "-" {
		return "UNKNOWN"
	}
	return value
}

func capacityAvailable(total, available uint64) string {
	if total == 0 {
		return "UNKNOWN"
	}
	return fmt.Sprintf("%s total, %s available", formatBytes(total), formatBytes(available))
}

func capacityUsed(total, used uint64) string {
	if total == 0 {
		return "UNKNOWN"
	}
	return fmt.Sprintf("%s used, %s free", formatBytes(used), formatBytes(remainingBytes(total, used)))
}

func readinessLine(state, detail string) string {
	return state + ": " + detail
}

func compatibilityReadiness(status string) string {
	switch {
	case strings.HasPrefix(status, "VERIFIED:"):
		return "VERIFIED"
	case strings.HasPrefix(status, "INCOMPATIBLE:"), strings.HasPrefix(status, "CANDIDATE_MATCH:"):
		return "BLOCKED"
	case strings.HasPrefix(status, "PENDING:"):
		return "PENDING"
	default:
		return "UNKNOWN"
	}
}

func readinessIdentity(device platform.DeviceState, props []platform.KV) string {
	if device.Serial != "" && device.Serial != "-" &&
		firstNonEmpty(device.Codename, propValue(props, "ro.product.device")) != "-" {
		return "VERIFIED"
	}
	return "UNKNOWN"
}

func dashboardABSupport(value string) string {
	switch strings.ToLower(value) {
	case "true":
		return "VERIFIED"
	case "false":
		return "NOT APPLICABLE (single-slot device)"
	default:
		return "UNKNOWN"
	}
}

func dashboardBootState(value string) string {
	switch value {
	case "1", "yes":
		return "yes"
	case "0", "no":
		return "no"
	default:
		return "UNKNOWN"
	}
}

func adbConnectionState(mode platform.DeviceMode) string {
	switch mode {
	case platform.ModeDevice:
		return "connected"
	case platform.ModeRecovery:
		return "recovery"
	case platform.ModeSideload:
		return "sideload"
	default:
		return string(mode)
	}
}

func connectionGuidance(device platform.DeviceState) string {
	switch device.Mode {
	case platform.ModeUnauthorized:
		return "ADB UNAUTHORIZED — unlock the device and accept the USB debugging RSA prompt."
	case platform.ModeOffline:
		return "ADB OFFLINE — reconnect USB, restart debugging, then press R to rescan."
	case platform.ModeAmbiguous:
		return "MULTIPLE DEVICES — select the intended target using number keys 1-9."
	default:
		return "DISCONNECTED — connect a device, then press R to scan again."
	}
}

func voltageValue(millivolts int) string {
	if millivolts < 1000 || millivolts > 6000 {
		return "UNKNOWN"
	}
	return fmt.Sprintf("%d mV", millivolts)
}

func fastbootPreparationStatus(device platform.DeviceState) []string {
	lines := []string{}
	switch fastbootBootloaderStatus(device) {
	case "Locked — flashing blocked":
		lines = append(lines, "BLOCKED: bootloader is locked")
	case "UNKNOWN — unlock state not exposed":
		lines = append(lines, "UNKNOWN: bootloader unlock state")
	default:
		lines = append(lines, "VERIFIED: bootloader reports unlocked")
	}
	switch err := core.CheckBattery(device); {
	case errors.Is(err, core.ErrBatteryLow):
		lines = append(lines, "BLOCKED: "+err.Error())
	case errors.Is(err, core.ErrBatteryInvalid):
		lines = append(lines, "BLOCKED: battery reading is invalid")
	case errors.Is(err, core.ErrBatteryUnknown):
		lines = append(lines, "UNKNOWN: battery level not reported or invalid")
	default:
		lines = append(lines, "VERIFIED: reported battery meets minimum")
	}
	return lines
}

func remainingBytes(total, used uint64) uint64 {
	if used >= total {
		return 0
	}
	return total - used
}

func fastbootBootloaderStatus(device platform.DeviceState) string {
	if device.Unlocked == platform.No {
		return "Locked — flashing blocked"
	}
	if device.Unlocked == platform.Yes {
		return "Unlocked — bootloader state verified"
	}
	return "UNKNOWN — unlock state not exposed"
}

func fastbootBatteryStatus(device platform.DeviceState) string {
	if device.BatteryInvalid {
		return "INVALID — battery reading rejected"
	}
	switch {
	case device.BatteryPct > 0 || (device.BatteryPct == 0 && strings.Contains(device.Battery, "%")):
		if device.BatteryPct > 100 {
			return "UNKNOWN — invalid battery percentage"
		}
		if device.BatteryPct < core.BatteryMinimumPercent {
			return fmt.Sprintf("%d%% — below %d%% minimum", device.BatteryPct, core.BatteryMinimumPercent)
		}
		return fmt.Sprintf("%d%% — meets minimum", device.BatteryPct)
	case device.BatteryMV > 0:
		if device.BatteryMV < 1000 || device.BatteryMV > 6000 {
			return "UNKNOWN — invalid battery voltage"
		}
		if device.BatteryMV < core.BatteryMinimumMV {
			return fmt.Sprintf("%d mV — below %d mV minimum", device.BatteryMV, core.BatteryMinimumMV)
		}
		return fmt.Sprintf("%d mV — meets minimum", device.BatteryMV)
	default:
		return "Not reported — check charge manually"
	}
}

func filterDashboardLines(lines []string, query string) []string {
	if query == "" {
		return lines
	}
	query = strings.ToLower(query)
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), query) {
			result = append(result, line)
		}
	}
	return result
}

func wrapDashboardLines(lines []string, width int) []string {
	width = max(1, width)
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		words := strings.Fields(line)
		if len(words) == 0 {
			wrapped = append(wrapped, "")
			continue
		}

		current := ""
		appendCurrent := func() {
			if current != "" {
				wrapped = append(wrapped, current)
				current = ""
			}
		}
		for _, word := range words {
			if current != "" && lipgloss.Width(current)+1+lipgloss.Width(word) <= width {
				current += " " + word
				continue
			}
			appendCurrent()
			if lipgloss.Width(word) <= width {
				current = word
				continue
			}

			var chunk strings.Builder
			chunkWidth := 0
			for _, r := range word {
				runeWidth := lipgloss.Width(string(r))
				if chunkWidth+runeWidth > width {
					wrapped = append(wrapped, chunk.String())
					chunk.Reset()
					chunkWidth = 0
				}
				chunk.WriteRune(r)
				chunkWidth += runeWidth
			}
			current = chunk.String()
		}
		appendCurrent()
	}
	return wrapped
}

func dashboardScroll(lines []string, offset, height int) []string {
	if offset <= 0 || offset >= len(lines) || height <= 0 {
		return lines
	}
	end := min(len(lines), offset+height)
	return lines[offset:end]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" && value != "-" {
			return value
		}
	}
	return "-"
}

func dashValue(value string) string { return firstNonEmpty(value) }

func dashValueInt(value int) string {
	if value <= 0 {
		return "-"
	}
	return fmt.Sprint(value)
}

func triStateLabel(value platform.TriState) string {
	switch value {
	case platform.Yes:
		return "yes"
	case platform.No:
		return "no"
	default:
		return "unknown"
	}
}

func propValue(props []platform.KV, key string) string {
	for _, pair := range props {
		if pair.Key == key {
			return dashValue(pair.Value)
		}
	}
	return "-"
}

func percentValue(value int) string {
	if value < 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", value)
}

func cpuPercentValue(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", value)
}

func temperatureValue(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "-"
	}
	return fmt.Sprintf("%.1f°C", value)
}

func formatBytes(value uint64) string {
	const unit = uint64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := unit, 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

func formatUptime(seconds uint64) string {
	if seconds == 0 {
		return "-"
	}
	days := seconds / 86400
	hours := seconds % 86400 / 3600
	minutes := seconds % 3600 / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

func formatUpdateTime(value time.Time) string {
	if value.IsZero() {
		return "waiting"
	}
	return value.Format("15:04:05")
}

func renderModal(m AppModel) string {
	w := min(max(4, m.Width-2), max(4, min(120, int(float64(m.Width)*0.8))))

	h := 20
	if m.ActiveModal == ModalHelp {
		h = 26
	}
	if m.ActiveModal == ModalCustom {
		h = 24
	}
	if m.ActiveModal == ModalSettings {
		h = 22
	}
	h = min(max(4, m.Height-2), h)

	style := BorderStyle.Width(w).Height(h).Border(lipgloss.RoundedBorder()).BorderForeground(CurrentTheme.Highlight)

	var body string
	switch m.ActiveModal {
	case ModalFile:
		body = renderFileModal(m, w)
	case ModalSHA256:
		body = renderSHA256Modal(m, w)
	case ModalCustom:
		body = renderCustomModal(m, w, h)
	case ModalSettings:
		body = renderSettingsModal(m, w, h)
	case ModalHelp:
		body = renderHelpModal(m, w)
	case ModalConfirm:
		return ""
	default:
		return ""
	}
	if m.ActiveToast != nil && m.ActiveToast.Type == core.LogError {
		body = lipgloss.JoinVertical(lipgloss.Left,
			renderChip(ChipError, m.ActiveToast.Message, max(1, w-4)),
			body)
	}
	return style.Render(body)
}

func renderSHA256Modal(m AppModel, w int) string {
	contentWidth := max(1, w-4)
	title := TitleStyle.Width(contentWidth).Align(lipgloss.Center).Render(" CALCULATE SHA-256 ")
	fileName := lipgloss.NewStyle().Foreground(CurrentTheme.Highlight).Bold(true).
		Render("File: " + filepath.Base(m.Modal.SHA256Path))
	filePath := DimStyle.Width(max(1, contentWidth-8)).Render("Path: " + m.Modal.SHA256Path)
	prompt := BaseStyle.Width(max(1, contentWidth-8)).Render(m.Modal.ConfirmMsg)
	input := m.UI.TextInput.View()
	help := DimStyle.Align(lipgloss.Center).Width(max(1, contentWidth-8)).
		Render("[ENTER] Calculate / compare    [ESC] Cancel")
	body := lipgloss.JoinVertical(lipgloss.Left, fileName, "", filePath, "", prompt, "", input, "", help)
	box := BorderStyle.Width(max(1, contentWidth-8)).Padding(1, 1).Render(body)
	centeredBox := lipgloss.PlaceHorizontal(contentWidth+4, lipgloss.Center, box)
	return lipgloss.JoinVertical(lipgloss.Left, title, centeredBox)
}

func renderFileModal(m AppModel, w int) string {
	title := TitleStyle.Width(w - 4).Align(lipgloss.Center).Render(m.Modal.FileTitle)
	var b strings.Builder

	// Path Box
	pathLabel := lipgloss.NewStyle().Foreground(CurrentTheme.Background).Background(CurrentTheme.Highlight).Padding(0, 1).Bold(true).Render(" PATH ")
	pathText := lipgloss.NewStyle().Foreground(CurrentTheme.Highlight).Italic(true).Render(" " + m.Modal.FileDir)
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Left, pathLabel, pathText))
	b.WriteString("\n\n")

	// Search Box
	searchLabel := lipgloss.NewStyle().Padding(0, 1).Background(CurrentTheme.Accent).Foreground(CurrentTheme.Background).Bold(true).Render(" 🔍 SEARCH ")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Left, searchLabel, " ", m.UI.TextInput.View()))
	b.WriteString("\n\n")

	start := 0
	if m.Modal.FileCursor > 8 {
		start = m.Modal.FileCursor - 8
	}
	end := start + 10
	if end > len(m.Modal.FileList) {
		end = len(m.Modal.FileList)
	}
	for i := start; i < end; i++ {
		f := m.Modal.FileList[i]
		icon := "📄"
		if f.IsDir {
			icon = "📁"
		}
		if f.Name == "[ SELECT THIS FOLDER ]" {
			icon = "✅"
		}
		name := f.Name
		if len(name) > 40 {
			name = name[:37] + "..."
		}
		if i == m.Modal.FileCursor {
			barWidth := w - 8
			b.WriteString(SelectedStyle.Width(barWidth).Background(CurrentTheme.Highlight).Foreground(CurrentTheme.Background).Render(fmt.Sprintf(" ❯ %s  %s", icon, name)))
			b.WriteByte('\n')
		} else {
			s := BaseStyle
			if f.Name == "[ SELECT THIS FOLDER ]" {
				s = s.Foreground(CurrentTheme.Highlight).Bold(true)
			}
			b.WriteString(fmt.Sprintf("   %s  %s\n", icon, s.Render(name)))
		}
	}
	for i := 0; i < (10 - (end - start)); i++ {
		b.WriteByte('\n')
	}
	fLeft := DimStyle.Render(fmt.Sprintf(" %d items match", len(m.Modal.FileList)))
	fRight := DimStyle.Render("↑/↓ Nav • ↵ Open • Esc Back ")
	space := w - lipgloss.Width(fLeft) - lipgloss.Width(fRight) - 4
	b.WriteString(fmt.Sprintf("\n%s%s%s", fLeft, strings.Repeat(" ", space), fRight))
	return lipgloss.JoinVertical(lipgloss.Center, title, "", lipgloss.NewStyle().Width(w-4).Padding(0, 2).Render(b.String()))
}

func renderCustomModal(m AppModel, w, _ int) string {
	title := TitleStyle.
		Background(CurrentTheme.Highlight).
		Foreground(CurrentTheme.Background).
		Width(w - 4).
		Align(lipgloss.Center).
		Render(" COMMAND CONSOLE ")
	var b strings.Builder
	innerW := w - 6
	// Output Panel
	outputTitle := lipgloss.NewStyle().Foreground(CurrentTheme.Highlight).Bold(true).Render("🖥️  OUTPUT")
	b.WriteString(outputTitle)
	b.WriteByte('\n')

	m.Modal.CustomViewport.Width = innerW
	m.Modal.CustomViewport.Height = 11

	outputContent := m.Modal.CustomViewport.View()
	if m.Modal.CustomLogs.Len() == 0 && m.State == core.StateIdle {
		outputContent = DimStyle.Italic(true).Render("\n\n  Read-only command console. Destructive and unknown commands are blocked.")
	}

	outputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(CurrentTheme.Dim).
		Width(innerW).
		Height(11).
		Padding(0, 1).
		Render(outputContent)
	b.WriteString(outputBox)
	b.WriteByte('\n')

	// Input Panel
	inputLabel := lipgloss.NewStyle().Foreground(CurrentTheme.Background).Background(CurrentTheme.Accent).Bold(true).Padding(0, 1).Render(" COMMAND ")
	m.UI.TextInput.Prompt = lipgloss.NewStyle().Foreground(CurrentTheme.Accent).Render(" ❯ ")
	inputField := m.UI.TextInput.View()
	if m.State != core.StateIdle {
		inputField = DimStyle.Render(fmt.Sprintf("%s... ⚡", m.State.String()))
	}

	inputLine := lipgloss.JoinHorizontal(lipgloss.Left, inputLabel, " ", inputField)
	inputBox := lipgloss.NewStyle().Background(lipgloss.Color("#1a1a2e")).Width(innerW).Padding(0, 1).Render(inputLine)
	b.WriteString(inputBox)
	b.WriteString("\n\n")

	// Footer
	example := "Connect a device; command tool is inferred from its mode"
	switch m.Device.Mode {
	case platform.ModeFastboot:
		example = "Try: 'getvar product' or 'devices' (read-only; Fastboot inferred)"
	case platform.ModeDevice, platform.ModeRecovery, platform.ModeSideload:
		example = "Try: 'shell getprop' or 'devices' (read-only; ADB inferred)"
	}
	examples := DimStyle.Render(" " + example)
	if m.Modal.CustomLogs.Len() > 0 || m.State != core.StateIdle {
		examples = DimStyle.Render(fmt.Sprintf(" History: %d lines", m.Modal.CustomLogs.Len()))
	}

	fLeft, fRight := examples, DimStyle.Render("↵ EXECUTE  •  ESC EXIT ")
	spaceCount := w - lipgloss.Width(fLeft) - lipgloss.Width(fRight) - 8
	if spaceCount < 0 {
		spaceCount = 0
	}

	footer := lipgloss.JoinHorizontal(lipgloss.Bottom, fLeft, strings.Repeat(" ", spaceCount), fRight)
	b.WriteString(footer)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", lipgloss.NewStyle().Padding(0, 2).Render(b.String()))
}

func renderHelpModal(_ AppModel, w int) string {
	title := TitleStyle.Width(w - 4).Align(lipgloss.Center).Render("SYSTEM DOCUMENTATION")
	hl := TitleStyle.Foreground(CurrentTheme.Highlight).Width(w - 8).Align(lipgloss.Center)
	row := func(k, v string) string {
		return fmt.Sprintf(" %s %s\n", lipgloss.NewStyle().Width(24).Foreground(CurrentTheme.Highlight).Render(k), v)
	}

	var b strings.Builder
	b.WriteString(hl.Render("🎮 NAVIGATION & CONTROLS"))
	b.WriteByte('\n')
	b.WriteString(row("[↑/↓] / [W/S]", "Move Selection"))
	b.WriteString(row("[ENTER]", "Execute Action"))
	b.WriteString(row("[ESC/BACK]", "Close Window"))

	b.WriteString(fmt.Sprintf("\n%s\n", hl.Render("📱 DEVICE MODES GUIDE")))
	b.WriteString(" • FASTBOOT : Phone is in Bootloader mode (flashing images)\n")
	b.WriteString(" • SIDELOAD : Recovery mode -> Apply update from ADB\n")
	b.WriteString(" • UNAUTH   : Check phone screen for USB debug prompt\n")

	b.WriteString(fmt.Sprintf("\n%s\n", hl.Render("🛡️ FLASHING SAFETY TIPS")))
	b.WriteString(" • Use original USB 2.0 cables whenever possible\n")
	b.WriteString(" • Ensure battery is at least 30% before flashing\n")
	b.WriteString(" • A/B SLOTS: Tool targets the active slot automatically\n")

	b.WriteString(fmt.Sprintf("\n%s\n", hl.Render("⌨️ CUSTOM COMMANDS")))
	b.WriteString(" Enter the device command only; ADB or Fastboot is inferred\n")
	b.WriteString(" from the connection. Read-only and reboot commands run without\n")
	b.WriteString(" confirmation; commands that modify device data stay guarded.\n")

	b.WriteString(fmt.Sprintf("\n%s", DimStyle.Width(w-8).Align(lipgloss.Center).Render("Build: stable-1.2.0 • Pro Flasher Core")))

	return lipgloss.JoinVertical(lipgloss.Center, title, "", lipgloss.NewStyle().Width(w-4).Padding(0, 2).Render(b.String()))
}

func renderSettingsModal(m AppModel, w, _ int) string {
	title := TitleStyle.Width(w - 4).Align(lipgloss.Center).Render("APPLICATION CONFIGURATION")
	var b strings.Builder
	innerW := w - 6

	renderItem := func(index int, label, desc, pathVal string) {
		b.WriteString(fmt.Sprintf(" %s\n", TitleStyle.Foreground(CurrentTheme.Highlight).Render(label)))
		b.WriteString(fmt.Sprintf("  %s\n", DimStyle.Render(desc)))

		pad := "  "
		if m.Modal.SettingsCursor == index {
			pad = lipgloss.NewStyle().Foreground(CurrentTheme.Accent).Render("❯ ")
		}

		val := pathVal
		if val == "" {
			val = "(NOT SET)"
		}

		pathStr := lipgloss.NewStyle().Foreground(CurrentTheme.Foreground).Render(val)
		b.WriteString(fmt.Sprintf("%s%s\n\n", pad, pathStr))
	}

	renderItem(0, "📂 Base ROM Directory", "The root folder where your custom ROMs and images are stored.", m.App.Config.BaseDir)
	renderItem(1, "📱 Target Device Folder", "Default folder structure path for the current device.", m.App.Config.DevicePath)

	for i := 0; i < 1; i++ {
		b.WriteByte('\n')
	}

	saveLabel := "  [ SAVE AND APPLY CONFIGURATION ]  "
	saveStyle := lipgloss.NewStyle().Foreground(CurrentTheme.Dim)
	if m.Modal.SettingsCursor == 2 {
		saveStyle = lipgloss.NewStyle().Foreground(CurrentTheme.Background).Background(CurrentTheme.Accent).Bold(true)
	}
	saveBtn := lipgloss.NewStyle().Width(innerW).Align(lipgloss.Center).Render(saveStyle.Render(saveLabel))
	b.WriteString(saveBtn)
	b.WriteString("\n\n")

	fLeft, fRight := DimStyle.Render(" TAB Nav • ↵ SELECT "), DimStyle.Render(" ESC CANCEL ")
	spaceCount := w - lipgloss.Width(fLeft) - lipgloss.Width(fRight) - 8
	if spaceCount < 0 {
		spaceCount = 0
	}
	footer := lipgloss.JoinHorizontal(lipgloss.Bottom, fLeft, strings.Repeat(" ", spaceCount), fRight)
	b.WriteString(footer)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", lipgloss.NewStyle().Padding(0, 2).Render(b.String()))
}
