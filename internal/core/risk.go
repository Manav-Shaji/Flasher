package core

import "strings"

type CommandRisk int

const (
	RiskSafe CommandRisk = iota
	RiskNormal
	RiskDangerous
	RiskBlocked
)

func ClassifyCommand(bin string, args []string) (CommandRisk, string) {
	for _, arg := range args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return RiskBlocked, "command contains a NUL or newline"
		}
	}
	if bin != "adb" && bin != "fastboot" {
		return RiskNormal, "unrecognized command"
	}

	args = skipGlobalFlags(bin, args)
	if len(args) == 0 {
		return RiskNormal, "unrecognized command"
	}
	subcommand := args[0]
	rest := args[1:]
	switch bin {
	case "fastboot":
		switch subcommand {
		case "--version", "version", "help", "-h", "--help", "devices", "getvar":
			return RiskSafe, "read-only fastboot command"
		case "reboot":
			return RiskNormal, "reboot changes device state"
		case "erase":
			if len(rest) > 0 && isEraseBlockedPartition(rest[0]) {
				return RiskBlocked, "refusing to erase a critical partition"
			}
			return RiskDangerous, dangerousReason
		case "format", "wipe-super", "set_active", "snapshot-update",
			"delete-logical-partition", "create-logical-partition", "resize-logical-partition":
			return RiskDangerous, dangerousReason
		case "flashing":
			if len(rest) > 0 && (rest[0] == "lock" || rest[0] == "lock_critical") {
				return RiskBlocked, "refusing to lock the bootloader"
			}
			return RiskDangerous, dangerousReason
		case "oem":
			if len(rest) > 0 && rest[0] == "device-info" {
				return RiskSafe, "read-only fastboot device information"
			}
			if len(rest) > 0 && rest[0] == "lock" {
				return RiskBlocked, "refusing to lock the bootloader"
			}
			return RiskDangerous, dangerousReason
		case "flash":
			if len(rest) > 0 && isCriticalPartition(rest[0]) {
				return RiskDangerous, dangerousReason
			}
			return RiskNormal, "flashing changes device state"
		default:
			return RiskNormal, "unrecognized fastboot command"
		}
	case "adb":
		switch subcommand {
		case "devices", "version", "get-state", "get-serialno", "get-devpath", "help", "-h", "--help", "start-server":
			return RiskSafe, "read-only or informational adb command"
		case "reboot":
			return RiskNormal, "reboot changes device state"
		case "logcat":
			if len(rest) > 0 && rest[0] == "-d" {
				return RiskSafe, "logcat dump is read-only"
			}
		case "sideload", "install", "push":
			return RiskNormal, "command changes device state"
		case "shell":
			return classifyShell(rest)
		}
	}
	return RiskNormal, "unrecognized adb command"
}

func IsAllowedCustomFastbootCommand(args []string) bool {
	switch {
	case len(args) == 1 && args[0] == "reboot":
		return true
	case len(args) == 1 && args[0] == "reboot-bootloader":
		return true
	case len(args) == 2 && args[0] == "reboot" &&
		(args[1] == "bootloader" || args[1] == "fastboot" || args[1] == "recovery"):
		return true
	case len(args) == 2 && args[0] == "set_active" &&
		(args[1] == "a" || args[1] == "b"):
		return true
	default:
		return false
	}
}

const dangerousReason = "DANGEROUS: this can erase data or brick the device"

func skipGlobalFlags(bin string, args []string) []string {
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-s", "-H", "-P", "-i", "-b", "-t":
			if i+1 >= len(args) {
				return nil
			}
			i += 2
		case "-d", "-e", "-a":
			if bin != "adb" {
				return args[i:]
			}
			i++
		default:
			return args[i:]
		}
	}
	return nil
}

func isEraseBlockedPartition(partition string) bool {
	switch strings.ToLower(partition) {
	case "boot", "vbmeta", "bootloader", "aboot", "xbl", "abl", "modem", "persist":
		return true
	default:
		return false
	}
}

func isCriticalPartition(partition string) bool {
	switch strings.ToLower(partition) {
	case "bootloader", "aboot", "xbl", "abl", "modem", "persist", "vbmeta", "dtbo":
		return true
	default:
		return false
	}
}

func classifyShell(args []string) (CommandRisk, string) {
	if len(args) == 0 {
		return RiskNormal, "unrecognized shell command"
	}
	hasMeta := false
	for _, arg := range args {
		if strings.ContainsAny(arg, ";&|><`$()") {
			hasMeta = true
		}
	}
	command := args[0]
	dangerous := map[string]bool{"rm": true, "dd": true, "mkfs": true, "format": true, "wipe": true}
	if dangerous[command] || hasMeta || command == "sh" || command == "su" {
		return RiskDangerous, dangerousReason
	}
	safe := false
	switch command {
	case "getprop", "dumpsys", "df", "ls", "uname", "uptime", "id", "date":
		safe = true
	case "cat":
		safe = len(args) > 1
	case "pm":
		safe = len(args) > 1 && args[1] == "list"
	case "settings":
		safe = len(args) > 1 && args[1] == "get"
	case "wm":
		safe = len(args) > 1 && (args[1] == "size" || args[1] == "density")
	}
	if safe {
		return RiskSafe, "read-only shell command"
	}
	return RiskNormal, "unrecognized shell command"
}
