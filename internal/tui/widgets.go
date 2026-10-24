package tui

import (
	"context"
	"errors"
	"flasher/internal/core"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type Glyphs struct {
	Marker, BarFilled, BarEmpty string
	Sparkline                   []string
	OK, Fail, Optional          string
	Dot, HollowDot              string
	Spinner                     []string
	Ellipsis, Danger            string
	Horizontal, Vertical        string
	TopLeft, TopRight           string
	BottomLeft, BottomRight     string
}

var UnicodeGlyphs = Glyphs{
	Marker: "▸", BarFilled: "▰", BarEmpty: "▱",
	Sparkline: []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"},
	OK:        "✔", Fail: "✖", Optional: "○", Dot: "●", HollowDot: "○",
	Spinner:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	Ellipsis: "…", Danger: "!",
	Horizontal: "─", Vertical: "│", TopLeft: "╭", TopRight: "╮",
	BottomLeft: "╰", BottomRight: "╯",
}

var ASCIIGlyphs = Glyphs{
	Marker: ">", BarFilled: "#", BarEmpty: "-",
	Sparkline: []string{".", "-", "=", "+", "*", "#", "#", "#"},
	OK:        "x", Fail: "x", Optional: "o", Dot: "*", HollowDot: "o",
	Spinner:  []string{"|", "/", "-", "\\"},
	Ellipsis: ".", Danger: "!",
	Horizontal: "-", Vertical: "|", TopLeft: "+", TopRight: "+",
	BottomLeft: "+", BottomRight: "+",
}

var CurrentGlyphs = UnicodeGlyphs

func renderPanel(title, body string, width, height int, focused bool) string {
	if width < 4 || height < 4 {
		return strings.Repeat(" ", max(0, width)) + strings.Repeat("\n", max(0, height-1))
	}
	innerWidth := width - 2
	titleText := " " + title + " "
	titleText = ansi.Truncate(titleText, innerWidth-2, CurrentGlyphs.Ellipsis)
	ruleWidth := max(0, innerWidth-lipgloss.Width(titleText)-1)
	top := CurrentGlyphs.TopLeft + CurrentGlyphs.Horizontal + titleText +
		strings.Repeat(CurrentGlyphs.Horizontal, ruleWidth) + CurrentGlyphs.TopRight
	borderColor := CurrentTheme.Border
	if focused {
		borderColor = CurrentTheme.Highlight
	}
	border := lipgloss.NewStyle().Foreground(borderColor)
	lines := []string{border.Render(top)}
	bodyLines := strings.Split(body, "\n")
	for row := 0; row < height-2; row++ {
		content := ""
		if row < len(bodyLines) {
			content = ansi.Truncate(bodyLines[row], innerWidth, CurrentGlyphs.Ellipsis)
		}
		content += strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(content)))
		lines = append(lines, border.Render(CurrentGlyphs.Vertical)+content+border.Render(CurrentGlyphs.Vertical))
	}
	lines = append(lines, border.Render(CurrentGlyphs.BottomLeft+strings.Repeat(CurrentGlyphs.Horizontal, innerWidth)+CurrentGlyphs.BottomRight))
	return strings.Join(lines, "\n")
}

func gauge(value, minValue, maxValue float64, width int, invert bool) string {
	if width <= 0 || math.IsNaN(value) || math.IsInf(value, 0) ||
		math.IsNaN(minValue) || math.IsNaN(maxValue) || minValue == maxValue {
		return ""
	}
	fraction := (value - minValue) / (maxValue - minValue)
	fraction = math.Max(0, math.Min(1, fraction))
	filled := int(math.Round(fraction * float64(width)))
	color := lipgloss.Color("#00ff66")
	if invert {
		switch {
		case fraction < 0.2:
			color = CurrentTheme.Error
		case fraction < 0.4:
			color = CurrentTheme.Warning
		}
	} else {
		switch {
		case fraction >= 0.85:
			color = CurrentTheme.Error
		case fraction >= 0.6:
			color = CurrentTheme.Warning
		}
	}
	bar := strings.Repeat(CurrentGlyphs.BarFilled, filled) +
		strings.Repeat(CurrentGlyphs.BarEmpty, width-filled)
	return lipgloss.NewStyle().Foreground(color).Render(bar)
}

func sparkline(samples []float64, width int) string {
	if width <= 0 {
		return ""
	}
	values := make([]float64, 0, len(samples))
	for _, value := range samples {
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			values = append(values, value)
		}
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}
	minValue, maxValue := values[0], values[0]
	for _, value := range values[1:] {
		minValue = math.Min(minValue, value)
		maxValue = math.Max(maxValue, value)
	}
	var out strings.Builder
	for _, value := range values {
		level := 3
		if maxValue != minValue {
			level = int(math.Round((value - minValue) / (maxValue - minValue) * 7))
		}
		out.WriteString(CurrentGlyphs.Sparkline[level])
	}
	result := out.String()
	return strings.Repeat(" ", max(0, width-lipgloss.Width(result))) + result
}

func kvRows(pairs [][2]string, width int) []string {
	keyWidth := 0
	for _, pair := range pairs {
		keyWidth = max(keyWidth, lipgloss.Width(pair[0]))
	}
	keyWidth = min(18, keyWidth)
	valueWidth := max(0, width-keyWidth-2)
	rows := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		key := ansi.Truncate(pair[0], keyWidth, CurrentGlyphs.Ellipsis)
		value := ansi.Truncate(pair[1], valueWidth, CurrentGlyphs.Ellipsis)
		rows = append(rows, key+strings.Repeat(" ", max(0, keyWidth-lipgloss.Width(key)))+"  "+value)
	}
	return rows
}

type Samples struct {
	values []float64
	start  int
	count  int
}

func NewSamples(capacity int) *Samples {
	return &Samples{values: make([]float64, max(0, capacity))}
}

func (s *Samples) Add(value float64) {
	if s == nil || len(s.values) == 0 {
		return
	}
	if s.count < len(s.values) {
		s.values[(s.start+s.count)%len(s.values)] = value
		s.count++
		return
	}
	s.values[s.start] = value
	s.start = (s.start + 1) % len(s.values)
}

func (s *Samples) Slice() []float64 {
	if s == nil || s.count == 0 {
		return nil
	}
	result := make([]float64, s.count)
	for i := range result {
		result[i] = s.values[(s.start+i)%len(s.values)]
	}
	return result
}

func (m *AppModel) showError(msg string) tea.Cmd {
	m.ActiveToast = &Toast{Message: msg, Type: core.LogError}
	logs := m.Logs
	if m.ActiveModal == ModalCustom && m.Modal.CustomLogs != nil {
		logs = m.Modal.CustomLogs
	}
	if logs != nil {
		last, ok := logs.Last()
		if !ok || last.Text != msg {
			logs.Add(core.LogEntry{Text: msg, Level: core.LogError, Timestamp: time.Now()})
		}
	}
	m.LogsDirty = true
	return toastTimeoutCmd(6 * time.Second)
}

func userMessage(err error) string {
	if err == nil {
		return "unknown error"
	}
	switch {
	case errors.Is(err, core.ErrBootloaderLocked):
		return "bootloader is locked"
	case errors.Is(err, core.ErrBatteryLow):
		return "battery too low to flash safely"
	case errors.Is(err, core.ErrDeviceDisconnected):
		return "device not in the right mode or disconnected"
	case errors.Is(err, core.ErrHashMismatch):
		return "file hash does not match"
	case errors.Is(err, context.Canceled), strings.Contains(strings.ToLower(err.Error()), "task canceled"):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(strings.ToLower(err.Error()), "task timed out"):
		return "timed out"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "command failed (exit code " + strconv.Itoa(exitErr.ExitCode()) + "), see Output"
	}
	message := strings.TrimPrefix(err.Error(), "action failed: ")
	message = strings.TrimPrefix(message, "flash failed: ")
	if message == "" {
		return "unknown error"
	}
	return message
}

type Segment struct {
	Text string
	Bg   lipgloss.Color
	Fg   lipgloss.Color
	Bold bool
}

func Powerline(segs ...Segment) string {
	var b strings.Builder
	for i, seg := range segs {
		b.WriteString(lipgloss.NewStyle().
			Background(seg.Bg).
			Foreground(seg.Fg).
			Bold(seg.Bold).
			Padding(0, 1).
			Render(seg.Text))
		if i+1 < len(segs) {
			b.WriteString(lipgloss.NewStyle().
				Foreground(seg.Bg).
				Background(segs[i+1].Bg).
				Render(powerlineArrow()))
		}
	}
	return b.String()
}

func powerlineArrow() string {
	if CurrentGlyphs.Fail == "x" {
		return ">"
	}
	return "\ue0b0"
}

type ChipKind int

const (
	ChipError ChipKind = iota
	ChipWarning
	ChipSuccess
	ChipInfo
)

func renderChip(kind ChipKind, msg string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	msg = strings.Join(strings.Fields(strings.NewReplacer("\n", " ", "\r", " ").Replace(msg)), " ")

	label, shortLabel := "✖ ERROR", "✖"
	labelBg, labelFg := CurrentTheme.Error, CurrentTheme.Background
	messageBg, messageFg := CurrentTheme.Accent, CurrentTheme.Foreground
	switch kind {
	case ChipWarning:
		label, shortLabel, labelBg = "! WARNING", "!", CurrentTheme.Warning
	case ChipSuccess:
		label, shortLabel, labelBg = "✔ SUCCESS", "✔", CurrentTheme.Success
	case ChipInfo:
		label, shortLabel, labelBg = "i INFO", "i", CurrentTheme.Accent
	}
	if CurrentGlyphs.Fail == "x" {
		if kind == ChipError || kind == ChipSuccess {
			label, shortLabel = "x "+strings.TrimPrefix(label, "✖ "), "x"
			if kind == ChipSuccess {
				label = "x SUCCESS"
			}
		}
	}
	bolt := "⚡"
	if CurrentGlyphs.Fail == "x" {
		bolt = "!"
	}

	labelSegment := Segment{Text: label, Bg: labelBg, Fg: labelFg, Bold: true}
	shortSegment := Segment{Text: shortLabel, Bg: labelBg, Fg: labelFg, Bold: true}
	boltSegment := Segment{Text: bolt, Bg: CurrentTheme.Highlight, Fg: CurrentTheme.Background, Bold: true}
	messageSegment := Segment{Text: msg, Bg: messageBg, Fg: messageFg, Bold: true}

	if msg == "" {
		for _, candidate := range [][]Segment{{labelSegment, boltSegment}, {shortSegment, boltSegment}, {shortSegment}} {
			rendered := Powerline(candidate...)
			if lipgloss.Width(rendered) <= maxWidth {
				return rendered
			}
		}
		return ansi.Truncate(Powerline(shortSegment), maxWidth, CurrentGlyphs.Ellipsis)
	}

	for _, candidate := range [][]Segment{
		{labelSegment, boltSegment, messageSegment},
		{shortSegment, boltSegment, messageSegment},
		{shortSegment, messageSegment},
	} {
		rendered := Powerline(candidate...)
		if lipgloss.Width(rendered) <= maxWidth {
			return rendered
		}
	}

	for textWidth := lipgloss.Width(msg); textWidth >= 0; textWidth-- {
		truncated := ansi.Truncate(msg, textWidth, "…")
		rendered := Powerline(shortSegment, Segment{
			Text: truncated, Bg: messageBg, Fg: messageFg, Bold: true,
		})
		if lipgloss.Width(rendered) <= maxWidth {
			return rendered
		}
	}
	return ansi.Truncate(Powerline(shortSegment), maxWidth, CurrentGlyphs.Ellipsis)
}
