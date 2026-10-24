package platform

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

type DeviceMode string

const (
	ModeDisconnected DeviceMode = "DISCONNECTED"
	ModeFastboot     DeviceMode = "FASTBOOT"
	ModeDevice       DeviceMode = "DEVICE"
	ModeRecovery     DeviceMode = "RECOVERY"
	ModeSideload     DeviceMode = "SIDELOAD"
	ModeUnauthorized DeviceMode = "UNAUTHORIZED"
	ModeOffline      DeviceMode = "OFFLINE"
	ModeAmbiguous    DeviceMode = "AMBIGUOUS"
)

type DeviceState struct {
	Mode              DeviceMode
	Serial            string
	Model             string
	Codename          string
	Manufacturer      string
	Transport         string
	BootCompleted     string
	Candidates        []DeviceState
	SecureBoot        string
	Battery           string
	BatteryMV         int
	BatteryPct        int
	BatteryInvalid    bool
	Slot              string
	SlotCount         int
	Secure            string
	Unlocked          TriState
	TempC             float64
	Charging          TriState
	BootloaderVersion string
	HardwareRevision  string
}

type TriState int

const (
	Unknown TriState = iota
	Yes
	No
)

var (
	reProduct       = regexp.MustCompile(`product:\s*(\S+)`)
	reBattery       = regexp.MustCompile(`(?i)battery-voltage:\s*(\d+)`)
	reBatteryNumber = regexp.MustCompile(`\d+`)
	reSlot          = regexp.MustCompile(`current-slot:\s*(\S+)`)
	reSecure        = regexp.MustCompile(`(?i)secure:\s*(\S+)`)
	reUnlocked      = regexp.MustCompile(`(?i)unlocked:\s*(yes|no)`)
	reDeviceState   = regexp.MustCompile(`(?i)device-state:\s*(unlocked|locked)`)

	isScanning        atomic.Bool
	deviceCache       = make(map[string]string)
	secureCache       = make(map[string]secureCacheEntry)
	cacheMu           sync.RWMutex
	secureCacheMaxAge = 10 * time.Second
	cmdTimeout        = 3 * time.Second
	cmdRetryDelay     = 100 * time.Millisecond
	deviceCommand     = runDeviceCommand
)

type secureCacheEntry struct {
	value     string
	checkedAt time.Time
}

func deviceCacheKey(state DeviceState) string {
	transport := "adb"
	if state.Mode == ModeFastboot {
		transport = "fastboot"
	}
	return transport + "\x00" + state.Serial
}

type DeviceUpdateMsg DeviceState
type DeviceScanMsg struct {
	Gen    int
	Device DeviceState
	Fresh  bool
}
type DeviceSelectionMsg struct {
	Gen    int
	Device DeviceState
}
type PollMsg time.Time
type HeartbeatMsg time.Time
type SkipUpdateMsg struct{}

func HeartbeatCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
		return HeartbeatMsg(t)
	})
}

func PollTickCmd() tea.Cmd {
	return tea.Tick(3000*time.Millisecond, func(t time.Time) tea.Msg {
		return PollMsg(t)
	})
}

func runCmdWithRetry(cmdName string, timeout time.Duration, retries int, args ...string) ([]byte, error) {
	cmdPath, err := ResolveCommandPath(cmdName)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var out []byte
	for i := 0; i < retries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, cmdPath, args...)
		out, err = cmd.CombinedOutput()
		cancel()
		if err == nil {
			return out, nil
		}
		lastErr = fmt.Errorf("cmd error (attempt %d): %w", i+1, err)
		if i < retries-1 {
			time.Sleep(cmdRetryDelay)
		}
	}
	return out, lastErr
}

func runFastbootCmd(args ...string) ([]byte, error) {
	return runCmdWithRetry("fastboot", cmdTimeout, 2, args...)
}

func runAdbCmd(args ...string) ([]byte, error) {
	return runCmdWithRetry("adb", cmdTimeout, 2, args...)
}

func runDeviceCommand(name string, args ...string) ([]byte, error) {
	switch name {
	case "adb":
		return runAdbCmd(args...)
	case "fastboot":
		return runFastbootCmd(args...)
	default:
		return nil, fmt.Errorf("unsupported device command %q", name)
	}
}

func sanitizeDiagnosticText(value string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return -1
		}
		return char
	}, value)
}

func ValidateDeviceTarget(serial string, mode DeviceMode) error {
	if strings.TrimSpace(serial) == "" || serial == "-" {
		return fmt.Errorf("selected device has no usable serial number")
	}

	var name string
	var args []string
	switch mode {
	case ModeFastboot:
		name, args = "fastboot", []string{"devices"}
	case ModeDevice, ModeRecovery, ModeSideload:
		name, args = "adb", []string{"devices", "-l"}
	default:
		return fmt.Errorf("cannot validate a target in %s mode", mode)
	}

	out, err := deviceCommand(name, args...)
	if err != nil {
		return fmt.Errorf("recheck selected %s device: %w", name, err)
	}
	var candidates []DeviceState
	if name == "fastboot" {
		candidates = parseDeviceCandidates(string(out), "")
	} else {
		candidates = parseDeviceCandidates("", string(out))
	}
	if !containsDeviceCandidate(candidates, DeviceState{Mode: mode, Serial: serial}) {
		return fmt.Errorf("selected device %q is no longer available in %s mode", serial, mode)
	}
	return nil
}

func ReadDeviceTargetState(serial string, mode DeviceMode) (DeviceState, error) {
	if err := ValidateDeviceTarget(serial, mode); err != nil {
		return DeviceState{}, err
	}
	state := DeviceState{
		Mode: mode, Serial: serial, Model: "-", Battery: "-", Slot: "-", Secure: "-",
		BatteryPct: -1, BatteryMV: -1,
	}
	switch mode {
	case ModeFastboot:
		return fetchFastbootDetails(state, true), nil
	case ModeDevice, ModeRecovery:
		return fetchAdbDetails(state), nil
	case ModeSideload, ModeUnauthorized, ModeOffline:
		return state, nil
	default:
		return DeviceState{}, fmt.Errorf("cannot read a target in %s mode", mode)
	}
}

func ReadADBDeviceTargetState(serial string) (DeviceState, error) {
	if strings.TrimSpace(serial) == "" || serial == "-" {
		return DeviceState{}, fmt.Errorf("selected device has no usable serial number")
	}
	out, err := deviceCommand("adb", "devices", "-l")
	if err != nil {
		return DeviceState{}, fmt.Errorf("recheck selected ADB device: %w", err)
	}
	for _, candidate := range parseDeviceCandidates("", string(out)) {
		if candidate.Serial != serial {
			continue
		}
		switch candidate.Mode {
		case ModeDevice, ModeRecovery:
			return fetchAdbDetails(candidate), nil
		case ModeSideload:
			return candidate, nil
		default:
			return DeviceState{}, fmt.Errorf("selected device %q is %s, not ready for ADB sideload", serial, candidate.Mode)
		}
	}
	return DeviceState{}, fmt.Errorf("selected device %q is no longer available through ADB", serial)
}

func CheckDeviceState() tea.Msg {
	return checkDeviceState(false)
}

func CheckDeviceStateFresh() tea.Msg {
	return checkDeviceState(true)
}

func CheckDeviceStateCmd(gen int, fresh bool) tea.Cmd {
	return func() tea.Msg {
		var result tea.Msg
		if fresh {
			result = CheckDeviceStateFresh()
		} else {
			result = CheckDeviceState()
		}
		if state, ok := result.(DeviceUpdateMsg); ok {
			return DeviceScanMsg{Gen: gen, Device: DeviceState(state), Fresh: fresh}
		}
		return result
	}
}

func checkDeviceState(forceSecureRefresh bool) tea.Msg {
	if !isScanning.CompareAndSwap(false, true) {
		return SkipUpdateMsg{}
	}
	defer isScanning.Store(false)

	state := DeviceState{Mode: ModeDisconnected, Serial: "-", Model: "-", Battery: "-", Slot: "-", Secure: "-"}
	state.BatteryPct, state.BatteryMV = -1, -1
	fastbootOut, _ := deviceCommand("fastboot", "devices")
	adbOut, _ := deviceCommand("adb", "devices", "-l")
	candidates := parseDeviceCandidates(string(fastbootOut), string(adbOut))
	state = resolveDeviceCandidates(candidates)
	if state.Mode == ModeDisconnected || state.Mode == ModeAmbiguous {
		return DeviceUpdateMsg(state)
	}

	switch state.Mode {
	case ModeFastboot:
		state = fetchFastbootDetails(state, forceSecureRefresh)
	case ModeDevice, ModeRecovery:
		state = fetchAdbDetails(state)
	case ModeSideload:
		state.Model = "SIDELOAD DEVICE"
	case ModeUnauthorized:
		state.Model = "ACTION REQUIRED"
	case ModeOffline:
		state.Model = "OFFLINE"
	}
	return DeviceUpdateMsg(state)
}

func parseDeviceCandidates(fastbootOutput, adbOutput string) []DeviceState {
	var candidates []DeviceState
	for _, line := range strings.Split(fastbootOutput, "\n") {
		line = sanitizeDiagnosticText(line)
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "fastboot" {
			candidates = append(candidates, DeviceState{
				Mode: ModeFastboot, Serial: fields[0], Model: "-", Battery: "-", Slot: "-", Secure: "-",
				BatteryPct: -1, BatteryMV: -1,
			})
		}
	}
	for _, line := range strings.Split(adbOutput, "\n") {
		line = sanitizeDiagnosticText(line)
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "List" || fields[0] == "daemon" {
			continue
		}
		state := DeviceState{
			Serial: fields[0], Model: "-", Battery: "-", Slot: "-", Secure: "-",
			Transport:  adbTransport(fields[0], fields[2:]),
			BatteryPct: -1, BatteryMV: -1,
		}
		switch fields[1] {
		case "device":
			state.Mode = ModeDevice
		case "recovery":
			state.Mode = ModeRecovery
		case "sideload":
			state.Mode = ModeSideload
		case "unauthorized":
			state.Mode = ModeUnauthorized
		case "offline":
			state.Mode = ModeOffline
		default:
			continue
		}
		candidates = append(candidates, state)
	}
	return candidates
}

func resolveDeviceCandidates(candidates []DeviceState) DeviceState {
	switch len(candidates) {
	case 0:
		return DeviceState{Mode: ModeDisconnected, Serial: "-", Model: "-", Battery: "-", Slot: "-", Secure: "-", BatteryPct: -1, BatteryMV: -1}
	case 1:
		return candidates[0]
	default:
		return DeviceState{
			Mode: ModeAmbiguous, Serial: "-", Model: "MULTIPLE DEVICES",
			Candidates: append([]DeviceState(nil), candidates...), Secure: "-", Battery: "-", Slot: "-",
		}
	}
}

func adbTransport(serial string, details []string) string {
	if strings.Contains(serial, ":") {
		return "TCP/IP"
	}
	for _, detail := range details {
		if strings.HasPrefix(detail, "usb:") {
			return "USB"
		}
	}
	return "unknown"
}

func SelectDeviceCmd(candidate DeviceState, gen int) tea.Cmd {
	return func() tea.Msg {
		switch candidate.Mode {
		case ModeFastboot:
			out, err := deviceCommand("fastboot", "-s", candidate.Serial, "getvar", "product")
			if err != nil || !reProduct.Match(out) {
				return DeviceSelectionMsg{Gen: gen, Device: DeviceState{Mode: ModeDisconnected, Serial: "-", Model: "-", Secure: "-"}}
			}
			return DeviceSelectionMsg{Gen: gen, Device: fetchFastbootDetails(candidate, true)}
		case ModeDevice, ModeRecovery, ModeSideload, ModeUnauthorized, ModeOffline:
			out, err := deviceCommand("adb", "devices", "-l")
			if err != nil || !containsDeviceCandidate(parseDeviceCandidates("", string(out)), candidate) {
				return DeviceSelectionMsg{Gen: gen, Device: DeviceState{Mode: ModeDisconnected, Serial: "-", Model: "-", Secure: "-"}}
			}
			if candidate.Mode == ModeDevice || candidate.Mode == ModeRecovery {
				return DeviceSelectionMsg{Gen: gen, Device: fetchAdbDetails(candidate)}
			}
			return DeviceSelectionMsg{Gen: gen, Device: candidate}
		default:
			return DeviceSelectionMsg{Gen: gen, Device: DeviceState{Mode: ModeDisconnected, Serial: "-", Model: "-", Secure: "-"}}
		}
	}
}

func containsDeviceCandidate(candidates []DeviceState, target DeviceState) bool {
	for _, candidate := range candidates {
		if candidate.Mode == target.Mode && candidate.Serial == target.Serial {
			return true
		}
	}
	return false
}

func fetchFastbootDetails(s DeviceState, forceSecureRefresh bool) DeviceState {
	cacheKey := deviceCacheKey(s)
	cacheMu.RLock()
	cachedModel := deviceCache[cacheKey]
	cachedSecure := secureCache[s.Serial]
	cacheMu.RUnlock()

	if cachedModel != "" && cachedModel != "-" {
		s.Model = cachedModel
	} else {
		if out, err := deviceCommand("fastboot", "-s", s.Serial, "getvar", "product"); err == nil {
			if m := reProduct.FindStringSubmatch(sanitizeDiagnosticText(string(out))); len(m) > 1 {
				codename := strings.ToUpper(strings.TrimSpace(m[1]))
				pretty := prettyDeviceName(codename)
				if pretty != codename {
					s.Model = fmt.Sprintf("%s (%s)", pretty, codename)
				} else {
					s.Model = codename
				}
				cacheMu.Lock()
				deviceCache[cacheKey] = s.Model
				cacheMu.Unlock()
			}
		}
	}

	if !forceSecureRefresh && cachedSecure.value != "" && time.Since(cachedSecure.checkedAt) < secureCacheMaxAge {
		s.Secure = cachedSecure.value
	} else {
		out, _ := deviceCommand("fastboot", "-s", s.Serial, "getvar", "unlocked")
		s.Secure = parseFastbootSecure(string(out))
		s.Unlocked = parseUnlocked(string(out), "")
		if s.Secure == "-" {
			out, _ = deviceCommand("fastboot", "-s", s.Serial, "getvar", "secure")
			if match := reSecure.FindStringSubmatch(string(out)); len(match) > 1 {
				s.SecureBoot = strings.TrimSpace(match[1])
			}
		}
		cacheMu.Lock()
		secureCache[s.Serial] = secureCacheEntry{value: s.Secure, checkedAt: time.Now()}
		cacheMu.Unlock()
	}

	if out, err := deviceCommand("fastboot", "-s", s.Serial, "getvar", "battery-voltage"); err == nil {
		if mv, known, invalid := parseFastbootBatteryValue(string(out)); known {
			s.BatteryMV = mv
			s.Battery = strconv.Itoa(mv) + " mV"
		} else if invalid {
			s.BatteryInvalid = true
		}
	}

	return s
}

func parseFastbootSecure(output string) string {
	output = sanitizeDiagnosticText(output)
	unlocked := parseUnlocked(
		firstMatch(reUnlocked, output),
		firstMatch(reDeviceState, output),
	)
	switch unlocked {
	case Yes:
		return "NO"
	case No:
		return "YES"
	default:
		return "-"
	}
}

func firstMatch(pattern *regexp.Regexp, output string) string {
	match := pattern.FindStringSubmatch(output)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func fetchAdbDetails(s DeviceState) DeviceState {
	cacheKey := deviceCacheKey(s)
	cacheMu.RLock()
	cachedModel := deviceCache[cacheKey]
	cacheMu.RUnlock()

	out, err := deviceCommand("adb", "-s", s.Serial, "shell", "getprop ro.product.marketname; getprop ro.product.model; getprop ro.product.brand; getprop ro.product.device; getprop ro.product.manufacturer; getprop sys.boot_completed")
	lines := strings.Split(sanitizeDiagnosticText(string(out)), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	for len(lines) < 6 {
		lines = append(lines, "")
	}
	marketProp, modelProp, brandProp, codenameProp := lines[0], lines[1], lines[2], strings.ToUpper(lines[3])
	s.Manufacturer = lines[4]
	if lines[5] == "1" {
		s.BootCompleted = "yes"
	} else if lines[5] == "0" {
		s.BootCompleted = "no"
	}

	if cachedModel != "" && cachedModel != "-" {
		s.Model = cachedModel
	} else if err == nil {
		marketingLabel := ""
		if out, err := deviceCommand("adb", "devices", "-l"); err == nil {
			devLines := strings.Split(string(out), "\n")
			for _, l := range devLines {
				fields := strings.Fields(l)
				if len(fields) > 0 && fields[0] == s.Serial {
					for _, f := range fields {
						if strings.HasPrefix(f, "model:") {
							marketingLabel = strings.TrimPrefix(f, "model:")
							marketingLabel = strings.ReplaceAll(marketingLabel, "_", " ")
							break
						}
					}
					break
				}
			}
		}

		name := ""
		candidates := []string{marketProp, modelProp, marketingLabel, codenameProp}
		for _, c := range candidates {
			if c == "" {
				continue
			}
			pretty := prettyDeviceName(c)
			if pretty != strings.ToUpper(c) {
				name = pretty
				break
			}
		}

		if name == "" {
			if marketProp != "" {
				name = marketProp
			} else if marketingLabel != "" && len(marketingLabel) > 3 {
				name = marketingLabel
			} else if brandProp != "" && modelProp != "" {
				name = brandProp + " " + modelProp
			} else {
				name = modelProp
			}
		}

		s.Model = strings.ToUpper(name)
		s.Codename = strings.ToLower(codenameProp)

		cacheMu.Lock()
		deviceCache[cacheKey] = s.Model
		cacheMu.Unlock()
	}
	s.Codename = strings.ToLower(codenameProp)
	s.Secure = "YES"

	if out, err := deviceCommand("adb", "-s", s.Serial, "shell", "dumpsys", "battery"); err == nil {
		stats := adbStats{BatteryPct: -1, BatteryMV: -1}
		parseBatterySection(&stats, strings.Split(sanitizeDiagnosticText(string(out)), "\n"))
		s.BatteryPct, s.BatteryMV = stats.BatteryPct, stats.BatteryMV
		s.BatteryInvalid = stats.BatteryInvalid
		if stats.BatteryPct >= 0 {
			s.Battery = strconv.Itoa(stats.BatteryPct) + "%"
		}
		if stats.BatteryMV > 0 {
			s.Battery += fmt.Sprintf(" / %d mV", stats.BatteryMV)
		}
	}

	return s
}

func parseFastbootBattery(output string) (int, bool) {
	value, known, _ := parseFastbootBatteryValue(output)
	return value, known
}

func parseFastbootBatteryValue(output string) (int, bool, bool) {
	output = sanitizeDiagnosticText(output)
	match := reBattery.FindStringSubmatch(output)
	if len(match) < 2 {
		return 0, false, false
	}
	number := reBatteryNumber.FindString(match[1])
	value, err := strconv.Atoi(number)
	if err != nil {
		return 0, false, true
	}
	if value == 0 {
		return 0, false, false
	}
	if value < 1000 || value > 6000 {
		return 0, false, true
	}
	return value, true, false
}

var deviceNameMap = map[string]string{
	"M2007J17I":  "MI 10I",
	"M2102K1G":   "MI 11 ULTRA",
	"M2102J20SG": "POCO X3 PRO",
	"M2012K11AG": "POCO F3 / MI 11X",
	"M2012K11AC": "REDMI K40 / POCO F3",
	"24069PC21I": "POCO F6",
	"24069PC21G": "POCO F6",
	"PERIDOT":    "POCO F6",
	"BERYLLIUM":  "POCO F1",
	"MONDRIAN":   "POCO F5 PRO",
	"MARBLE":     "POCO F5",
	"VILI":       "XIAOMI 11T PRO",
	"SWEET":      "REDMI NOTE 10 PRO",
	"SUNNY":      "REDMI NOTE 10",
}

var (
	extraDeviceNames = map[string]string{}
	deviceNamesMu    sync.RWMutex
)

func SetDeviceNames(names map[string]string) {
	deviceNamesMu.Lock()
	defer deviceNamesMu.Unlock()
	extraDeviceNames = make(map[string]string, len(names))
	for id, name := range names {
		extraDeviceNames[strings.ToUpper(strings.TrimSpace(id))] = name
	}
}

func prettyDeviceName(id string) string {
	id = strings.ToUpper(strings.TrimSpace(id))
	deviceNamesMu.RLock()
	name, ok := extraDeviceNames[id]
	deviceNamesMu.RUnlock()
	if ok {
		return name
	}
	if name, ok := deviceNameMap[id]; ok {
		return name
	}
	return id
}
