package tui

import (
	"flasher/internal/core"
	"flasher/internal/platform"

	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

/* MESSAGES */

type ToastTimeoutMsg struct{}
type SetupMainDirMsg string
type SetupDeviceDirMsg string
type LogTickMsg time.Time

type SetupConfirmMsg struct {
	Msg         string
	Cmd         tea.Cmd
	Warning     string
	ConfirmWord string
}
type SHA256FileSelectedMsg string
type SetupValidationErrorMsg struct{ Err error }

type SelectedInputFingerprintMsg struct {
	RequestID   uint64
	Path        string
	Fingerprint string
	Integrity   *PackageIntegrityMsg
	Err         error
	OnSelect    func(string, string) tea.Cmd
}

type SettingsFolderSelectedMsg struct {
	Index int
	Path  string
}

type SearchDebounceMsg struct {
	Query string
}

type PackageIntegrityMsg struct {
	Path   string
	Status string
}

type PackageCompatibilityMsg struct {
	Path     string
	Serial   string
	Mode     platform.DeviceMode
	Codename string
	Status   string
}

func DashboardTickCmd(gen int, mode platform.DeviceMode) tea.Cmd {
	if mode != platform.ModeFastboot && mode != platform.ModeDevice &&
		mode != platform.ModeRecovery {
		return nil
	}
	interval := 2 * time.Second
	if mode == platform.ModeFastboot {
		interval = 3 * time.Second
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return platform.DashboardTickMsg{Gen: gen} })
}

/* INIT */

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		platform.PollTickCmd(),
		platform.HeartbeatCmd(),
		m.App.Engine.WaitForLogs(),
		LogTickCmd(),
		textinput.Blink,
	)
}

func LogTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return LogTickMsg(t)
	})
}

/* UPDATE */

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.MouseMsg:
		return m, nil

	case platform.HeartbeatMsg:
		m.Tick++
		return m, platform.HeartbeatCmd()

	case LogTickMsg:
		if m.LogsDirty {
			m.syncViewportLayout()
			if m.ActiveModal == ModalCustom {
				innerW := m.Modal.CustomViewport.Width
				if innerW <= 0 {
					innerW = max(20, m.Width-10)
				}
				m.Modal.CustomViewport.SetContent(RenderLogsStr(m.Modal.CustomLogs, innerW))
				m.Modal.CustomViewport.GotoBottom()
			} else {
				m.UI.Viewport.SetContent(RenderLogsStr(m.Logs, m.UI.Viewport.Width))
				m.UI.Viewport.GotoBottom()
			}
			m.LogsDirty = false
		}
		return m, LogTickCmd()

	case platform.PollMsg:
		return m.handlePollMsg(msg)

	case platform.DeviceUpdateMsg:
		return m.handleDeviceUpdate(msg)

	case platform.DeviceScanMsg:
		if msg.Gen != m.DashGen {
			return m, nil
		}
		if msg.Fresh {
			m.reconcileInterruptedOperation(platform.DeviceState(msg.Device))
		}
		return m.handleDeviceUpdate(platform.DeviceUpdateMsg(msg.Device))

	case platform.DeviceSelectionMsg:
		if msg.Gen != m.DashGen || m.Screen != ScreenDashboard {
			return m, nil
		}
		m.reconcileInterruptedOperation(platform.DeviceState(msg.Device))
		return m.handleDeviceUpdate(platform.DeviceUpdateMsg(msg.Device))

	case core.LogMsg:
		return m.handleLogMsg(msg)

	case core.TaskCompleteMsg:
		return m.handleTaskComplete(msg)

	case core.ProgressMsg:
		pct := float64(msg)
		if math.IsNaN(pct) || math.IsInf(pct, 0) {
			return m, nil
		}
		m.ProgressPct = min(100, max(0, pct))
		m.ProgressActive = true
		return m, m.UI.Progress.SetPercent(m.ProgressPct / 100)

	case PackageIntegrityMsg:
		if msg.Path == m.SelectedFlashPath {
			m.SelectedFlashSHA256 = msg.Status
		}
		return m, nil
	case PackageCompatibilityMsg:
		if msg.Path == m.SelectedFlashPath && msg.Serial == m.Device.Serial &&
			msg.Mode == m.Device.Mode && msg.Codename == m.Device.Codename {
			m.SelectedFlashCompatibility = msg.Status
		}
		return m, nil
	case SelectedInputFingerprintMsg:
		if msg.RequestID != m.SelectedFingerprintRequest || msg.Path != m.SelectedFlashPath {
			return m, nil
		}
		m.SelectedFlashFingerprint = msg.Fingerprint
		if msg.Err != nil {
			m.SelectedFlashFingerprint = ""
			m.SelectedFlashSHA256 = "ERROR: could not fingerprint selected content"
			if msg.Integrity != nil {
				m.SelectedFlashSHA256 = msg.Integrity.Status
			}
			return m, tea.Batch(m.showError(msg.Err.Error()), msg.OnSelect(msg.Path, ""))
		}
		if msg.Integrity != nil {
			m.SelectedFlashSHA256 = msg.Integrity.Status
		}
		return m, msg.OnSelect(msg.Path, msg.Fingerprint)

	case progress.FrameMsg:
		updated, cmd := m.UI.Progress.Update(msg)
		m.UI.Progress = updated.(progress.Model)
		return m, cmd

	case platform.DashboardMsg:
		return m.handleDashboardMsg(msg)

	case platform.DashboardTickMsg:
		if msg.Gen != m.DashGen || m.Screen != ScreenDashboard || m.State != core.StateIdle {
			return m, nil
		}
		m.Dashboard.Loading = true
		m.Dashboard.Err = ""
		return m, platform.CollectDashboardCmd(m.Device.Serial, m.Device.Mode, false, m.DashGen)

	case ToastTimeoutMsg:
		m.ActiveToast = nil
		return m, nil

	case SearchDebounceMsg:
		if m.ActiveModal == ModalFile && m.UI.TextInput.Value() == msg.Query {
			val := strings.ToLower(msg.Query)
			if val != "" {
				var filtered []FileItem
				for _, f := range m.Modal.FullFileList {
					if strings.Contains(strings.ToLower(f.Name), val) {
						filtered = append(filtered, f)
					}
				}
				m.Modal.FileList = filtered
			} else {
				m.Modal.FileList = m.Modal.FullFileList
			}
			m.Modal.FileCursor = 0
		}
		return m, nil

	case SHA256FileSelectedMsg:
		m.Modal.SHA256Path = string(msg)
		m.Modal.ConfirmMsg = "Enter an expected SHA-256 to compare, or leave blank to only calculate."
		m.ActiveModal = ModalSHA256
		m.UI.TextInput.Placeholder = "Expected SHA-256 (optional)"
		m.UI.TextInput.CharLimit = 64
		m.UI.TextInput.Reset()
		return m, m.UI.TextInput.Focus()
	case SetupMainDirMsg, SetupDeviceDirMsg, SetupConfirmMsg, SetupValidationErrorMsg, SettingsFolderSelectedMsg:
		return m.handleSetupMsg(msg)
	}

	return m, nil
}

func (m AppModel) GetLayoutDimensions() (menuW, detailW, mainH, bodyH, logH int) {
	if m.Width == 0 || m.Height == 0 {
		return
	}

	// Reserve space for the banner and status row.
	headerH := headerHeight(m.Width, m.Height)
	statusH := 1

	mainH = m.Height - headerH - statusH
	if mainH < 1 {
		mainH = 1
	}

	if singlePane(m.Width) {
		if m.PaneDetails {
			detailW = m.Width
		} else {
			menuW = m.Width
		}
		bodyH = 5
		if m.State != core.StateIdle || m.ActiveModal == ModalConfirm {
			bodyH = 4
		}
		logH = mainH - bodyH - 4
		if logH < 0 {
			logH = 0
		}
		return
	}

	menuW = 32
	if m.Width < 70 || m.Height < 18 {
		menuW = m.Width
	}
	if menuW < 25 {
		menuW = min(m.Width, 25)
	} else if menuW > 40 {
		menuW = 40
	}
	if menuW > m.Width/2 {
		menuW = m.Width / 2
	}
	detailW = m.Width - menuW

	bodyH = 5
	if m.State != core.StateIdle || m.ActiveModal == ModalConfirm {
		bodyH = 4
	}

	logH = mainH - bodyH - 4
	if logH < 0 {
		logH = 0
	}
	return
}

func singlePane(width int) bool { return width < 48 }

func (m *AppModel) syncViewportLayout() {
	if m.Width <= 0 || m.Height <= 0 {
		return
	}

	_, detailW, _, _, logH := m.GetLayoutDimensions()
	m.UI.Viewport.Width = max(0, detailW-2)
	m.UI.Viewport.Height = max(0, logH)

	if m.ActiveModal == ModalCustom {
		m.Modal.CustomViewport.Width = max(20, m.Width-10)
		m.Modal.CustomViewport.Height = max(5, m.Height/3)
	}
}

func toastTimeoutCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return ToastTimeoutMsg{} })
}

func RenderLogsStr(logs *LogBuffer, width int) string {
	if logs == nil {
		return ""
	}
	glyphs := glyphSignature(CurrentGlyphs)
	cache := &logs.render
	if cache.initialized && cache.width == width && cache.theme == CurrentTheme && cache.glyphs == glyphs {
		if cache.revision == logs.revision {
			return cache.output
		}
		if logs.lastChange == logBufferAppend && logs.revision == cache.revision+1 &&
			logs.head == cache.head && logs.count == cache.count+1 && logs.size == cache.size {
			entry, _ := logs.Last()
			cache.output += renderLogEntry(entry, logStylesFor(width, CurrentTheme)) + logEntryNewline(entry)
			updateLogRenderCache(cache, logs, width, glyphs)
			return cache.output
		}
	}

	var b strings.Builder
	styles := logStylesFor(width, CurrentTheme)
	logs.Iterate(func(entry core.LogEntry) {
		b.WriteString(renderLogEntry(entry, styles))
		b.WriteString(logEntryNewline(entry))
	})
	cache.output = b.String()
	updateLogRenderCache(cache, logs, width, glyphs)
	return cache.output
}

type logRenderStyles struct {
	base, success, adb, fastboot, action, done lipgloss.Style
}

type logRenderStyleKey struct {
	width int
	theme Theme
}

var logRenderStylesCache = struct {
	sync.Mutex
	styles map[logRenderStyleKey]logRenderStyles
}{styles: make(map[logRenderStyleKey]logRenderStyles)}

func logStylesFor(width int, theme Theme) logRenderStyles {
	key := logRenderStyleKey{width: width, theme: theme}
	logRenderStylesCache.Lock()
	defer logRenderStylesCache.Unlock()
	if styles, ok := logRenderStylesCache.styles[key]; ok {
		return styles
	}
	styles := logRenderStyles{
		base:     lipgloss.NewStyle().Foreground(theme.Foreground).Width(width).PaddingRight(1),
		success:  lipgloss.NewStyle().Foreground(theme.Success).Bold(true),
		adb:      lipgloss.NewStyle().Foreground(theme.Highlight).Bold(true),
		fastboot: lipgloss.NewStyle().Foreground(theme.Accent).Bold(true),
		action:   lipgloss.NewStyle().Foreground(theme.Warning).Bold(true),
		done:     lipgloss.NewStyle().Foreground(theme.Success).Bold(true),
	}
	logRenderStylesCache.styles[key] = styles
	return styles
}

func renderLogEntry(entry core.LogEntry, styles logRenderStyles) string {
	if entry.Level == core.LogError {
		return ""
	}
	style := styles.base
	text := entry.Text
	switch entry.Level {
	case core.LogSuccess:
		style = styles.base.Inherit(styles.success)
	}
	if strings.HasPrefix(text, ">") {
		cmdPart := text
		if strings.Contains(text, "adb") {
			cmdPart = strings.Replace(text, "adb", styles.adb.Render("adb"), 1)
		} else if strings.Contains(text, "fastboot") {
			cmdPart = strings.Replace(text, "fastboot", styles.fastboot.Render("fastboot"), 1)
		}
		for _, action := range []string{"flash", "sideload", "wipe-super", "reboot"} {
			if strings.Contains(cmdPart, action) {
				cmdPart = strings.Replace(cmdPart, action, styles.action.Render(action), 1)
			}
		}
		text = cmdPart
	}
	if strings.Contains(text, "[ DONE ]") {
		text = strings.Replace(text, "[ DONE ]", styles.done.Render("[ DONE ]"), 1)
	}
	return style.Render(text)
}

func logEntryNewline(entry core.LogEntry) string {
	if entry.Level == core.LogError {
		return ""
	}
	return "\n"
}

func glyphSignature(glyphs Glyphs) string {
	return strings.Join([]string{
		glyphs.Marker, glyphs.BarFilled, glyphs.BarEmpty, strings.Join(glyphs.Sparkline, "\x00"),
		glyphs.OK, glyphs.Fail, glyphs.Optional, glyphs.Dot, glyphs.HollowDot,
		strings.Join(glyphs.Spinner, "\x00"), glyphs.Ellipsis, glyphs.Danger,
		glyphs.Horizontal, glyphs.Vertical, glyphs.TopLeft, glyphs.TopRight,
		glyphs.BottomLeft, glyphs.BottomRight,
	}, "\x00")
}

func updateLogRenderCache(cache *logRenderCache, logs *LogBuffer, width int, glyphs string) {
	cache.width = width
	cache.theme = CurrentTheme
	cache.glyphs = glyphs
	cache.revision = logs.revision
	cache.head = logs.head
	cache.count = logs.count
	cache.size = logs.size
	cache.initialized = true
}

func LoadFiles(dir, filter string) ([]FileItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	items := make([]FileItem, 0, len(entries)+2)

	if filter == "" {
		items = append(items, FileItem{Name: "[ SELECT THIS FOLDER ]", Path: dir, IsDir: true})
	}

	if parent := filepath.Dir(dir); parent != dir {
		items = append(items, FileItem{Name: "..", Path: parent, IsDir: true})
	}

	filter = strings.ToLower(filter)
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && filter != "" && filter != "*" && !strings.HasSuffix(strings.ToLower(name), filter) {
			continue
		}

		items = append(items, FileItem{
			Name:  name,
			Path:  filepath.Join(dir, name),
			IsDir: e.IsDir(),
		})
	}
	return items, nil
}
