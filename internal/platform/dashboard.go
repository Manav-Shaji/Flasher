package platform

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type Partition struct {
	Name      string
	SizeBytes uint64
	Type      string
}

type KV struct {
	Key   string
	Value string
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

type Dashboard struct {
	Loaded, Loading bool
	Err             string
	ConnectionError string
	Device          DeviceState
	Partitions      []Partition
	Vars            []KV
	Props           []KV
	StorageUsed     uint64
	StorageTotal    uint64
	RAMUsedKB       uint64
	RAMTotalKB      uint64
	CPUPct          float64
	UptimeSec       uint64
	SELinuxState    string
	Samples         struct{ Battery, Temp, CPU *Samples }
	LastLatency     time.Duration
	UpdatedAt       time.Time
}

type DashboardMsg struct {
	Gen  int
	Data Dashboard
}

type DashboardTickMsg struct {
	Gen int
}

type fastbootInfo struct {
	Codename          string
	Serial            string
	Slot              string
	SlotCount         int
	Unlocked          TriState
	Secure            string
	BatteryMV         int
	BatteryInvalid    bool
	MaxDownloadSize   uint64
	BootloaderVersion string
	HardwareRevision  string
}

type adbStats struct {
	Props                     []KV
	BatteryPct, BatteryMV     int
	BatteryInvalid            bool
	TempC                     float64
	Charging                  TriState
	StorageUsed, StorageTotal uint64
	RAMUsedKB, RAMTotalKB     uint64
	CPU                       cpuCounters
	HasCPU                    bool
	UptimeSec                 uint64
	SELinuxState              string
}

type cpuCounters struct {
	Values []uint64
}

var dashboardCollecting atomic.Bool
var cpuSampleMu sync.Mutex
var cpuSamplesBySerial = map[string]cpuCounters{}
var dashboardCommand = runDashboardCommand

var sensitiveFastbootVar = regexp.MustCompile(`(?i)(token|auth|key|credential)([^:]*:\s*).*`)
var sensitiveProperty = regexp.MustCompile(`(?i)(token|auth|secret|credential|private.?key)`)

const adbDashboardCommand = "echo @@PROP; echo '[ro.product.manufacturer]: ['$(getprop ro.product.manufacturer)']'; echo '[ro.product.model]: ['$(getprop ro.product.model)']'; echo '[ro.product.device]: ['$(getprop ro.product.device)']'; echo '[ro.build.version.release]: ['$(getprop ro.build.version.release)']'; echo '[ro.build.version.sdk]: ['$(getprop ro.build.version.sdk)']'; echo '[ro.build.id]: ['$(getprop ro.build.id)']'; echo '[ro.build.fingerprint]: ['$(getprop ro.build.fingerprint)']'; echo '[ro.build.type]: ['$(getprop ro.build.type)']'; echo '[ro.build.version.security_patch]: ['$(getprop ro.build.version.security_patch)']'; echo '[ro.product.cpu.abi]: ['$(getprop ro.product.cpu.abi)']'; echo '[ro.product.cpu.abilist]: ['$(getprop ro.product.cpu.abilist)']'; echo '[sys.boot_completed]: ['$(getprop sys.boot_completed)']'; echo '[ro.boot.slot_suffix]: ['$(getprop ro.boot.slot_suffix)']'; echo '[ro.build.version.codename]: ['$(getprop ro.build.version.codename)']'; echo @@BATT; dumpsys battery; echo @@DF; df /data; echo @@MEM; head -n 3 /proc/meminfo; echo @@CPU; head -n 1 /proc/stat; echo @@UP; cat /proc/uptime; echo @@SELINUX; getenforce"

func CollectDashboardCmd(serial string, mode DeviceMode, full bool, gen int) tea.Cmd {
	return func() tea.Msg {
		if !dashboardCollecting.CompareAndSwap(false, true) {
			return DashboardMsg{Gen: gen, Data: Dashboard{Loading: true, Err: "collection already in progress"}}
		}
		defer dashboardCollecting.Store(false)

		data := Dashboard{Loading: false, CPUPct: math.NaN()}
		started := time.Now()
		switch mode {
		case ModeFastboot:
			if full {
				out, err := dashboardCommand(20*time.Second, "fastboot", "-s", serial, "getvar", "all")
				if err != nil {
					data.Err = safeDiagnosticError(err.Error())
				}
				vars, partitions, info := parseFastbootGetvarAll(string(out))
				data.Vars, data.Partitions = redactFastbootVars(vars), partitions
				data.Loaded = len(vars) > 0 || len(partitions) > 0 || info.Codename != "" || info.Serial != ""
				data.Device = DeviceState{
					Mode: ModeFastboot, Serial: info.Serial, Codename: info.Codename,
					Slot: info.Slot, SlotCount: info.SlotCount, Unlocked: info.Unlocked,
					SecureBoot: info.Secure, BatteryMV: info.BatteryMV,
					BatteryInvalid:    info.BatteryInvalid,
					BootloaderVersion: info.BootloaderVersion, HardwareRevision: info.HardwareRevision,
					Battery: func() string {
						if info.BatteryMV == 0 {
							return "-"
						}
						return fmt.Sprintf("%d mV", info.BatteryMV)
					}(),
				}
				data.Props = []KV{
					{Key: "product", Value: info.Codename},
					{Key: "serialno", Value: info.Serial},
					{Key: "current-slot", Value: info.Slot},
					{Key: "slot-count", Value: strconv.Itoa(info.SlotCount)},
					{Key: "unlocked", Value: triStateText(info.Unlocked)},
					{Key: "secure", Value: info.Secure},
					{Key: "battery-voltage", Value: fmt.Sprintf("%d mV", info.BatteryMV)},
					{Key: "max-download-size", Value: strconv.FormatUint(info.MaxDownloadSize, 10)},
					{Key: "version-bootloader", Value: info.BootloaderVersion},
					{Key: "hw-revision", Value: info.HardwareRevision},
				}
			} else {
				data.Device.Mode, data.Device.Serial = ModeFastboot, serial
				out, err := dashboardCommand(3*time.Second, "fastboot", "-s", serial, "getvar", "battery-voltage")
				if err != nil {
					// Battery voltage is optional; preserve a useful connected state.
					data.Device.Battery = "-"
				}
				if mv, ok := parseBatteryVoltage(string(out)); ok {
					data.Samples.Battery = NewSamples(30)
					data.Samples.Battery.Add(float64(mv))
					data.Device.BatteryMV = mv
					data.Device.Battery = fmt.Sprintf("%d mV", mv)
				} else if _, _, invalid := parseFastbootBatteryValue(string(out)); invalid {
					data.Device.BatteryInvalid = true
				}
				data.Loaded = true
			}
		case ModeSideload:
			data.Device = DeviceState{Mode: mode, Serial: serial}
			data.Loaded = true
			data.Err = "ADB sideload mode does not expose Android shell diagnostics"
		case ModeDevice, ModeRecovery:
			out, err := dashboardCommand(6*time.Second, "adb", "-s", serial, "shell", adbDashboardCommand)
			if err != nil {
				data.Err = safeDiagnosticError(err.Error())
			}
			stats := parseAdbStats(string(out))
			data.Props = stats.Props
			data.SELinuxState = stats.SELinuxState
			data.StorageUsed, data.StorageTotal = stats.StorageUsed, stats.StorageTotal
			data.RAMUsedKB, data.RAMTotalKB = stats.RAMUsedKB, stats.RAMTotalKB
			data.UptimeSec = stats.UptimeSec
			data.Device = DeviceState{
				Mode: mode, Serial: serial, BatteryMV: stats.BatteryMV, BatteryPct: stats.BatteryPct,
				BatteryInvalid: stats.BatteryInvalid,
				TempC:          stats.TempC, Charging: stats.Charging,
			}
			data.Device.Manufacturer = getPropValue(stats.Props, "ro.product.manufacturer")
			data.Device.Model = getPropValue(stats.Props, "ro.product.model")
			data.Device.Codename = getPropValue(stats.Props, "ro.product.device")
			data.Device.BootCompleted = getPropValue(stats.Props, "sys.boot_completed")
			if data.Device.BootCompleted == "1" {
				data.Device.BootCompleted = "yes"
			} else if data.Device.BootCompleted == "0" {
				data.Device.BootCompleted = "no"
			}
			data.Device.Battery = "-"
			if stats.BatteryPct >= 0 {
				data.Device.Battery = fmt.Sprintf("%d%%", stats.BatteryPct)
			}
			if stats.BatteryMV > 0 {
				data.Device.Battery += fmt.Sprintf(" / %d mV", stats.BatteryMV)
			}
			data.CPUPct = math.NaN()
			if stats.HasCPU {
				cpuSampleMu.Lock()
				previous, hadPrevious := cpuSamplesBySerial[serial]
				cpuSamplesBySerial[serial] = stats.CPU
				cpuSampleMu.Unlock()
				if hadPrevious {
					data.CPUPct = cpuPercent(previous, stats.CPU)
				}
				if !math.IsNaN(data.CPUPct) {
					data.Samples.CPU = NewSamples(30)
					data.Samples.CPU.Add(data.CPUPct)
				}
			}
			if stats.BatteryPct >= 0 {
				data.Samples.Battery = NewSamples(30)
				data.Samples.Battery.Add(float64(stats.BatteryPct))
			}
			if !math.IsNaN(stats.TempC) {
				data.Samples.Temp = NewSamples(30)
				data.Samples.Temp.Add(stats.TempC)
			}
			data.Loaded = len(out) > 0 || err == nil
			if full {
				props, propErr := dashboardCommand(6*time.Second, "adb", "-s", serial, "shell", "getprop")
				if propErr != nil {
					if data.Err == "" {
						data.Err = safeDiagnosticError(propErr.Error())
					}
				} else {
					data.Props = redactDiagnosticProps(parseGetprop(string(props)))
				}
			}
		case ModeDisconnected, ModeUnauthorized, ModeOffline, ModeAmbiguous:
			data.Device = DeviceState{Mode: mode, Serial: serial}
			data.Loaded = true
		default:
			data.Err = fmt.Sprintf("unsupported dashboard device mode: %s", mode)
		}
		if data.Loaded && data.Device.Mode == "" {
			data.Device.Mode, data.Device.Serial = mode, serial
		}
		data.LastLatency = time.Since(started)
		data.UpdatedAt = time.Now()
		return DashboardMsg{Gen: gen, Data: data}
	}
}

func runDashboardCommand(timeout time.Duration, name string, args ...string) ([]byte, error) {
	path, err := ResolveCommandPath(name)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s command failed: %w: %s", name, err, safeDiagnosticError(string(out)))
	}
	return out, nil
}

func parseFastbootGetvarAll(out string) ([]KV, []Partition, fastbootInfo) {
	values := make(map[string]string)
	var order []string
	partitionOrder := []string{}
	partitionIndexes := map[string]int{}
	partitions := []Partition{}
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(sanitizeDiagnosticText(raw))
		line = strings.TrimPrefix(line, "(bootloader)")
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if (key == "partition-size" || key == "partition-type") && strings.Contains(value, ":") {
			name, partitionValue, _ := strings.Cut(value, ":")
			key += ":" + strings.TrimSpace(name)
			value = strings.TrimSpace(partitionValue)
		}
		if key == "" || value == "" || key == "all" || strings.HasPrefix(key, "Finished.") || strings.HasPrefix(value, "Finished.") {
			continue
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = value
		switch {
		case strings.HasPrefix(key, "partition-size:"):
			name := strings.TrimPrefix(key, "partition-size:")
			size, err := parseFlexibleUint(value)
			if err != nil {
				continue
			}
			index := getPartitionIndex(name, &partitionOrder, partitionIndexes, &partitions)
			partitions[index].SizeBytes = size
		case strings.HasPrefix(key, "partition-type:"):
			name := strings.TrimPrefix(key, "partition-type:")
			index := getPartitionIndex(name, &partitionOrder, partitionIndexes, &partitions)
			partitions[index].Type = value
		}
	}
	vars := make([]KV, 0, len(order))
	for _, key := range order {
		if !strings.HasPrefix(key, "partition-size:") && !strings.HasPrefix(key, "partition-type:") {
			vars = append(vars, KV{Key: key, Value: values[key]})
		}
	}
	info := fastbootInfo{
		Codename: values["product"], Serial: values["serialno"], Slot: values["current-slot"],
		Secure: values["secure"], MaxDownloadSize: parseFlexibleUintValue(values["max-download-size"]),
		BootloaderVersion: firstPresent(values, "version-bootloader", "bootloader-version"),
		HardwareRevision:  firstPresent(values, "hw-revision", "hardware-revision"),
	}
	if slotCount, err := strconv.Atoi(values["slot-count"]); err == nil && slotCount > 0 && slotCount <= 8 {
		info.SlotCount = slotCount
	}
	info.Unlocked = parseUnlocked(values["unlocked"], values["device-state"])
	if mv, ok := parseBatteryVoltage(values["battery-voltage"]); ok {
		info.BatteryMV = mv
	} else if _, _, invalid := parseFastbootBatteryValue("battery-voltage: " + values["battery-voltage"]); invalid {
		info.BatteryInvalid = true
	}
	return vars, partitions, info
}

func firstPresent(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values[key] != "" {
			return values[key]
		}
	}
	return ""
}

func redactFastbootVars(vars []KV) []KV {
	result := append([]KV(nil), vars...)
	for i := range result {
		if sensitiveFastbootVar.MatchString(result[i].Key + ": " + result[i].Value) {
			result[i].Value = "[REDACTED]"
		}
	}
	return result
}

func redactDiagnosticProps(props []KV) []KV {
	result := append([]KV(nil), props...)
	for i := range result {
		if sensitiveProperty.MatchString(result[i].Key) {
			result[i].Value = "[REDACTED]"
		}
	}
	return result
}

func safeDiagnosticError(message string) string {
	message = sanitizeDiagnosticText(message)
	lines := strings.Split(message, "\n")
	for i, line := range lines {
		if sensitiveFastbootVar.MatchString(line) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				lines[i] = parts[0] + ": [REDACTED]"
			} else {
				lines[i] = "[REDACTED]"
			}
		}
	}
	return strings.Join(lines, "\n")
}

func getPartitionIndex(name string, order *[]string, indexes map[string]int, partitions *[]Partition) int {
	if index, ok := indexes[name]; ok {
		return index
	}
	index := len(*partitions)
	indexes[name] = index
	*order = append(*order, name)
	*partitions = append(*partitions, Partition{Name: name})
	return index
}

func parseFlexibleUint(raw string) (uint64, error) {
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "mV"))
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		return strconv.ParseUint(value[2:], 16, 64)
	}
	return strconv.ParseUint(value, 10, 64)
}

func parseFlexibleUintValue(value string) uint64 {
	n, _ := parseFlexibleUint(value)
	return n
}

func parseBatteryVoltage(raw string) (int, bool) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "(bootloader)"))
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(raw, "mV"), " mV"))
	if before, after, ok := strings.Cut(raw, ":"); ok {
		if strings.Contains(before, "battery-voltage") {
			raw = strings.TrimSpace(after)
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	return n, err == nil && n >= 1000 && n <= 6000
}

func parseUnlocked(unlocked, deviceState string) TriState {
	switch strings.ToLower(strings.TrimSpace(unlocked)) {
	case "yes":
		return Yes
	case "no":
		return No
	}
	switch strings.ToLower(strings.TrimSpace(deviceState)) {
	case "unlocked":
		return Yes
	case "locked":
		return No
	default:
		return Unknown
	}
}

func triStateText(state TriState) string {
	switch state {
	case Yes:
		return "yes"
	case No:
		return "no"
	default:
		return "-"
	}
}

func parseAdbStats(out string) adbStats {
	stats := adbStats{BatteryPct: -1, BatteryMV: -1, TempC: math.NaN()}
	const (
		sectionOther = iota
		sectionProps
		sectionBattery
		sectionDF
		sectionMemory
		sectionCPU
		sectionUptime
		sectionSELinux
	)
	sections := [8][]string{}
	section := sectionOther
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		header := strings.TrimSpace(line)
		if strings.HasPrefix(header, "@@") {
			switch strings.TrimPrefix(header, "@@") {
			case "PROP":
				section = sectionProps
			case "BATT":
				section = sectionBattery
			case "DF":
				section = sectionDF
			case "MEM":
				section = sectionMemory
			case "CPU":
				section = sectionCPU
			case "UP":
				section = sectionUptime
			case "SELINUX":
				section = sectionSELinux
			default:
				section = sectionOther
			}
			continue
		}
		sections[section] = append(sections[section], line)
	}
	stats.Props = parseGetpropLines(sections[sectionProps])
	parseBatterySection(&stats, sections[sectionBattery])
	stats.StorageUsed, stats.StorageTotal = parseDFData(sections[sectionDF])
	stats.RAMUsedKB, stats.RAMTotalKB = parseMeminfo(sections[sectionMemory])
	stats.CPU, stats.HasCPU = parseCPULine(sections[sectionCPU])
	stats.UptimeSec = parseUptime(sections[sectionUptime])
	stats.SELinuxState = parseSELinuxState(sections[sectionSELinux])
	return stats
}

func parseSELinuxState(lines []string) string {
	for _, line := range lines {
		switch strings.ToUpper(strings.TrimSpace(line)) {
		case "ENFORCING", "PERMISSIVE", "DISABLED":
			return strings.ToUpper(strings.TrimSpace(line))
		}
	}
	return "UNKNOWN"
}

func parseGetprop(out string) []KV {
	lines := strings.Split(out, "\n")
	return parseGetpropLines(lines)
}

func getPropValue(props []KV, key string) string {
	for _, pair := range props {
		if pair.Key == key {
			return pair.Value
		}
	}
	return ""
}

func parseGetpropLines(lines []string) []KV {
	var props []KV
	last := -1
	for _, line := range lines {
		line = sanitizeDiagnosticText(line)
		line = strings.TrimSuffix(line, "\r")
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			if close := strings.Index(line, "]:"); close > 1 {
				key := line[1:close]
				value := strings.Trim(strings.TrimSpace(line[close+2:]), "[]")
				props = append(props, KV{Key: key, Value: value})
				last = len(props) - 1
				continue
			}
		}
		if last >= 0 && line != "" {
			props[last].Value += "\n" + line
		}
	}
	return props
}

func parseBatterySection(stats *adbStats, lines []string) {
	for _, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "level":
			if pct, err := strconv.Atoi(value); err == nil && pct >= 0 && pct <= 100 {
				stats.BatteryPct = pct
			} else {
				stats.BatteryInvalid = true
			}
		case "temperature":
			if tenths, err := strconv.ParseFloat(value, 64); err == nil &&
				!math.IsNaN(tenths) && !math.IsInf(tenths, 0) && tenths >= -400 && tenths <= 1000 {
				stats.TempC = tenths / 10
			}
		case "voltage":
			if mv, err := strconv.Atoi(value); err == nil && mv >= 1000 && mv <= 6000 {
				stats.BatteryMV = mv
			} else if err == nil && mv == 0 {
				continue
			} else {
				stats.BatteryInvalid = true
			}
		case "ac powered", "usb powered", "wireless powered":
			if value == "true" {
				stats.Charging = Yes
			} else if stats.Charging == Unknown {
				stats.Charging = No
			}
		case "status":
			if strings.EqualFold(value, "charging") || strings.EqualFold(value, "full") {
				stats.Charging = Yes
			} else if stats.Charging == Unknown {
				stats.Charging = No
			}
		}
	}
}

func parseDFData(lines []string) (used, total uint64) {
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) < 4 || !strings.HasPrefix(fields[len(fields)-1], "/") {
			continue
		}
		if len(fields) >= 6 && (strings.EqualFold(fields[1], "1K-blocks") || strings.EqualFold(fields[1], "Blocks")) {
			continue
		}
		if len(fields) >= 6 {
			totalBlocks, totalErr := parseFlexibleUint(fields[1])
			usedBlocks, usedErr := parseFlexibleUint(fields[2])
			maxBlocks := ^uint64(0) / 1024
			if totalErr != nil || usedErr != nil || totalBlocks > maxBlocks ||
				usedBlocks > maxBlocks || usedBlocks > totalBlocks {
				continue
			}
			total, used = totalBlocks*1024, usedBlocks*1024
			return used, total
		}
		usedBytes, totalBytes := parseHumanSize(fields[2]), parseHumanSize(fields[1])
		if usedBytes <= totalBytes {
			return usedBytes, totalBytes
		}
	}
	return 0, 0
}

func parseHumanSize(raw string) uint64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	suffix := strings.ToUpper(raw[len(raw)-1:])
	multiplier := uint64(1)
	switch suffix {
	case "K":
		multiplier = 1 << 10
	case "M":
		multiplier = 1 << 20
	case "G":
		multiplier = 1 << 30
	case "T":
		multiplier = 1 << 40
	default:
		value, _ := strconv.ParseUint(raw, 10, 64)
		return value
	}
	value, err := strconv.ParseFloat(raw[:len(raw)-1], 64)
	bytes := value * float64(multiplier)
	if err != nil || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes < 0 || bytes >= float64(^uint64(0)) {
		return 0
	}
	return uint64(bytes)
}

func parseMeminfo(lines []string) (used, total uint64) {
	var available uint64
	var hasTotal, hasAvailable bool
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value
			hasTotal = true
		case "MemAvailable":
			available = value
			hasAvailable = true
		}
	}
	if hasTotal && hasAvailable && total >= available {
		used = total - available
	} else {
		return 0, 0
	}
	return used, total
}

func parseCPULine(lines []string) (cpuCounters, bool) {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		values := make([]uint64, 0, len(fields)-1)
		for _, field := range fields[1:] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return cpuCounters{}, false
			}
			values = append(values, value)
		}
		return cpuCounters{Values: values}, true
	}
	return cpuCounters{}, false
}

func cpuPercent(prev, cur cpuCounters) float64 {
	if len(prev.Values) < 4 || len(cur.Values) < 4 || len(prev.Values) != len(cur.Values) {
		return math.NaN()
	}
	var previousTotal, currentTotal uint64
	for i := range cur.Values {
		previousTotal += prev.Values[i]
		currentTotal += cur.Values[i]
	}
	if currentTotal <= previousTotal {
		return math.NaN()
	}
	previousIdle := prev.Values[3]
	currentIdle := cur.Values[3]
	for _, index := range []int{4} {
		if len(cur.Values) > index {
			previousIdle += prev.Values[index]
			currentIdle += cur.Values[index]
		}
	}
	totalDelta := currentTotal - previousTotal
	if currentIdle < previousIdle {
		return math.NaN()
	}
	busyDelta := totalDelta - (currentIdle - previousIdle)
	return float64(busyDelta) / float64(totalDelta) * 100
}

func parseUptime(lines []string) uint64 {
	if len(lines) == 0 {
		return 0
	}
	fields := strings.Fields(lines[0])
	if len(fields) == 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds >= float64(^uint64(0)) {
		return 0
	}
	return uint64(seconds)
}
