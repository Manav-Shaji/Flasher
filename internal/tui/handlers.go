package tui

import (
	"flasher/internal/config"
	"flasher/internal/core"
	"flasher/internal/platform"

	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

/* MODAL HANDLER */

func (m AppModel) startSetupFlow() (AppModel, tea.Cmd) {
	m.ActiveModal = ModalFile
	m.Modal.FileTitle = "SELECT MAIN CUSTOM ROMS DIR"
	m.Modal.FileFilter = ""

	m.Modal.FileDir = m.App.Config.BaseDir
	if _, err := os.Stat(m.Modal.FileDir); err != nil {
		m.Modal.FileDir, _ = os.Getwd()
	}

	loadCmd := m.loadModalFiles("")
	m.Modal.OnFileSelect = func(path, _ string) tea.Cmd {
		return func() tea.Msg { return SetupMainDirMsg(path) }
	}
	return m, tea.Batch(loadCmd, m.UI.TextInput.Focus())
}

func (m *AppModel) loadModalFiles(filter string) tea.Cmd {
	items, err := LoadFiles(m.Modal.FileDir, filter)
	m.Modal.FullFileList = items
	m.Modal.FileList = items
	if err != nil {
		return m.showError("File picker read failed: " + err.Error())
	}
	return nil
}

func updateModal(m AppModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" && m.IsInitialized {
		if m.ActiveModal == ModalConfirm {
			return finishConfirmation(m, false)
		}
		m.ActiveModal = ModalNone
		m.UI.TextInput.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	switch m.ActiveModal {
	case ModalFile:
		oldVal := m.UI.TextInput.Value()
		newTi, tiCmd := m.UI.TextInput.Update(msg)
		m.UI.TextInput = newTi
		cmd = tiCmd

		if m.UI.TextInput.Value() != oldVal {
			return m, tea.Batch(cmd, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
				return SearchDebounceMsg{Query: m.UI.TextInput.Value()}
			}))
		}
	case ModalCustom:
		if m.State == core.StateIdle {
			newTi, tiCmd := m.UI.TextInput.Update(msg)
			m.UI.TextInput = newTi
			cmd = tiCmd
		}
	case ModalSHA256:
		newTi, tiCmd := m.UI.TextInput.Update(msg)
		m.UI.TextInput = newTi
		cmd = tiCmd
	}

	switch m.ActiveModal {
	case ModalConfirm:
		if m.Modal.ConfirmWord != "" {
			switch strings.ToLower(msg.String()) {
			case "esc":
				return finishConfirmation(m, false)
			case "backspace":
				typed := []rune(m.Modal.ConfirmTyped)
				if len(typed) > 0 {
					m.Modal.ConfirmTyped = string(typed[:len(typed)-1])
				}
				return m, nil
			case "enter":
				if m.Modal.ConfirmTyped != m.Modal.ConfirmWord {
					return m, nil
				}
				return finishConfirmation(m, true)
			default:
				key := msg.String()
				if len(key) == 1 && key[0] >= 32 && key[0] <= 126 && len(m.Modal.ConfirmTyped) < len(m.Modal.ConfirmWord) {
					m.Modal.ConfirmTyped += strings.ToUpper(key)
				}
				return m, nil
			}
		}
		switch strings.ToLower(msg.String()) {
		case "y", "enter":
			return finishConfirmation(m, true)
		case "n", "esc":
			return finishConfirmation(m, false)
		}

	case ModalSHA256:
		switch msg.String() {
		case "enter":
			expected := strings.TrimSpace(m.UI.TextInput.Value())
			if expected != "" {
				if len(expected) != 64 {
					m.ActiveToast = &Toast{Message: "Expected SHA-256 must contain 64 hexadecimal characters.", Type: core.LogError}
					return m, nil
				}
				if _, err := hex.DecodeString(expected); err != nil {
					m.ActiveToast = &Toast{Message: "Expected SHA-256 must contain only hexadecimal characters.", Type: core.LogError}
					return m, nil
				}
			}
			path := m.Modal.SHA256Path
			m.ActiveModal = ModalNone
			m.UI.TextInput.Blur()
			m.beginOperation()
			m.ActiveToast = &Toast{Message: "Calculating SHA-256...", Type: core.LogInfo}
			m.LastErrorLine = ""
			return m, m.App.Engine.ExecuteAsync(func(ctx context.Context) error {
				actual, err := core.CalculateInputFingerprint(path)
				if err != nil {
					return fmt.Errorf("calculate SHA-256: %w", err)
				}
				status := "NOT CHECKED (no reference supplied)"
				level := core.LogInfo
				if expected != "" {
					if strings.EqualFold(actual, expected) {
						status = "MATCH"
						level = core.LogSuccess
					} else {
						status = "MISMATCH"
					}
				}
				m.App.Engine.Emit(level, fmt.Sprintf("SHA-256 result for %s | Path: %s | SHA-256: %s | %s",
					filepath.Base(path), path, actual, status))
				return nil
			})
		}

	case ModalFile:
		switch msg.String() {
		case "up":
			if m.Modal.FileCursor > 0 {
				m.Modal.FileCursor--
			}
		case "down":
			if m.Modal.FileCursor < len(m.Modal.FileList)-1 {
				m.Modal.FileCursor++
			}
		case "enter":
			if len(m.Modal.FileList) == 0 {
				return m, cmd
			}
			sel := m.Modal.FileList[m.Modal.FileCursor]

			if sel.Name == "[ SELECT THIS FOLDER ]" {
				m.SelectedFingerprintRequest++
				m.ActiveModal = ModalNone
				m.UI.TextInput.Blur()
				if m.Modal.OnFileSelect != nil {
					return m, m.Modal.OnFileSelect(m.Modal.FileDir, "")
				}
				return m, nil
			}

			if sel.IsDir {
				m.Modal.FileDir = sel.Path
				m.Modal.FileCursor = 0
				m.UI.TextInput.Reset()
				loadCmd := m.loadModalFiles(m.Modal.FileFilter)
				return m, tea.Batch(cmd, loadCmd)
			}
			m.ActiveModal = ModalNone
			m.UI.TextInput.Blur()
			if m.Modal.OnFileSelect != nil {
				if m.Modal.FileFilter == "*" {
					return m, tea.Batch(cmd, m.Modal.OnFileSelect(sel.Path, ""))
				}
				m.SelectedFlashPath = sel.Path
				m.SelectedFingerprintRequest++
				requestID := m.SelectedFingerprintRequest
				m.SelectedFlashFingerprint = ""
				m.SelectedFlashSHA256 = "NOT CHECKED"
				m.SelectedFlashCompatibility = "PENDING: evaluating package and device evidence"
				var evaluatorEngine *core.Engine
				if m.App != nil {
					evaluatorEngine = m.App.Engine
				}
				commands := []tea.Cmd{cmd}
				if m.Modal.FileFilter != ".zip" {
					commands = append(commands,
						calculateSelectedInputFingerprint(requestID, sel.Path, m.Modal.OnFileSelect),
						inspectPackageCompatibility(sel.Path, m.Device, evaluatorEngine))
				} else {
					commands = append(commands, m.Modal.OnFileSelect(sel.Path, ""))
				}
				return m, tea.Batch(commands...)
			}
		}

	case ModalCustom:
		if msg.String() == "enter" && m.State == core.StateIdle {
			val := m.UI.TextInput.Value()
			if val != "" {
				bin, args, display, err := core.ParseCustomCommandForMode(val, m.Device.Mode)
				if err != nil {
					return m, m.showError(err.Error())
				}
				risk, reason := core.ClassifyCommand(bin, args)
				if risk == core.RiskBlocked {
					return m, m.showError(reason)
				}
				if bin == "fastboot" && core.IsAllowedCustomFastbootCommand(args) {
					if m.DeviceReconciliationRequired {
						return m, m.showError("A fresh scan of the same device is required before running this command.")
					}
					m.Modal.CustomLogs.Add(core.LogEntry{
						Level: core.LogInfo, Text: "[CORE VALIDATED] Executing: " + display + "...",
						Timestamp: time.Now(),
					})
					m.Modal.CustomViewport.SetContent(RenderLogsStr(m.Modal.CustomLogs, max(20, m.Width-10)))
					m.Modal.CustomViewport.GotoBottom()
					m.UI.TextInput.Reset()
					m.beginOperation()
					m.LastErrorLine = ""
					m.ProgressPct = 0
					m.ProgressActive = false
					target := m.Device
					engine := m.App.Engine
					return m, engine.ExecuteAsync(func(ctx context.Context) error {
						state, err := freshDeviceState(target)
						if err != nil {
							return err
						}
						if err := ensureSameDevice(target, state); err != nil {
							return err
						}
						return engine.RunCustomFastbootCommand(ctx, state.Serial, args)
					})
				}
				if recovery, ok := customRebootTarget(args); ok {
					if m.DeviceReconciliationRequired {
						return m, m.showError("A fresh scan of the same device is required before rebooting.")
					}
					m.Modal.CustomLogs.Add(core.LogEntry{
						Level: core.LogInfo, Text: "[CORE VALIDATED] Executing: " + display + "...",
						Timestamp: time.Now(),
					})
					m.Modal.CustomViewport.SetContent(RenderLogsStr(m.Modal.CustomLogs, max(20, m.Width-10)))
					m.Modal.CustomViewport.GotoBottom()
					m.UI.TextInput.Reset()
					m.beginOperation()
					m.LastErrorLine = ""
					m.ProgressPct = 0
					m.ProgressActive = false
					target := m.Device
					return m, m.App.Engine.ExecuteAsync(func(ctx context.Context) error {
						state, err := freshDeviceState(target)
						if err != nil {
							return err
						}
						if err := ensureSameDevice(target, state); err != nil {
							return err
						}
						if recovery {
							return m.App.Engine.DeviceService.RebootRecoveryWithAcknowledgement(ctx, state, true)
						}
						return m.App.Engine.DeviceService.RebootSystemWithAcknowledgement(ctx, state, true)
					})
				}
				if risk != core.RiskSafe {
					return m, m.showError("Custom commands are limited to recognized read-only operations: " + reason)
				}
				m.Modal.CustomLogs.Add(core.LogEntry{Level: core.LogInfo, Text: "[SAFE] Executing: " + display + "...", Timestamp: time.Now()})
				m.Modal.CustomViewport.SetContent(RenderLogsStr(m.Modal.CustomLogs, max(20, m.Width-10)))
				m.Modal.CustomViewport.GotoBottom()
				m.UI.TextInput.Reset()
				m.beginOperation()
				m.LastErrorLine = ""
				m.ProgressPct = 0
				m.ProgressActive = false
				return m, m.App.Engine.ExecuteAsync(func(ctx context.Context) error {
					return m.App.Engine.RunCustomCommand(ctx, m.Device.Mode, m.Device.Serial, val)
				})
			}
		}

	case ModalHelp:
		m.ActiveModal = ModalNone

	case ModalSettings:
		switch msg.String() {
		case "up", "k", "shift+tab":
			if m.Modal.SettingsCursor > 0 {
				m.Modal.SettingsCursor--
			}
		case "down", "j", "tab":
			if m.Modal.SettingsCursor < 2 {
				m.Modal.SettingsCursor++
			}
		case "enter":
			switch m.Modal.SettingsCursor {
			case 0:
				m.ActiveModal = ModalFile
				m.Modal.FileTitle = "SELECT BASE ROM DIRECTORY"
				m.Modal.FileDir = m.App.Config.BaseDir
				if _, err := os.Stat(m.Modal.FileDir); err != nil {
					m.Modal.FileDir, _ = os.Getwd()
				}
				loadCmd := m.loadModalFiles("")
				m.Modal.FileCursor = 0
				m.UI.TextInput.Reset()
				m.Modal.OnFileSelect = func(path, _ string) tea.Cmd {
					return func() tea.Msg { return SettingsFolderSelectedMsg{Index: 0, Path: path} }
				}
				return m, tea.Batch(loadCmd, m.UI.TextInput.Focus())
			case 1:
				m.ActiveModal = ModalFile
				m.Modal.FileTitle = "SELECT DEVICE FOLDER"
				m.Modal.FileDir = m.App.Config.DevicePath
				if m.Modal.FileDir == "" {
					m.Modal.FileDir = m.App.Config.BaseDir
				}
				if _, err := os.Stat(m.Modal.FileDir); err != nil {
					m.Modal.FileDir = m.App.Config.BaseDir
					if _, err := os.Stat(m.Modal.FileDir); err != nil {
						m.Modal.FileDir, _ = os.Getwd()
					}
				}
				loadCmd := m.loadModalFiles("")
				m.Modal.FileCursor = 0
				m.UI.TextInput.Reset()
				m.Modal.OnFileSelect = func(path, _ string) tea.Cmd {
					return func() tea.Msg { return SettingsFolderSelectedMsg{Index: 1, Path: path} }
				}
				return m, tea.Batch(loadCmd, m.UI.TextInput.Focus())
			case 2:
				err := config.SaveConfig(m.App.Config)
				m.BaseDir = m.App.Config.BaseDir
				m.DevicePath = m.App.Config.DevicePath

				var cmd tea.Cmd
				if err != nil {
					cmd = m.showError("Save Failed: " + err.Error())
				} else {
					m.ActiveToast = &Toast{Message: "Settings Saved", Type: core.LogSuccess}
					cmd = toastTimeoutCmd(3 * time.Second)
				}
				m.ActiveModal = ModalNone
				return m, cmd
			}
		}
	}

	return m, cmd
}

func customRebootTarget(args []string) (recovery bool, ok bool) {
	if len(args) == 1 && args[0] == "reboot" {
		return false, true
	}
	if len(args) == 2 && args[0] == "reboot" && args[1] == "system" {
		return false, true
	}
	if len(args) == 2 && args[0] == "reboot" && args[1] == "recovery" {
		return true, true
	}
	return false, false
}

func finishConfirmation(m AppModel, accepted bool) (tea.Model, tea.Cmd) {
	returnToCustom := m.Modal.ReturnToCustom
	m.Modal.ReturnToCustom = false
	m.Modal.ConfirmWord = ""
	m.Modal.ConfirmTyped = ""
	m.ActiveModal = ModalNone
	if returnToCustom {
		m.ActiveModal = ModalCustom
	}

	if !accepted {
		if returnToCustom {
			return m, m.UI.TextInput.Focus()
		}
		m.UI.TextInput.Blur()
		return m, nil
	}

	m.beginOperation()
	m.LastErrorLine = ""
	m.ProgressPct = 0
	m.ProgressActive = false
	m.UI.TextInput.Blur()
	if m.Modal.OnConfirm != nil {
		return m, m.Modal.OnConfirm()
	}
	return m, nil
}

/* UPDATE HANDLERS */

func (m AppModel) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.Width, m.Height = msg.Width, msg.Height
	m.syncViewportLayout()
	if !m.IsInitialized && m.ActiveModal == ModalNone {
		return m.startSetupFlow()
	}
	return m, nil
}

func (m AppModel) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ActiveToast != nil && m.ActiveToast.Type == core.LogError {
		m.ActiveToast = nil
	}
	if m.State != core.StateIdle {
		if msg.String() == "tab" && singlePane(m.Width) && m.ActiveModal == ModalNone {
			m.PaneDetails = !m.PaneDetails
			m.syncViewportLayout()
			return m, nil
		}
		if msg.String() == "ctrl+c" || msg.String() == "esc" {
			m.App.Engine.CancelActiveCommand()
			return m, nil
		}
		return m, nil
	}

	if m.ActiveModal != ModalNone {
		return updateModal(m, msg)
	}

	if m.Screen == ScreenDashboard {
		if m.Device.Mode == platform.ModeAmbiguous {
			if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
				index := int(msg.String()[0] - '1')
				if index < len(m.Device.Candidates) {
					candidate := m.Device.Candidates[index]
					m.Dashboard = platform.Dashboard{Loading: true, CPUPct: math.NaN()}
					m.DashGen++
					return m, platform.SelectDeviceCmd(candidate, m.DashGen)
				}
				return m, nil
			}
			switch msg.String() {
			case "up", "w":
				m.DashCandidateSelection = (m.DashCandidateSelection - 1 + len(m.Device.Candidates)) % max(1, len(m.Device.Candidates))
				return m, nil
			case "down", "s":
				m.DashCandidateSelection = (m.DashCandidateSelection + 1) % max(1, len(m.Device.Candidates))
				return m, nil
			case "enter":
				if m.DashCandidateSelection >= 0 && m.DashCandidateSelection < len(m.Device.Candidates) {
					candidate := m.Device.Candidates[m.DashCandidateSelection]
					m.Dashboard = platform.Dashboard{Loading: true, CPUPct: math.NaN()}
					m.DashGen++
					return m, platform.SelectDeviceCmd(candidate, m.DashGen)
				}
			}
		}
		switch msg.String() {
		case "esc":
			if m.DashFilterOn {
				m.DashFilter.Reset()
				m.DashFilterOn = false
				m.DashScroll = 0
				return m, nil
			}
			m.DashGen++
			m.Screen = ScreenHome
			return m, nil
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.DashGen++
			m.Dashboard.Loading = true
			m.Dashboard.Err = ""
			return m, tea.Batch(
				platform.CheckDeviceStateCmd(m.DashGen, true),
				platform.CollectDashboardCmd(m.Device.Serial, m.Device.Mode, true, m.DashGen),
			)
		case "tab":
			if m.DashSidebar {
				m.DashSidebar = false
				m.DashFocus = 0
			} else if m.DashFocus+1 >= dashboardPanelCount(m.Device.Mode) {
				m.DashSidebar = true
			} else {
				m.DashFocus++
			}
			m.DashScroll = 0
			return m, nil
		case "shift+tab":
			if m.DashSidebar {
				m.DashSidebar = false
				m.DashFocus = dashboardPanelCount(m.Device.Mode) - 1
			} else if m.DashFocus == 0 {
				m.DashSidebar = true
			} else {
				m.DashFocus--
			}
			m.DashScroll = 0
			return m, nil
		case "left":
			m.DashSidebar = true
			m.DashScroll = 0
			return m, nil
		case "right":
			m.DashSidebar = false
			m.DashScroll = 0
			return m, nil
		case "up", "w":
			if len(m.Menu) == 0 {
				return m, nil
			}
			m.DashSidebar = true
			m.Selection = (m.Selection - 1 + len(m.Menu)) % len(m.Menu)
			return m, nil
		case "down", "s":
			if len(m.Menu) == 0 {
				return m, nil
			}
			m.DashSidebar = true
			m.Selection = (m.Selection + 1) % len(m.Menu)
			return m, nil
		case "pgup":
			m.DashScroll = max(0, m.DashScroll-max(1, m.Height/2))
			return m, nil
		case "pgdown":
			m.DashScroll += max(1, m.Height/2)
			return m, nil
		case "/":
			m.DashSidebar = false
			m.DashFilterOn = true
			return m, m.DashFilter.Focus()
		case "enter":
			if m.DashSidebar && m.Selection >= 0 && m.Selection < len(m.Menu) {
				return handleMenuSelect(m)
			}
		}
		if m.DashFilterOn {
			updated, cmd := m.DashFilter.Update(msg)
			m.DashFilter = updated
			m.DashScroll = 0
			return m, cmd
		}
		return m, nil
	}

	if singlePane(m.Width) {
		switch msg.String() {
		case "tab":
			m.PaneDetails = !m.PaneDetails
			m.syncViewportLayout()
			return m, nil
		case "esc":
			if m.PaneDetails {
				m.PaneDetails = false
				m.syncViewportLayout()
				return m, nil
			}
		}
	}

	switch msg.String() {
	case "up", "w":
		m.Selection = (m.Selection - 1 + len(m.Menu)) % len(m.Menu)
	case "down", "s":
		m.Selection = (m.Selection + 1) % len(m.Menu)
	case "enter":
		return handleMenuSelect(m)
	case "d":
		return openDashboard(m)
	case "r":
		return selectMenuAction(m, "refresh")
	case "q", "ctrl+c":
		return m, tea.Quit
	case "h", "?":
		m.ActiveModal = ModalHelp
	}
	return m, nil
}

func (m *AppModel) beginOperation() {
	m.State = core.StatePreparing
	if singlePane(m.Width) {
		m.PaneDetails = true
	}
	m.syncViewportLayout()
}

func openDashboard(m AppModel) (tea.Model, tea.Cmd) {
	m.Screen = ScreenDashboard
	m.DashGen++
	m.DashFocus = 0
	m.DashSidebar = true
	m.DashScroll = 0
	m.Dashboard.Loading = true
	m.Dashboard.Err = ""
	return m, platform.CollectDashboardCmd(m.Device.Serial, m.Device.Mode, true, m.DashGen)
}

func selectMenuAction(m AppModel, action string) (tea.Model, tea.Cmd) {
	for i, item := range m.Menu {
		if item.Action == action {
			m.Selection = i
			return handleMenuSelect(m)
		}
	}
	return m, nil
}

func dashboardPanelCount(mode platform.DeviceMode) int {
	if mode == platform.ModeFastboot {
		return 3
	}
	return 4
}

func (m AppModel) itemAvailable(it MenuItem) (bool, string) {
	reason := m.itemUnavailableReason(it)
	return reason == "", reason
}

func (m AppModel) itemUnavailableReason(it MenuItem) string {
	if m.DeviceReconciliationRequired && isDeviceChangingAction(it.Action) {
		return "previous device-changing operation was interrupted; fresh scan of the same device is required"
	}
	switch it.Action {
	case "flash_rec", "flash_boot", "wipe_super":
		if m.Device.Mode != platform.ModeFastboot {
			return "needs fastboot mode"
		}
		if ok, reason := m.canFlash(false); !ok {
			return reason
		}
	case "sideload":
		if m.Device.Mode != platform.ModeRecovery && m.Device.Mode != platform.ModeSideload && m.Device.Mode != platform.ModeDevice {
			return "needs recovery or sideload mode"
		}
	case "rb_system", "rb_recovery":
		if m.Device.Mode == platform.ModeDisconnected || m.Device.Mode == platform.ModeOffline ||
			m.Device.Mode == platform.ModeUnauthorized || m.Device.Mode == platform.ModeAmbiguous {
			return "no device connected"
		}
	}
	return ""
}

func isDeviceChangingAction(action string) bool {
	switch action {
	case "flash_rec", "flash_boot", "wipe_super", "sideload", "rb_system", "rb_recovery":
		return true
	default:
		return false
	}
}

func (m *AppModel) reconcileInterruptedOperation(device platform.DeviceState) {
	if !m.DeviceReconciliationRequired ||
		device.Serial != m.ReconciliationTargetSerial ||
		device.Serial == "" || device.Serial == "-" ||
		device.Mode == platform.ModeAmbiguous ||
		device.Mode == platform.ModeDisconnected ||
		device.Mode == platform.ModeOffline {
		return
	}
	m.DeviceReconciliationRequired = false
	m.ReconciliationTargetSerial = ""
	m.ActiveToast = &Toast{
		Message: "Device state re-read; prior transfer outcome remains unknown.",
		Type:    core.LogInfo,
		Warning: true,
	}
}

func (m AppModel) handlePollMsg(_ platform.PollMsg) (tea.Model, tea.Cmd) {
	if m.State != core.StateIdle {
		return m, tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg { return platform.PollMsg(t) })
	}
	return m, tea.Batch(platform.CheckDeviceStateCmd(m.DashGen, false), platform.PollTickCmd())
}

func (m AppModel) handleDeviceUpdate(msg platform.DeviceUpdateMsg) (tea.Model, tea.Cmd) {
	newDevice := platform.DeviceState(msg)
	newMode := newDevice.Mode
	oldMode, oldSerial := m.Device.Mode, m.Device.Serial
	oldCodename := m.Device.Codename
	oldCandidates := m.Device.Candidates
	var cmds []tea.Cmd
	if newMode == platform.ModeAmbiguous && oldMode != platform.ModeAmbiguous &&
		containsDeviceCandidate(newDevice.Candidates, m.Device) {
		newDevice = m.Device
		newMode = newDevice.Mode
	}
	if newMode == platform.ModeSideload && newDevice.Serial == oldSerial {
		newDevice = retainSideloadIdentity(m.Device, newDevice)
	}

	if m.IsRefreshing {
		m.IsRefreshing = false
		displayMode := formatDeviceMode(newMode)

		if newMode == platform.ModeDisconnected {
			m.ActiveToast = &Toast{Message: "Scan Finished: NO DEVICE", Type: core.LogInfo}
		} else {
			m.ActiveToast = &Toast{Message: fmt.Sprintf("Scan Complete: %s", displayMode), Type: core.LogSuccess}
		}
		cmds = append(cmds, toastTimeoutCmd(3*time.Second))
	} else if m.Device.Mode != newMode {
		m.ActiveToast = &Toast{Message: fmt.Sprintf("Mode: %s", newMode), Type: core.LogInfo}
		cmds = append(cmds, toastTimeoutCmd(3*time.Second))
	}
	m.Device = newDevice
	deviceChanged := oldMode != newMode || oldSerial != newDevice.Serial || oldCodename != newDevice.Codename ||
		(newMode == platform.ModeAmbiguous && !sameDeviceCandidates(oldCandidates, newDevice.Candidates))
	if deviceChanged && newMode == platform.ModeAmbiguous {
		m.DashCandidateSelection = 0
	}
	if deviceChanged {
		m.DashGen++
		m.Dashboard = platform.Dashboard{CPUPct: math.NaN()}
		m.SelectedFlashCompatibility = "PENDING: evaluating package and device evidence"
		if m.SelectedFlashPath != "" {
			var evaluatorEngine *core.Engine
			if m.App != nil {
				evaluatorEngine = m.App.Engine
			}
			cmds = append(cmds, inspectPackageCompatibility(m.SelectedFlashPath, m.Device, evaluatorEngine))
		}
	}
	if m.Screen == ScreenDashboard && deviceChanged {
		m.Dashboard = platform.Dashboard{Loading: true, CPUPct: math.NaN()}
		cmds = append(cmds, platform.CollectDashboardCmd(m.Device.Serial, m.Device.Mode, true, m.DashGen))
	}
	return m, tea.Batch(cmds...)
}

func containsDeviceCandidate(candidates []platform.DeviceState, target platform.DeviceState) bool {
	for _, candidate := range candidates {
		if candidate.Mode == target.Mode && candidate.Serial == target.Serial {
			return true
		}
	}
	return false
}

func sameDeviceCandidates(a, b []platform.DeviceState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Mode != b[i].Mode || a[i].Serial != b[i].Serial {
			return false
		}
	}
	return true
}

func (m AppModel) handleLogMsg(msg core.LogMsg) (tea.Model, tea.Cmd) {
	line := string(msg)

	// Process Standard Logs
	isOverwrite := strings.HasPrefix(line, "\r")

	cleanLine := strings.TrimPrefix(line, "\r")
	if cleanLine == "" {
		return m, m.App.Engine.WaitForLogs()
	}

	level := parseLogLevel(cleanLine)
	if m.State != core.StateIdle && level == core.LogError {
		m.LastErrorLine = cleanLine
	}
	entry := core.LogEntry{Level: level, Text: cleanLine, Timestamp: time.Now()}
	var progressCmd tea.Cmd
	if pct, ok := parseProgress(cleanLine); ok {
		m.ProgressPct = pct
		m.ProgressActive = true
		progressCmd = m.UI.Progress.SetPercent(pct / 100)
	}

	if m.ActiveModal != ModalCustom && !shouldShowLiveLog(cleanLine) {
		return m, tea.Batch(progressCmd, m.App.Engine.WaitForLogs())
	}
	if m.ActiveModal == ModalCustom {
		m.syncCustomLogs(entry, isOverwrite)
	} else {
		m.syncMainLogs(entry, isOverwrite)
	}

	return m, tea.Batch(progressCmd, m.App.Engine.WaitForLogs())
}

func shouldShowLiveLog(line string) bool {
	trimmed := strings.TrimSpace(line)
	upper := strings.ToUpper(trimmed)
	if upper == "STARTING COMMAND EXECUTION..." ||
		upper == "STREAMERS ATTACHED. WAITING FOR OUTPUT..." ||
		strings.HasPrefix(upper, "SUCCESS: TASK FINISHED SUCCESSFULLY IN ") {
		return false
	}
	if isTransferProgress(trimmed) && parseLogLevel(trimmed) != core.LogError {
		return false
	}
	if commandEchoPattern.MatchString(trimmed) {
		return false
	}
	return true
}

func isTransferProgress(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "serving:") || strings.Contains(lower, "installing:") ||
		strings.Contains(lower, "(~")
}

func (m AppModel) handleTaskComplete(msg core.TaskCompleteMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.State = core.StateFailed
	} else {
		m.State = core.StateCompleted
	}

	// Reset to idle after a bit, handled by tick or manually, but for now just idle
	m.State = core.StateIdle
	m.ProgressActive = false

	var cmds []tea.Cmd
	if msg.Err != nil {
		indeterminate := errors.Is(msg.Err, core.ErrOperationIndeterminate)
		message := m.LastErrorLine
		if indeterminate {
			m.DeviceReconciliationRequired = true
			m.ReconciliationTargetSerial = m.Device.Serial
			message = "Device-changing operation ended without confirmed success. Device state is indeterminate; no rollback is implied. A fresh scan of the same device is required."
			m.DashGen++
			cmds = append(cmds, platform.CheckDeviceStateCmd(m.DashGen, true))
		} else if message == "" ||
			(!strings.Contains(strings.ToLower(message), "failed") &&
				!strings.Contains(strings.ToLower(message), "error")) {
			message = userMessage(msg.Err)
		}
		canceled := !indeterminate && userMessage(msg.Err) == "canceled"
		if canceled {
			message = "canceled by user"
		}
		cmd := m.showError(message)
		if canceled {
			m.ActiveToast.Warning = true
		}
		cmds = append(cmds, cmd)
	} else {
		m.ActiveToast = &Toast{Message: "Success", Type: core.LogSuccess}
		entry := core.LogEntry{Text: "[ DONE ]", Level: core.LogInfo, Timestamp: time.Now()}
		if m.ActiveModal == ModalCustom {
			m.Modal.CustomLogs.Add(entry)
		} else if m.Logs != nil {
			m.Logs.Add(entry)
		}
		cmds = append(cmds, toastTimeoutCmd(3*time.Second))
	}
	m.LogsDirty = true

	if m.ActiveModal == ModalCustom {
		cmds = append(cmds, m.UI.TextInput.Focus())
	}
	if m.Screen == ScreenDashboard {
		cmds = append(cmds, DashboardTickCmd(m.DashGen, m.Device.Mode))
	}
	return m, tea.Batch(cmds...)
}

func (m AppModel) handleDashboardMsg(msg platform.DashboardMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.DashGen || m.Screen != ScreenDashboard {
		return m, nil
	}
	previousErr := m.Dashboard.Err
	m.Dashboard = mergeDashboard(m.Dashboard, msg.Data)
	m.Dashboard.Err = msg.Data.Err
	var cmds []tea.Cmd
	if m.Dashboard.Err != "" && m.Dashboard.Err != previousErr {
		cmds = append(cmds, m.showError(m.Dashboard.Err))
	}
	if msg.Data.Device.Mode != "" {
		m.Device.Mode = msg.Data.Device.Mode
		if msg.Data.Device.Serial != "" {
			m.Device.Serial = msg.Data.Device.Serial
		}
		if msg.Data.Device.Manufacturer != "" {
			m.Device.Manufacturer = msg.Data.Device.Manufacturer
		}
		if msg.Data.Device.Transport != "" {
			m.Device.Transport = msg.Data.Device.Transport
		}
		if msg.Data.Device.BootCompleted != "" {
			m.Device.BootCompleted = msg.Data.Device.BootCompleted
		}
		if msg.Data.Device.Battery != "" {
			m.Device.Battery = msg.Data.Device.Battery
		}
		if msg.Data.Device.BatteryPct >= 0 {
			m.Device.BatteryPct = msg.Data.Device.BatteryPct
		}
		m.Device.BatteryInvalid = msg.Data.Device.BatteryInvalid
		if msg.Data.Device.BatteryMV > 0 {
			m.Device.BatteryMV = msg.Data.Device.BatteryMV
		}
		if !math.IsNaN(msg.Data.Device.TempC) && !math.IsInf(msg.Data.Device.TempC, 0) {
			m.Device.TempC = msg.Data.Device.TempC
		}
		if msg.Data.Device.Charging != platform.Unknown {
			m.Device.Charging = msg.Data.Device.Charging
		}
		if model := propValue(msg.Data.Props, "ro.product.model"); model != "-" {
			m.Device.Model = model
		}
		if msg.Data.Device.Codename != "" {
			m.Device.Codename = msg.Data.Device.Codename
		}
		if msg.Data.Device.Slot != "" {
			m.Device.Slot = msg.Data.Device.Slot
		}
		if msg.Data.Device.SlotCount > 0 {
			m.Device.SlotCount = msg.Data.Device.SlotCount
		}
		if msg.Data.Device.Unlocked != platform.Unknown {
			m.Device.Unlocked = msg.Data.Device.Unlocked
		}
		if msg.Data.Device.SecureBoot != "" {
			m.Device.SecureBoot = msg.Data.Device.SecureBoot
		}
		if msg.Data.Device.Secure != "" && msg.Data.Device.Mode != platform.ModeFastboot {
			m.Device.Secure = msg.Data.Device.Secure
		}
	}
	if m.Screen == ScreenDashboard && m.State == core.StateIdle {
		cmds = append(cmds, DashboardTickCmd(m.DashGen, m.Device.Mode))
	}
	return m, tea.Batch(cmds...)
}

func mergeDashboard(old, update platform.Dashboard) platform.Dashboard {
	if update.Loaded {
		old.Loaded = true
	}
	old.Loading = false
	if update.Partitions != nil {
		old.Partitions = update.Partitions
	}
	if update.Vars != nil {
		old.Vars = update.Vars
	}
	if update.Props != nil {
		old.Props = update.Props
	}
	if update.StorageUsed != 0 {
		old.StorageUsed = update.StorageUsed
	}
	if update.StorageTotal != 0 {
		old.StorageTotal = update.StorageTotal
	}
	if update.RAMUsedKB != 0 {
		old.RAMUsedKB = update.RAMUsedKB
	}
	if update.RAMTotalKB != 0 {
		old.RAMTotalKB = update.RAMTotalKB
	}
	if !math.IsNaN(update.CPUPct) {
		old.CPUPct = update.CPUPct
	}
	if update.UptimeSec != 0 {
		old.UptimeSec = update.UptimeSec
	}
	old.Samples.Battery = appendSample(old.Samples.Battery, update.Samples.Battery)
	old.Samples.Temp = appendSample(old.Samples.Temp, update.Samples.Temp)
	old.Samples.CPU = appendSample(old.Samples.CPU, update.Samples.CPU)
	if update.LastLatency != 0 {
		old.LastLatency = update.LastLatency
	}
	if !update.UpdatedAt.IsZero() {
		old.UpdatedAt = update.UpdatedAt
	}
	if update.Device.Mode != "" {
		old.Device = update.Device
	}
	if update.SELinuxState != "" {
		old.SELinuxState = update.SELinuxState
	}
	return old
}

func appendSample(old, update *platform.Samples) *platform.Samples {
	if update == nil || len(update.Slice()) == 0 {
		return old
	}
	values := old.Slice()
	values = append(values, update.Slice()...)
	if len(values) > 30 {
		values = values[len(values)-30:]
	}
	merged := platform.NewSamples(30)
	for _, value := range values {
		merged.Add(value)
	}
	return merged
}

func (m AppModel) handleSetupMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case SetupMainDirMsg:
		m.Modal.FileDir = string(msg)
		m.BaseDir = string(msg)
		m.App.Config.BaseDir = string(msg)
		m.ActiveModal = ModalFile
		m.Modal.FileTitle = "SELECT DEVICE FOLDER"
		m.Modal.FileCursor = 0
		m.UI.TextInput.Reset()
		loadCmd := m.loadModalFiles("")
		m.Modal.OnFileSelect = func(path, _ string) tea.Cmd {
			return func() tea.Msg { return SetupDeviceDirMsg(path) }
		}
		return m, tea.Batch(loadCmd, m.UI.TextInput.Focus())

	case SetupDeviceDirMsg:
		m.DevicePath = string(msg)
		m.App.Config.BaseDir = m.BaseDir
		m.App.Config.DevicePath = m.DevicePath
		if err := config.SaveConfig(m.App.Config); err != nil {
			return m, m.showError("Settings save failed: " + err.Error())
		}
		m.IsInitialized = true
		m.ActiveModal = ModalNone
		m.UI.TextInput.Blur()
		m.Screen = ScreenDashboard
		m.DashGen++
		m.DashFocus = 0
		m.DashSidebar = true
		m.DashScroll = 0
		m.Dashboard.Loading = true
		m.Dashboard.Err = ""
		return m, platform.CollectDashboardCmd(m.Device.Serial, m.Device.Mode, true, m.DashGen)

	case SetupConfirmMsg:
		m.ActiveModal = ModalConfirm
		m.Modal.ConfirmMsg = msg.Msg
		m.Modal.OnConfirm = func() tea.Cmd { return msg.Cmd }
		m.Modal.ReturnToCustom = false
		m.Modal.ConfirmWord = msg.ConfirmWord
		m.Modal.ConfirmTyped = ""
		if msg.Warning != "" {
			m.ActiveToast = &Toast{Message: msg.Warning, Type: core.LogInfo}
		}
		return m, nil
	case SetupValidationErrorMsg:
		m.ActiveModal = ModalNone
		return m, m.showError(msg.Err.Error())

	case SettingsFolderSelectedMsg:
		switch msg.Index {
		case 0:
			m.App.Config.BaseDir = msg.Path
		case 1:
			m.App.Config.DevicePath = msg.Path
		}
		m.ActiveModal = ModalSettings
		return m, nil
	}
	return m, nil
}

/* HELPERS */

func formatDeviceMode(m platform.DeviceMode) string {
	switch m {
	case platform.ModeDevice:
		return "ADB DEVICE"
	case platform.ModeSideload:
		return "ADB SIDELOAD"
	case platform.ModeRecovery:
		return "RECOVERY"
	case platform.ModeFastboot:
		return "FASTBOOT"
	case platform.ModeDisconnected:
		return "NOT FOUND"
	default:
		return string(m)
	}
}

func parseLogLevel(line string) core.LogLevel {
	line = strings.ToUpper(line)
	if strings.HasPrefix(line, "ERR:") || strings.HasPrefix(line, "ERROR:") ||
		strings.Contains(line, "FAILED") || strings.Contains(line, "CRITICAL") {
		return core.LogError
	}
	if strings.HasPrefix(line, "OK") || strings.Contains(line, "SUCCESS") ||
		strings.HasPrefix(line, "FINISHED") {
		return core.LogSuccess
	}
	return core.LogInfo
}

var progressPattern = regexp.MustCompile(`(?i)(?:^|[^0-9.-])([0-9]{1,3}(?:\.[0-9]+)?)\s*%`)
var commandEchoPattern = regexp.MustCompile(`(?i)^[A-Z]:\\.*>\s*(?:adb|fastboot)\b`)

func parseProgress(line string) (float64, bool) {
	match := progressPattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || value < 0 || value > 100 {
		return 0, false
	}
	return value, true
}

func (m *AppModel) syncCustomLogs(entry core.LogEntry, overwrite bool) {
	if overwrite && m.Modal.CustomLogs.Len() > 0 {
		m.Modal.CustomLogs.ReplaceLast(entry)
	} else {
		m.Modal.CustomLogs.Add(entry)
	}
	m.LogsDirty = true
}

func (m *AppModel) syncMainLogs(entry core.LogEntry, overwrite bool) {
	if overwrite && m.Logs.Len() > 0 {
		last, _ := m.Logs.Last()
		if !strings.Contains(last.Text, ">") && !strings.Contains(strings.ToUpper(last.Text), "EXECUTION") {
			m.Logs.ReplaceLast(entry)
		} else {
			m.Logs.Add(entry)
		}
	} else {
		m.Logs.Add(entry)
	}
	m.LogsDirty = true
}

func (m *AppModel) canFlash(isSideload bool) (bool, string) {
	if m.Device.Mode == platform.ModeDisconnected || m.Device.Mode == platform.ModeOffline {
		return false, "Device is disconnected or offline."
	}

	if isSideload {
		if m.Device.Mode != platform.ModeRecovery && m.Device.Mode != platform.ModeDevice && m.Device.Mode != platform.ModeSideload {
			return false, "Device must be in Recovery or Sideload mode."
		}
	} else {
		if m.Device.Mode != platform.ModeFastboot {
			return false, "Device must be in Fastboot mode."
		}
		if err := core.CheckBattery(m.Device); err != nil {
			if errors.Is(err, core.ErrBatteryUnknown) {
				m.ActiveToast = &Toast{Message: "Warning: battery status unknown; verify charge before flashing.", Type: core.LogInfo}
				return true, ""
			}
			return false, err.Error()
		}
	}

	return true, ""
}

func handleMenuSelect(m AppModel) (tea.Model, tea.Cmd) {
	if len(m.Menu) == 0 || m.Selection < 0 || m.Selection >= len(m.Menu) {
		return m, nil
	}
	sel := m.Menu[m.Selection]
	if sel.Action == "dashboard" {
		return openDashboard(m)
	}
	if reason := m.itemUnavailableReason(sel); reason != "" {
		return m, m.showError(reason)
	}

	var filter string
	switch sel.Action {
	case "flash_rec", "flash_boot", "wipe_super":
		filter = ".img"
	case "sideload":
		filter = ".zip"
	case "calculate_sha256":
		filter = "*"
	}

	if filter != "" {
		m.ActiveModal = ModalFile
		if sel.Action == "calculate_sha256" {
			m.Modal.FileTitle = "SELECT FILE TO CALCULATE SHA-256"
		} else {
			m.Modal.FileTitle = "SELECT " + strings.ToUpper(strings.Split(sel.Label, " ")[1]) + " FILE"
		}
		m.Modal.FileFilter = filter

		if sel.Action == "calculate_sha256" {
			m.Modal.FileDir = firstNonEmpty(m.DevicePath, m.BaseDir)
		} else {
			m.Modal.FileDir = operationPickerDirectory(
				firstNonEmpty(m.DevicePath, m.BaseDir),
				sel.Action,
				m.App.Config.Folders,
			)
		}
		if m.Modal.FileDir == "" {
			m.Modal.FileDir, _ = os.Getwd()
		}

		loadCmd := m.loadModalFiles(m.Modal.FileFilter)
		m.Modal.FileCursor = 0

		switch sel.Action {
		case "calculate_sha256":
			m.Modal.OnFileSelect = func(path, _ string) tea.Cmd {
				return func() tea.Msg { return SHA256FileSelectedMsg(path) }
			}
		case "flash_rec":
			if ok, msg := m.canFlash(false); !ok {
				m.ActiveModal = ModalNone
				return m, m.showError(msg)
			}
			m.Modal.OnFileSelect = func(p, fingerprint string) tea.Cmd {
				return m.confirmSelectedFile("flash_rec", p, fingerprint)
			}
		case "flash_boot":
			if ok, msg := m.canFlash(false); !ok {
				m.ActiveModal = ModalNone
				return m, m.showError(msg)
			}
			m.Modal.OnFileSelect = func(p, fingerprint string) tea.Cmd {
				return m.confirmSelectedFile("flash_boot", p, fingerprint)
			}
		case "wipe_super":
			if ok, msg := m.canFlash(false); !ok {
				m.ActiveModal = ModalNone
				return m, m.showError(msg)
			}
			m.Modal.OnFileSelect = func(p, fingerprint string) tea.Cmd {
				return m.confirmSelectedFile("wipe_super", p, fingerprint)
			}
		case "sideload":
			if ok, msg := m.canFlash(true); !ok {
				m.ActiveModal = ModalNone
				return m, m.showError(msg)
			}
			m.Modal.OnFileSelect = func(p, fingerprint string) tea.Cmd {
				return m.confirmSelectedFile("sideload", p, fingerprint)
			}
		}
		return m, tea.Batch(loadCmd, m.UI.TextInput.Focus())
	}

	switch sel.Action {
	case "rb_system", "rb_recovery":
		recovery := sel.Action == "rb_recovery"
		target := m.Device
		engine := m.App.Engine
		logMessage := "rebooting to system..."
		if recovery {
			logMessage = "rebooting to recovery..."
		}
		m.beginOperation()
		m.ActiveModal = ModalNone
		engine.Emit(core.LogInfo, "> "+logMessage)
		return m, engine.ExecuteAsync(func(ctx context.Context) error {
			state, err := freshDeviceState(target)
			if err != nil {
				return err
			}
			if err := ensureSameDevice(target, state); err != nil {
				return err
			}
			if recovery {
				return engine.DeviceService.RebootRecoveryWithAcknowledgement(ctx, state, true)
			}
			return engine.DeviceService.RebootSystemWithAcknowledgement(ctx, state, true)
		})
	case "refresh":
		m.DashGen++
		m.IsRefreshing = true
		m.ActiveToast = &Toast{Message: "Scanning for devices...", Type: core.LogInfo}
		return m, tea.Batch(
			platform.CheckDeviceStateCmd(m.DashGen, true),
			platform.PollTickCmd(),
			toastTimeoutCmd(2*time.Second),
		)
	case "help":
		m.ActiveModal = ModalHelp
	case "custom":
		m.ActiveModal = ModalCustom
		m.UI.TextInput.Reset()
		m.UI.TextInput.Width = 50
		switch m.Device.Mode {
		case platform.ModeFastboot:
			m.UI.TextInput.Placeholder = "Fastboot command (e.g. getvar product)"
		case platform.ModeDevice, platform.ModeRecovery, platform.ModeSideload:
			m.UI.TextInput.Placeholder = "ADB command (e.g. shell getprop ro.product.model)"
		default:
			m.UI.TextInput.Placeholder = "Connect a device to run a command"
		}
		return m, m.UI.TextInput.Focus()
	case "settings":
		m.ActiveModal = ModalSettings
		m.Modal.SettingsCursor = 0
		return m, nil
	case "exit":
		return m, tea.Quit
	}
	return m, nil
}

func freshDeviceState(expected platform.DeviceState) (platform.DeviceState, error) {
	state, err := platform.ReadDeviceTargetState(expected.Serial, expected.Mode)
	if err != nil {
		return platform.DeviceState{}, err
	}
	if state.Mode == platform.ModeSideload && state.Serial == expected.Serial {
		state = retainSideloadIdentity(expected, state)
	}
	return state, nil
}

func freshSideloadDeviceState(expected platform.DeviceState) (platform.DeviceState, error) {
	state, err := platform.ReadADBDeviceTargetState(expected.Serial)
	if err != nil {
		return platform.DeviceState{}, err
	}
	if state.Mode == platform.ModeSideload {
		state = retainSideloadIdentity(expected, state)
	}
	return state, nil
}

func ensureSameSideloadDevice(expected, actual platform.DeviceState) error {
	if expected.Serial == "" || expected.Serial == "-" || actual.Serial != expected.Serial {
		return fmt.Errorf("selected ADB device changed before sideload: expected %q, found %q",
			expected.Serial, actual.Serial)
	}
	switch actual.Mode {
	case platform.ModeDevice, platform.ModeRecovery, platform.ModeSideload:
		return nil
	default:
		return fmt.Errorf("selected device %q is not ready for sideload (mode: %s)",
			actual.Serial, actual.Mode)
	}
}

func retainSideloadIdentity(previous, current platform.DeviceState) platform.DeviceState {
	if current.Mode != platform.ModeSideload || current.Serial == "" ||
		current.Serial != previous.Serial || strings.TrimSpace(previous.Codename) == "" {
		return current
	}
	current.Codename = previous.Codename
	if current.Model == "" || current.Model == "-" || current.Model == "SIDELOAD DEVICE" {
		current.Model = previous.Model
	}
	if current.Manufacturer == "" || current.Manufacturer == "-" {
		current.Manufacturer = previous.Manufacturer
	}
	return current
}

func inspectPackageChecksum(path string) tea.Cmd {
	return func() tea.Msg {
		if !strings.EqualFold(filepath.Ext(path), ".zip") {
			_, present, err := verifyPackageChecksumIfPresent(path)
			if err != nil {
				if errors.Is(err, core.ErrHashMismatch) {
					return PackageIntegrityMsg{
						Path: path, Status: "MISMATCH: adjacent checksum differs; its source is not independently trusted",
					}
				}
				return PackageIntegrityMsg{Path: path, Status: "ERROR: " + err.Error()}
			}
			if !present {
				return PackageIntegrityMsg{Path: path, Status: "UNVERIFIED: no independently trusted checksum supplied"}
			}
			return PackageIntegrityMsg{
				Path: path, Status: "UNVERIFIED: matches adjacent .sha256; source is not independently trusted",
			}
		}
		_, present, err := readPackageChecksumIfPresent(path)
		if err != nil {
			return PackageIntegrityMsg{Path: path, Status: "ERROR: " + err.Error()}
		}
		if !present {
			return PackageIntegrityMsg{Path: path, Status: "UNVERIFIED: no independently trusted checksum supplied"}
		}
		return PackageIntegrityMsg{
			Path: path, Status: "FOUND: adjacent checksum will be checked during staging; its source is not independently trusted",
		}
	}
}

func inspectPackageCompatibility(path string, device platform.DeviceState, engine *core.Engine) tea.Cmd {
	return func() tea.Msg {
		method := "fastboot-flash"
		if device.Mode == platform.ModeDevice || device.Mode == platform.ModeRecovery ||
			device.Mode == platform.ModeSideload {
			method = "adb-sideload"
		}
		report := core.EvaluateROMCompatibility(device, path, method)
		if engine != nil {
			report = engine.EvaluateCompatibility(device, path, method)
		}
		return PackageCompatibilityMsg{
			Path: path, Serial: device.Serial, Mode: device.Mode,
			Codename: device.Codename, Status: string(report.State) + ": " + report.Error(),
		}
	}
}

func readPackageChecksumIfPresent(path string) (string, bool, error) {
	sidecar := path + ".sha256"
	if _, err := os.Stat(sidecar); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect checksum file: %w", err)
	}
	expected, err := core.ReadSHA256ChecksumFile(sidecar)
	if err != nil {
		return "", true, fmt.Errorf("read checksum file: %w", err)
	}
	if len(expected) != 64 {
		return "", true, fmt.Errorf("invalid SHA-256 checksum length")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return "", true, fmt.Errorf("invalid SHA-256 checksum: %w", err)
	}
	return expected, true, nil
}

func verifyPackageChecksumIfPresent(path string) (string, bool, error) {
	expected, present, err := readPackageChecksumIfPresent(path)
	if err != nil || !present {
		return expected, present, err
	}
	if err := core.VerifyImageSHA256(path, expected); err != nil {
		return "", true, fmt.Errorf("verify checksum: %w", err)
	}
	return expected, true, nil
}

func (m AppModel) confirmSelectedFile(action, path, selectedFingerprint string) tea.Cmd {
	return func() tea.Msg {
		if selectedFingerprint == "" && action != "sideload" {
			return SetupValidationErrorMsg{Err: fmt.Errorf("selected input has no calculated content fingerprint; select it again")}
		}
		var err error
		if selectedFingerprint != "" {
			currentFingerprint, fingerprintErr := core.CalculateInputFingerprint(path)
			if fingerprintErr != nil {
				return SetupValidationErrorMsg{Err: fmt.Errorf("fingerprint selected input: %w", fingerprintErr)}
			}
			if !strings.EqualFold(currentFingerprint, selectedFingerprint) {
				return SetupValidationErrorMsg{Err: fmt.Errorf("%w: selected %s, current %s",
					core.ErrInputChanged, selectedFingerprint, currentFingerprint)}
			}
		}
		var expectedSHA256 string
		if action != "sideload" {
			expectedSHA256, _, err = verifyPackageChecksumIfPresent(path)
		}
		if err != nil {
			return SetupValidationErrorMsg{Err: fmt.Errorf("checksum validation failed: %w", err)}
		}
		var state platform.DeviceState
		if action == "sideload" {
			state, err = freshSideloadDeviceState(m.Device)
		} else {
			state, err = freshDeviceState(m.Device)
		}
		if err == nil {
			if action == "sideload" {
				err = ensureSameSideloadDevice(m.Device, state)
			} else {
				err = ensureSameDevice(m.Device, state)
			}
		}
		warning := ""
		allowUnknownBattery := false
		allowCandidateCompatibility := false
		if err == nil {
			switch action {
			case "sideload":
				allowUnknownBattery = errors.Is(core.CheckBattery(state), core.ErrBatteryUnknown)
				if allowUnknownBattery {
					warning = "Battery status is unavailable; verify the device is sufficiently charged before sideloading."
				}
				allowCandidateCompatibility = true
				plan := m.App.Engine.FlashService.PlanSideload(context.Background(), state, path, core.FlashOptions{
					AllowUnknownBattery: allowUnknownBattery, AllowCandidateCompatibility: true,
					RiskAcknowledged: true,
				})
				err = plan.Error()
				if err == nil && plan.CompatibilityStatus != string(core.CompatibilityVerified) {
					warning = strings.TrimSpace(strings.Join([]string{
						warning,
						"ROM compatibility is not fully verified. Confirm this ZIP is intended for the selected device before continuing.",
					}, "\n"))
				}
			case "wipe_super":
				err = m.App.Engine.FlashService.ValidateDeviceForWipeSuper(context.Background(), state, path)
			default:
				err = m.App.Engine.FlashService.ValidateDeviceForFlashAllowUnknownBattery(context.Background(), state, path)
				if errors.Is(core.CheckBattery(state), core.ErrBatteryUnknown) {
					warning = "Warning: battery status unknown; verify charge before flashing."
				}
			}
		}
		if err != nil {
			return SetupValidationErrorMsg{Err: err}
		}

		name := filepath.Base(path)
		confirmText, confirmWord := flashConfirmation(action, name, warning)
		confirmText += fmt.Sprintf("\nTarget: %s device %q", m.Device.Mode, m.Device.Serial)
		if selectedFingerprint != "" {
			confirmText += "\nSelected content fingerprint (calculated SHA-256): " +
				shortFingerprint(selectedFingerprint)
		} else if action == "sideload" {
			confirmText += "\nSHA-256 is not checked during sideload."
		}
		warning = ""
		return SetupConfirmMsg{
			Msg:         confirmText,
			Warning:     warning,
			ConfirmWord: confirmWord,
			Cmd: m.App.Engine.ExecuteAsync(func(ctx context.Context) error {
				if selectedFingerprint != "" {
					currentFingerprint, err := core.CalculateInputFingerprint(path)
					if err != nil {
						return fmt.Errorf("fingerprint selected input before execution: %w", err)
					}
					if !strings.EqualFold(currentFingerprint, selectedFingerprint) {
						return fmt.Errorf("%w: selected %s, current %s",
							core.ErrInputChanged, selectedFingerprint, currentFingerprint)
					}
				}
				var state platform.DeviceState
				if action == "sideload" {
					state, err = freshSideloadDeviceState(m.Device)
				} else {
					state, err = freshDeviceState(m.Device)
				}
				if err != nil {
					return err
				}
				if action == "sideload" {
					if err := ensureSameSideloadDevice(m.Device, state); err != nil {
						return err
					}
				} else if err := ensureSameDevice(m.Device, state); err != nil {
					return err
				}

				if action == "sideload" {
					m.App.Engine.Emit(core.LogInfo,
						fmt.Sprintf("Preparing sideload package: %s...", filepath.Base(path)))
					return m.App.Engine.FlashService.SideloadWithPolicy(ctx, state, path, core.FlashOptions{
						AllowUnknownBattery: allowUnknownBattery, AllowCandidateCompatibility: allowCandidateCompatibility,
						RiskAcknowledged: true,
					})
				}
				if action == "wipe_super" {
					return m.App.Engine.FlashService.WipeSuperWithPolicy(ctx, state, path, core.FlashOptions{
						RiskAcknowledged: true, ExpectedSHA256: expectedSHA256,
						InputFingerprintSHA256: selectedFingerprint,
					})
				}
				partition := "boot"
				if action == "flash_rec" {
					partition = "recovery"
				}
				return m.App.Engine.FlashService.FlashImageWithPolicy(ctx, state, partition, path, core.FlashOptions{
					AllowUnknownBattery: true, RiskAcknowledged: true,
					ExpectedSHA256: expectedSHA256, InputFingerprintSHA256: selectedFingerprint,
				})
			}),
		}
	}
}

func calculateSelectedInputFingerprint(
	requestID uint64,
	path string,
	onSelect func(string, string) tea.Cmd,
) tea.Cmd {
	return func() tea.Msg {
		fingerprint, err := core.CalculateInputFingerprint(path)
		var integrity *PackageIntegrityMsg
		if !strings.EqualFold(filepath.Ext(path), ".zip") {
			if err != nil {
				msg, ok := inspectPackageChecksum(path)().(PackageIntegrityMsg)
				if ok {
					integrity = &msg
				}
			} else {
				msg := packageChecksumStatusForFingerprint(path, fingerprint)
				integrity = &msg
			}
		}
		return SelectedInputFingerprintMsg{
			RequestID: requestID, Path: path, Fingerprint: fingerprint,
			Integrity: integrity, Err: err, OnSelect: onSelect,
		}
	}
}

func packageChecksumStatusForFingerprint(path, fingerprint string) PackageIntegrityMsg {
	expected, present, err := readPackageChecksumIfPresent(path)
	if err != nil {
		return PackageIntegrityMsg{Path: path, Status: "ERROR: " + err.Error()}
	}
	if !present {
		return PackageIntegrityMsg{Path: path, Status: "UNVERIFIED: no independently trusted checksum supplied"}
	}
	if err := verifyPackageChecksumFingerprint(expected, fingerprint); err != nil {
		if errors.Is(err, core.ErrHashMismatch) {
			return PackageIntegrityMsg{
				Path: path, Status: "MISMATCH: adjacent checksum differs; its source is not independently trusted",
			}
		}
		return PackageIntegrityMsg{Path: path, Status: "ERROR: " + err.Error()}
	}
	return PackageIntegrityMsg{
		Path: path, Status: "UNVERIFIED: matches adjacent .sha256; source is not independently trusted",
	}
}

func verifyPackageChecksumFingerprint(expected, fingerprint string) error {
	expected = strings.TrimSpace(expected)
	if !strings.EqualFold(expected, fingerprint) {
		return fmt.Errorf("verify checksum: %w: expected %s, got %s",
			core.ErrHashMismatch, expected, fingerprint)
	}
	return nil
}

func operationPickerDirectory(baseDir, action string, configuredFolders map[string]string) string {
	if strings.TrimSpace(baseDir) == "" {
		return ""
	}
	if configured := strings.TrimSpace(configuredFolders[action]); configured != "" {
		target := filepath.Join(baseDir, configured)
		if isDirectory(target) {
			return target
		}
	}

	var folderNames []string
	switch action {
	case "sideload":
		folderNames = []string{"ROMs", "ROM"}
	case "flash_rec", "flash_boot", "wipe_super":
		folderNames = []string{"Recovery"}
	default:
		return baseDir
	}

	if containsFolderName(filepath.Base(filepath.Clean(baseDir)), folderNames) {
		return baseDir
	}
	if target := findChildDirectory(baseDir, folderNames); target != "" {
		return target
	}
	return baseDir
}

func findChildDirectory(parent string, names []string) string {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return ""
	}
	for _, name := range names {
		for _, entry := range entries {
			if entry.IsDir() && strings.EqualFold(entry.Name(), name) {
				return filepath.Join(parent, entry.Name())
			}
		}
	}
	return ""
}

func containsFolderName(name string, names []string) bool {
	for _, candidate := range names {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	return false
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func shortFingerprint(fingerprint string) string {
	if len(fingerprint) < 16 {
		return "unavailable"
	}
	return fingerprint[:16] + "…"
}

func ensureSameDevice(expected, actual platform.DeviceState) error {
	if expected.Serial == "" || expected.Serial == "-" || actual.Serial != expected.Serial || actual.Mode != expected.Mode {
		return fmt.Errorf("selected device changed before operation: expected %s %q, found %s %q",
			expected.Mode, expected.Serial, actual.Mode, actual.Serial)
	}
	return nil
}

func flashConfirmation(action, name, warning string) (string, string) {
	switch action {
	case "wipe_super":
		return "This erases the super partition (system, vendor, product). The device will not boot until you flash a ROM again.", "WIPE"
	case "sideload":
		message := "Sideload: " + name + "?"
		if warning != "" {
			return warning + "\n\n" + message, ""
		}
		return message, ""
	default:
		message := "Flash " + strings.ToUpper(action) + " with: " + name + "?"
		if warning != "" {
			message = warning + "\n\n" + message
		}
		return message, ""
	}
}
