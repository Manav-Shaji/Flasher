package tui

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"flasher/internal/core"
	"flasher/internal/version"

	"github.com/charmbracelet/lipgloss"
)

const (
	bannerTitle        = "FLASHER"
	bigTitleMinWidth   = 56
	bigTitleGapHeight  = 25
	bigTitleRuleHeight = 26
)

var engineIdleBg = lipgloss.Color("#1a1a2e")

type headerRenderKey struct {
	width, height int
	state         core.FlashState
	theme         Theme
	glyphs        string
	version       string
}

var headerRenderCache struct {
	sync.RWMutex
	key   headerRenderKey
	value string
	valid bool
}

var blockFont = map[rune][2]string{
	'A': {"▄▀█", "█▀█"},
	'D': {"█▀▄", "█▄▀"},
	'E': {"█▀▀", "██▄"},
	'F': {"█▀▀", "█▀ "},
	'H': {"█ █", "█▀█"},
	'I': {"▀█▀", "▄█▄"},
	'L': {"█  ", "█▄▄"},
	'O': {"█▀█", "█▄█"},
	'R': {"█▀█", "█▀▄"},
	'S': {"█▀▀", "▄▄█"},
	'V': {"█ █", "▀▄▀"},
}

func isASCIIMode() bool { return CurrentGlyphs.Fail == "x" }

func headerHeight(width, height int) int {
	switch {
	case width < bigTitleMinWidth:
		return 2
	case !bigTitleFits(width):
		return 3
	case height >= bigTitleRuleHeight:
		return 5
	case height >= bigTitleGapHeight:
		return 4
	default:
		return 3
	}
}

func bigTitleFits(width int) bool {
	if isASCIIMode() || width < bigTitleMinWidth {
		return false
	}
	rows, ok := blockTitleRows(bannerTitle)
	return ok && lipgloss.Width(rows[0]) <= width
}

func blockTitleRows(name string) (rows [2]string, ok bool) {
	for wi, word := range strings.Fields(name) {
		if wi > 0 {
			rows[0] += "   "
			rows[1] += "   "
		}
		for i, r := range []rune(strings.ToUpper(word)) {
			glyph, found := blockFont[r]
			if !found {
				return rows, false
			}
			if i > 0 {
				rows[0] += " "
				rows[1] += " "
			}
			rows[0] += glyph[0]
			rows[1] += glyph[1]
		}
	}
	return rows, rows[0] != ""
}

func renderHeader(m AppModel) string {
	key := headerRenderKey{
		width: m.Width, height: m.Height, state: m.State,
		theme: CurrentTheme, glyphs: glyphSignature(CurrentGlyphs),
		version: version.Version,
	}
	headerRenderCache.RLock()
	if headerRenderCache.valid && headerRenderCache.key == key {
		value := headerRenderCache.value
		headerRenderCache.RUnlock()
		return value
	}
	headerRenderCache.RUnlock()

	value := renderHeaderUncached(m)
	headerRenderCache.Lock()
	headerRenderCache.key = key
	headerRenderCache.value = value
	headerRenderCache.valid = true
	headerRenderCache.Unlock()
	return value
}

func renderHeaderUncached(m AppModel) string {
	if m.Width < bigTitleMinWidth {
		return renderCompactHeader(m)
	}
	center := lipgloss.NewStyle().Width(m.Width).Align(lipgloss.Center)

	var title string
	big := bigTitleFits(m.Width)
	if big {
		rows, _ := blockTitleRows(bannerTitle)
		title = center.Render(lipgloss.JoinVertical(lipgloss.Left,
			gradientRow(rows[0], CurrentTheme.Accent, CurrentTheme.Highlight),
			gradientRow(rows[1], CurrentTheme.Accent, CurrentTheme.Highlight),
		))
	} else {
		title = center.Render(gradientText(spacedTitle(bannerTitle), CurrentTheme.Accent, CurrentTheme.Highlight))
	}

	engine := engineSegment(m.State)
	bar := Powerline(
		Segment{Text: "VOID", Bg: CurrentTheme.Accent, Fg: CurrentTheme.Background, Bold: true},
		Segment{Text: "PRIME v" + version.Version, Bg: CurrentTheme.Highlight, Fg: CurrentTheme.Background, Bold: true},
		engine,
	)
	bar = center.Render(bar)

	if !big {
		return strings.Join([]string{title, bar, gradientRule(m.Width)}, "\n")
	}
	switch headerHeight(m.Width, m.Height) {
	case 5:
		return strings.Join([]string{title, "", bar, gradientRule(m.Width)}, "\n")
	case 4:
		return strings.Join([]string{title, "", bar}, "\n")
	default:
		return strings.Join([]string{title, bar}, "\n")
	}
}

func renderCompactHeader(m AppModel) string {
	title := gradientText("FLASHER", CurrentTheme.Accent, CurrentTheme.Highlight)
	title = lipgloss.NewStyle().Width(m.Width).Align(lipgloss.Center).Render(title)
	return strings.Join([]string{title, gradientRule(max(1, m.Width))}, "\n")
}

func engineSegment(state core.FlashState) Segment {
	dot := CurrentGlyphs.Dot
	switch state {
	case core.StateFailed:
		return Segment{Text: dot + " ENGINE: ERROR", Bg: CurrentTheme.Error, Fg: CurrentTheme.Background, Bold: true}
	case core.StateIdle, core.StateCompleted:
		return Segment{Text: dot + " ENGINE: ONLINE", Bg: engineIdleBg, Fg: CurrentTheme.Success, Bold: true}
	default:
		return Segment{Text: dot + " ENGINE: BUSY", Bg: CurrentTheme.Warning, Fg: CurrentTheme.Background, Bold: true}
	}
}

func spacedTitle(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' {
			b.WriteString("  ")
			continue
		}
		b.WriteRune(r)
		b.WriteRune(' ')
	}
	return strings.TrimRight(b.String(), " ")
}

func gradientRow(row string, from, to lipgloss.Color) string {
	runes := []rune(row)
	var b strings.Builder
	for x, r := range runes {
		if r == ' ' {
			b.WriteRune(' ')
			continue
		}
		t := 0.0
		if len(runes) > 1 {
			t = float64(x) / float64(len(runes)-1)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(blend(from, to, t)).Render(string(r)))
	}
	return b.String()
}

func gradientText(s string, from, to lipgloss.Color) string {
	runes := []rune(s)
	total := 0
	for _, r := range runes {
		if r != ' ' {
			total++
		}
	}
	var b strings.Builder
	seen := 0
	for _, r := range runes {
		if r == ' ' {
			b.WriteRune(' ')
			continue
		}
		t := 0.0
		if total > 1 {
			t = float64(seen) / float64(total-1)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(blend(from, to, t)).Bold(true).Render(string(r)))
		seen++
	}
	return b.String()
}

func gradientRule(width int) string {
	if width <= 0 {
		return ""
	}
	line, gem := "━", "◆"
	if isASCIIMode() {
		line, gem = "-", "*"
	}
	mid := width / 2
	var b strings.Builder
	for i := 0; i < width; i++ {
		t := 1.0
		if width > 1 {
			t = 1 - math.Abs(2*float64(i)/float64(width-1)-1)
		}
		ch := line
		if i == mid {
			ch = gem
		}
		b.WriteString(lipgloss.NewStyle().Foreground(blend(CurrentTheme.Accent, CurrentTheme.Highlight, t)).Render(ch))
	}
	return b.String()
}

func hexRGB(c lipgloss.Color) (r, g, b int) {
	if _, err := fmt.Sscanf(string(c), "#%02x%02x%02x", &r, &g, &b); err != nil {
		return 255, 255, 255
	}
	return r, g, b
}

func blend(from, to lipgloss.Color, t float64) lipgloss.Color {
	t = math.Max(0, math.Min(1, t))
	r1, g1, b1 := hexRGB(from)
	r2, g2, b2 := hexRGB(to)
	mix := func(a, b int) int { return int(math.Round(float64(a) + float64(b-a)*t)) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(r1, r2), mix(g1, g2), mix(b1, b2)))
}
