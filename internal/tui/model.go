package tui

import (
	"flasher/internal/config"
	"flasher/internal/core"
	"flasher/internal/platform"

	"math"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

/* TYPES */

type ModalType int

const (
	ModalNone ModalType = iota
	ModalFile
	ModalSHA256
	ModalConfirm
	ModalHelp
	ModalCustom
	ModalSettings
)

type Screen int

const (
	ScreenHome Screen = iota
	ScreenDashboard
)

type LogBuffer struct {
	data       []core.LogEntry
	size       int
	head       int
	count      int
	revision   uint64
	lastChange logBufferChange
	render     logRenderCache
}

type logBufferChange uint8

const (
	logBufferUnchanged logBufferChange = iota
	logBufferAppend
	logBufferWrap
	logBufferReplace
)

type logRenderCache struct {
	width       int
	theme       Theme
	glyphs      string
	revision    uint64
	head        int
	count       int
	size        int
	output      string
	initialized bool
}

func NewLogBuffer(size int) *LogBuffer {
	return &LogBuffer{
		data: make([]core.LogEntry, size),
		size: size,
	}
}

func (lb *LogBuffer) Add(entry core.LogEntry) {
	if lb.count < lb.size {
		lb.data[lb.count] = entry
		lb.count++
		lb.lastChange = logBufferAppend
	} else {
		lb.data[lb.head] = entry
		lb.head = (lb.head + 1) % lb.size
		lb.lastChange = logBufferWrap
	}
	lb.revision++
}

func (lb *LogBuffer) Len() int {
	return lb.count
}

func (lb *LogBuffer) Iterate(fn func(core.LogEntry)) {
	for i := 0; i < lb.count; i++ {
		idx := (lb.head + i) % lb.size
		fn(lb.data[idx])
	}
}

func (lb *LogBuffer) ReplaceLast(entry core.LogEntry) {
	if lb.count == 0 {
		lb.Add(entry)
		return
	}
	idx := (lb.head + lb.count - 1) % lb.size
	lb.data[idx] = entry
	lb.revision++
	lb.lastChange = logBufferReplace
}

func (lb *LogBuffer) Last() (core.LogEntry, bool) {
	if lb.count == 0 {
		return core.LogEntry{}, false
	}
	idx := (lb.head + lb.count - 1) % lb.size
	return lb.data[idx], true
}

/* DATA MODELS */

type MenuItem struct {
	Label   string
	Icon    string
	Desc    string
	Action  string
	Section string
	Danger  bool
}

type FileItem struct {
	Name  string
	Path  string
	IsDir bool
}

type Toast struct {
	Message string
	Type    core.LogLevel
	Warning bool
}

/* APP MODEL */

type AppModel struct {
	// Core State
	Width, Height              int
	Selection                  int
	PaneDetails                bool
	Menu                       []MenuItem
	Screen                     Screen
	State                      core.FlashState
	ActiveModal                ModalType
	Tick                       int // Pulsing animation tick
	DashGen                    int
	DashFocus                  int
	DashCandidateSelection     int
	DashSidebar                bool
	DashScroll                 int
	DashFilter                 textinput.Model
	DashFilterOn               bool
	Dashboard                  platform.Dashboard
	ProgressPct                float64
	ProgressActive             bool
	SelectedFlashPath          string
	SelectedFlashFingerprint   string
	SelectedFingerprintRequest uint64
	SelectedFlashSHA256        string
	SelectedFlashCompatibility string

	App *config.App

	// Device State
	Device                       platform.DeviceState
	DeviceReconciliationRequired bool
	ReconciliationTargetSerial   string

	// UI Components
	UI struct {
		Viewport  viewport.Model
		Progress  progress.Model
		TextInput textinput.Model
	}

	// Modal Data
	Modal struct {
		ConfirmMsg   string
		OnConfirm    func() tea.Cmd
		ConfirmWord  string
		ConfirmTyped string

		FileDir      string
		FileList     []FileItem
		FullFileList []FileItem
		FileCursor   int
		FileTitle    string
		FileFilter   string
		OnFileSelect func(string, string) tea.Cmd
		SHA256Path   string

		CustomLogs     *LogBuffer
		CustomViewport viewport.Model
		Width          int

		SettingsCursor int
		SettingsInputs []textinput.Model
		ReturnToCustom bool
	}

	// Logs
	Logs          *LogBuffer
	ActiveToast   *Toast
	LogsDirty     bool
	LastErrorLine string

	// Config
	BaseDir    string
	DevicePath string

	IsInitialized bool
	IsRefreshing  bool
}

/* FACTORY */

func NewModel(app *config.App) AppModel {
	m := AppModel{
		App:           app,
		BaseDir:       app.Config.BaseDir,
		DevicePath:    app.Config.DevicePath,
		IsInitialized: false,
		Dashboard:     platform.Dashboard{CPUPct: math.NaN()},
		Device: platform.DeviceState{
			Mode:       platform.ModeDisconnected,
			Serial:     "-",
			Model:      "-",
			Battery:    "-",
			Slot:       "-",
			Secure:     "-",
			BatteryPct: -1,
			BatteryMV:  -1,
		},
		Logs: NewLogBuffer(500),
	}
	m.Logs.Add(core.LogEntry{Level: core.LogInfo, Text: "SYSTEM INITIALIZED. READY.", Timestamp: time.Now()})
	m.Modal.CustomLogs = NewLogBuffer(500)

	m.Menu = GetDefaultMenu()
	m.SetupUI()
	if app.Config.ASCII {
		CurrentGlyphs = ASCIIGlyphs
	} else {
		CurrentGlyphs = UnicodeGlyphs
	}
	m.DashFilter = textinput.New()
	m.DashFilter.Placeholder = "Filter this panel..."
	m.DashFilter.CharLimit = 128
	m.DashFilter.Width = 32
	m.UI.Viewport = viewport.New(0, 0)
	m.UI.Viewport.SetContent(RenderLogsStr(m.Logs, 0))
	app.Engine.SetSink(func(_ core.LogLevel, line string) {
		select {
		case app.Engine.LogChan <- line:
		default:
			app.Logger.Warn("Dropping log because the TUI log queue is full")
		}
	})
	return m
}
